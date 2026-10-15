// Package ui implements the LinGecu desktop interface.
package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lingecu/internal/buffer"
	"lingecu/internal/bundle"
	"lingecu/internal/devicedb"
	"lingecu/internal/minipro"
)

// AppID is the Fyne application ID (used for preferences storage).
const AppID = "io.github.lingecu"

// Version is the LinGecu release.
var Version = "0.1.0" // overridden at build time with -ldflags -X

// maxBufferBytes is the largest chip image kept in memory for hex editing.
// Larger parts (NAND, eMMC, big SPI flash) are processed file-to-file.
const maxBufferBytes = 256 << 20

// App is the running LinGecu instance.
type App struct {
	fapp  fyne.App
	win   fyne.Window
	prefs fyne.Preferences

	runner     *minipro.Runner
	db         *devicedb.DB
	programmer devicedb.Programmer
	progInfo   *minipro.ProgrammerInfo
	device     *devicedb.Device

	code, data     *buffer.Buffer
	codeEd, dataEd *HexEditor
	jedText        *widget.Entry
	codeHolder     *fyne.Container
	largeMode      bool

	opts                                  *optionsPanel
	cfg                                   *configPanel
	logic                                 *logicPanel
	pins                                  *pinPanel
	tabs                                  *container.AppTabs
	tabCode, tabData, tabConfig, tabLogic *container.TabItem
	tabPins                               *container.TabItem

	infoGrid    *widget.Form
	infoLabels  map[string]*widget.Label
	devTitle    *widget.Label
	progLabel   *widget.Label
	progSelect  *widget.Select
	checksumLbl *widget.Label
	cursorLbl   *widget.Label
	status      *widget.Label
	progress    *widget.ProgressBar
	logView     *logView
	actions     []*widget.Button

	mu     sync.Mutex
	busy   bool
	cancel context.CancelFunc
	tmpDir string

	bundle                  *bundle.Files // unpacked embedded minipro, if any
	infoicPath, logicicPath string        // databases shared by the GUI and minipro
	filePath                string        // last file loaded into the code buffer
	recent                  []string

	checksumTimer *time.Timer
}

// Run starts the GUI and blocks until it exits.
func Run() {
	a := newApp(app.NewWithID(AppID))
	defer os.RemoveAll(a.tmpDir)
	go a.startup()
	a.win.ShowAndRun()
}

// newApp builds the main window on the given Fyne app.
func newApp(fa fyne.App) *App {
	a := &App{fapp: fa}
	a.prefs = a.fapp.Preferences()
	a.win = a.fapp.NewWindow("LinGecu")
	a.tmpDir, _ = os.MkdirTemp("", "lingecu-")

	applyTheme(a.fapp, a.prefs.StringWithFallback("theme", "System"))
	a.programmer = devicedb.Programmer(a.prefs.StringWithFallback("programmer", string(devicedb.T48)))
	a.recent = a.prefs.StringListWithFallback("recentDevices", nil)
	a.code = buffer.New(0, 0xff)
	a.data = buffer.New(0, 0xff)
	a.build()
	a.win.Resize(fyne.NewSize(1280, 820))
	a.win.SetMaster()
	a.win.SetOnDropped(func(_ fyne.Position, uris []fyne.URI) {
		if len(uris) > 0 {
			a.openFile(uris[0].Path(), buffer.FormatAuto)
		}
	})
	a.win.SetCloseIntercept(func() {
		if a.code.Modified() || a.data.Modified() {
			dialog.ShowConfirm("Quit", "The buffer has unsaved changes. Quit anyway?", func(ok bool) {
				if ok {
					a.win.Close()
				}
			}, a.win)
			return
		}
		a.win.Close()
	})
	return a
}

// startup locates minipro, loads the database and detects the programmer.
func (a *App) startup() {
	a.logf("LinGecu %s — a GUI for minipro", Version)
	var bin string
	var err error
	if a.prefs.String("miniproPath") == "" && bundle.Available() {
		// Release builds carry their own minipro and matching database.
		if a.bundle, err = bundle.Extract(); err == nil {
			bin = a.bundle.Minipro
		} else {
			a.logf("Could not unpack the bundled minipro: %v", err)
		}
	}
	if bin == "" {
		bin, err = minipro.Locate(a.prefs.String("miniproPath"))
	}
	if err != nil {
		a.logf("ERROR: %v", err)
	} else {
		a.runner = &minipro.Runner{Binary: bin}
		if banner, share, err := a.runner.Version(context.Background()); err == nil {
			a.logf("Using %s (%s)", bin, banner)
			if a.prefs.String("infoicPath") == "" && share != "" {
				a.prefs.SetString("autoShareDir", share)
			}
		}
	}
	a.loadDatabase()
	a.detectProgrammer(false)
}

func (a *App) findDataFile(name, prefKey string) string {
	if p := a.prefs.String(prefKey); p != "" {
		return p
	}
	// The bundled minipro must use the database it was built with.
	if a.bundle != nil && a.runner != nil && a.runner.Binary == a.bundle.Minipro {
		return filepath.Join(a.bundle.Dir, name)
	}
	var cands []string
	if share := a.prefs.String("autoShareDir"); share != "" {
		cands = append(cands, filepath.Join(share, name))
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		cands = append(cands, filepath.Join(d, name))
	}
	cands = append(cands, "/usr/local/share/minipro/"+name, "/usr/share/minipro/"+name)
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func (a *App) loadDatabase() {
	infoic := a.findDataFile("infoic.xml", "infoicPath")
	if infoic == "" {
		a.logf("ERROR: infoic.xml not found; set its location in Settings")
		return
	}
	logic := a.findDataFile("logicic.xml", "logicicPath")
	a.mu.Lock()
	a.infoicPath, a.logicicPath = infoic, logic
	a.mu.Unlock()
	a.setStatus("Loading device database…")
	start := time.Now()
	db, err := devicedb.Load(infoic, logic)
	if err != nil {
		a.logf("ERROR loading device database: %v", err)
		return
	}
	a.db = db
	a.logf("Loaded %d devices for %s from %s in %v", len(db.Devices(a.programmer)), a.programmer.Label(), infoic, time.Since(start).Round(time.Millisecond))
	a.setStatus("Ready")
	if last := a.prefs.String("lastDevice"); last != "" {
		if d := db.Find(a.programmer, last); d != nil {
			fyne.Do(func() { a.selectDevice(d) })
		}
	}
}

// detectProgrammer queries the connected programmer (-k).
func (a *App) detectProgrammer(verbose bool) {
	if a.runner == nil {
		return
	}
	info, err := a.runner.Detect(context.Background())
	if err != nil {
		a.logf("Programmer detection failed: %v", err)
		return
	}
	a.progInfo = info
	fyne.Do(func() {
		if !info.Found {
			a.progLabel.SetText("Programmer: not connected")
			if verbose {
				a.logf("%s", info.Raw)
			}
			return
		}
		a.progLabel.SetText("Programmer: " + info.Label + " " + info.Firmware)
		a.logf("Detected programmer: %s, firmware %s", info.Label, info.Firmware)
		for _, w := range info.Warnings {
			a.logf("WARNING: %s", w)
			if strings.Contains(w, "Firmware is out of date") {
				a.logf("  Update it with Tools → Firmware update… (file from xgecu.com)")
			}
		}
		if p := devicedb.Programmer(info.Model); p != a.programmer {
			a.progSelect.SetSelected(p.Label())
		}
	})
}

// ---- layout ----

func (a *App) build() {
	a.logView = newLogView()
	a.progLabel = widget.NewLabel("Programmer: …")
	a.progress = widget.NewProgressBar()
	a.status = widget.NewLabel("Starting…")
	a.cursorLbl = widget.NewLabel("")
	a.checksumLbl = widget.NewLabel("Checksum: —")
	a.checksumLbl.TextStyle = fyne.TextStyle{Monospace: true}

	a.codeEd = NewHexEditor(a.code)
	a.dataEd = NewHexEditor(a.data)
	for _, ed := range []*HexEditor{a.codeEd, a.dataEd} {
		ed := ed
		ed.OnCursor = func(off int) {
			txt := fmt.Sprintf("Offset 0x%08X  value 0x%02X (%d)", off, ed.Buffer().At(off), ed.Buffer().At(off))
			if s, e, ok := ed.Selection(); ok {
				txt += fmt.Sprintf("   selection 0x%X–0x%X (%d bytes)", s, e-1, e-s)
			}
			a.cursorLbl.SetText(txt)
		}
	}
	a.code.OnChange(a.scheduleChecksum)
	a.jedText = widget.NewMultiLineEntry()
	a.jedText.TextStyle = fyne.TextStyle{Monospace: true}
	a.jedText.SetPlaceHolder("JEDEC fuse map — open a .jed file or read the device")
	a.codeHolder = container.NewStack(a.codeEd)

	a.opts = newOptionsPanel(a)
	a.cfg = newConfigPanel(a)
	a.logic = newLogicPanel(a)
	a.pins = newPinPanel(a)

	a.tabCode = container.NewTabItemWithIcon("Code Memory", theme.FileIcon(), a.codeHolder)
	a.tabData = container.NewTabItemWithIcon("Data Memory (EEPROM)", theme.StorageIcon(), a.dataEd)
	a.tabConfig = container.NewTabItemWithIcon("Config / Fuses", theme.SettingsIcon(), a.cfg.content)
	a.tabLogic = container.NewTabItemWithIcon("Logic IC Test", theme.ComputerIcon(), a.logic.content)
	a.tabPins = container.NewTabItemWithIcon("ZIF Socket / Pins", theme.RadioButtonCheckedIcon(), a.pins.content)
	a.tabs = container.NewAppTabs(a.tabCode, a.tabData, a.tabConfig, a.tabLogic, a.tabPins)

	left := container.NewVScroll(container.NewVBox(a.devicePanel(), widget.NewSeparator(), a.opts.content))
	left.SetMinSize(fyne.NewSize(330, 0))

	bottom := container.NewBorder(nil,
		container.NewBorder(nil, nil, a.status, a.cursorLbl, a.progress),
		nil, nil, a.logView.list)

	center := container.NewVSplit(a.tabs, bottom)
	center.Offset = 0.70
	main := container.NewHSplit(left, center)
	main.Offset = 0.27

	a.win.SetMainMenu(a.menu())
	a.win.SetContent(container.NewBorder(a.toolbar(), nil, nil, nil, main))
	a.addShortcuts()
	a.refreshDevice()
}

func (a *App) toolbar() fyne.CanvasObject {
	btn := func(label string, icon fyne.Resource, f func()) *widget.Button {
		b := widget.NewButtonWithIcon(label, icon, f)
		b.Importance = widget.LowImportance
		a.actions = append(a.actions, b)
		return b
	}
	names := make([]string, len(devicedb.Programmers))
	for i, p := range devicedb.Programmers {
		names[i] = p.Label()
	}
	a.progSelect = widget.NewSelect(names, func(s string) {
		for _, p := range devicedb.Programmers {
			if p.Label() == s && p != a.programmer {
				a.setProgrammer(p)
			}
		}
	})
	a.progSelect.SetSelected(a.programmer.Label())
	stop := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), a.stop)
	stop.Importance = widget.DangerImportance

	return container.NewVBox(container.NewHBox(
		btn("Select IC", theme.SearchIcon(), a.showDevicePicker),
		btn("Open", theme.FolderOpenIcon(), a.openDialog),
		btn("Save", theme.DocumentSaveIcon(), a.saveDialog),
		widget.NewSeparator(),
		btn("Read", theme.DownloadIcon(), a.doRead),
		btn("Program", theme.UploadIcon(), a.doProgram),
		btn("Verify", theme.ConfirmIcon(), a.doVerify),
		btn("Blank", theme.CheckButtonIcon(), a.doBlankCheck),
		btn("Erase", theme.DeleteIcon(), a.doErase),
		btn("Read ID", theme.InfoIcon(), a.doReadID),
		btn("Auto", theme.MediaPlayIcon(), a.showAutoProgram),
		widget.NewSeparator(),
		btn("Logic test", theme.ComputerIcon(), func() { a.tabs.Select(a.tabLogic); a.logic.run() }),
		btn("Info", theme.HelpIcon(), a.showDeviceInfo),
		stop,
		layoutSpacerObj(),
		a.progSelect,
	), widget.NewSeparator())
}

func (a *App) devicePanel() fyne.CanvasObject {
	a.devTitle = widget.NewLabelWithStyle("No device selected", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	a.devTitle.Wrapping = fyne.TextWrapWord
	a.infoLabels = map[string]*widget.Label{}
	var items []*widget.FormItem
	for _, k := range []string{"Manufacturer", "Type", "Package", "Socket", "Code size", "Data size", "Page size", "Chip ID", "ICSP", "Config"} {
		l := widget.NewLabel("—")
		l.Wrapping = fyne.TextWrapWord
		a.infoLabels[k] = l
		items = append(items, widget.NewFormItem(k, l))
	}
	a.infoGrid = widget.NewForm(items...)
	sel := widget.NewButtonWithIcon("Select IC…", theme.SearchIcon(), a.showDevicePicker)
	sel.Importance = widget.HighImportance
	return container.NewVBox(
		container.NewBorder(nil, nil, nil, sel, a.devTitle),
		a.infoGrid,
		a.checksumLbl,
		a.progLabel,
	)
}

func (a *App) menu() *fyne.MainMenu {
	item := fyne.NewMenuItem
	sep := fyne.NewMenuItemSeparator
	file := fyne.NewMenu("File",
		item("Open file into buffer…", a.openDialog),
		item("Open file at offset…", a.openAtOffsetDialog),
		item("Save buffer as…", a.saveDialog),
		item("Save selection as…", a.saveSelectionDialog),
		sep(),
		item("Load project…", a.loadProjectDialog),
		item("Save project…", a.saveProjectDialog),
		sep(),
		item("Settings…", a.showSettings),
	)
	buf := fyne.NewMenu("Buffer",
		item("Go to address…", a.gotoDialog),
		item("Select block…", a.selectBlockDialog),
		item("Find / replace…", a.findDialog),
		sep(),
		item("Fill block…", a.fillDialog),
		item("Copy block…", a.copyBlockDialog),
		item("Swap bytes…", a.swapDialog),
		item("Invert bits (XOR FF)", a.invertSelection),
		item("Clear buffer (blank)", a.clearBuffer),
		sep(),
		item("Checksum calculator…", a.checksumDialog),
		item("Compare buffer with file…", a.compareDialog),
		item("Serial number / insert value…", a.serialInsertDialog),
	)
	dev := fyne.NewMenu("Device",
		item("Select IC…", a.showDevicePicker),
		item("Device information", a.showDeviceInfo),
		sep(),
		item("Read", a.doRead),
		item("Program", a.doProgram),
		item("Verify", a.doVerify),
		item("Blank check", a.doBlankCheck),
		item("Erase", a.doErase),
		item("Read chip ID", a.doReadID),
		item("Pin contact check", a.doPinCheck),
		sep(),
		item("Auto-detect SPI 25xx (8-pin)", func() { a.doSPIAutodetect(8) }),
		item("Auto-detect SPI 25xx (16-pin)", func() { a.doSPIAutodetect(16) }),
		item("Identify chip by ID", a.doIdentify),
		sep(),
		item("Auto program / production mode…", a.showAutoProgram),
	)
	tools := fyne.NewMenu("Tools",
		item("Logic IC test", func() { a.tabs.Select(a.tabLogic) }),
		item("Programmer self-test (hardware check)", a.doHardwareCheck),
		item("Detect connected programmer", func() { go a.detectProgrammer(true) }),
		item("Firmware update…", a.firmwareUpdateDialog),
		sep(),
		item("Install USB permissions (udev rules)…", a.installUdevRules),
		item("Install T56/T76 algorithms…", a.algorithmsHelp),
		item("Run custom minipro command…", a.customCommandDialog),
	)
	help := fyne.NewMenu("Help",
		item("minipro version", a.showMiniproVersion),
		item("Supported programmers", a.showSupportedProgrammers),
		item("About LinGecu", a.showAbout),
	)
	return fyne.NewMainMenu(file, buf, dev, tools, help)
}

// ---- device selection ----

func (a *App) setProgrammer(p devicedb.Programmer) {
	a.programmer = p
	a.prefs.SetString("programmer", string(p))
	a.logf("Programmer model set to %s", p.Label())
	a.opts.refreshForDevice()
	if a.device != nil && a.db != nil {
		if d := a.db.Find(p, a.device.Name); d != nil {
			a.selectDevice(d)
		} else {
			a.logf("%s is not supported by %s; please select another IC", a.device.Name, p.Label())
			a.device = nil
			a.refreshDevice()
		}
	}
}

func (a *App) selectDevice(d *devicedb.Device) {
	a.device = d
	a.prefs.SetString("lastDevice", d.Name)
	a.addRecent(d.Name)
	a.logf("Selected %s (%s, %s)", d.Name, d.Manufacturer, d.TypeName())
	a.refreshDevice()
}

func (a *App) addRecent(name string) {
	out := []string{name}
	for _, r := range a.recent {
		if r != name && len(out) < 20 {
			out = append(out, r)
		}
	}
	a.recent = out
	a.prefs.SetStringList("recentDevices", out)
}

// refreshDevice updates every panel after the device changes.
func (a *App) refreshDevice() {
	d := a.device
	set := func(k, v string) { a.infoLabels[k].SetText(v) }
	if d == nil {
		a.devTitle.SetText("No device selected")
		for k := range a.infoLabels {
			set(k, "—")
		}
		a.opts.refreshForDevice()
		a.cfg.refreshForDevice()
		a.logic.refreshForDevice()
		a.pins.refreshForDevice()
		return
	}
	a.devTitle.SetText(d.Name)
	set("Manufacturer", d.Manufacturer)
	set("Type", d.TypeName())
	set("Package", d.Package())
	set("Socket", d.SocketInfo())
	set("Code size", sizeString(d.CodeBytes(), d.Type == devicedb.TypePLD))
	set("Data size", sizeString(d.DataBytes(), false))
	set("Page size", sizeString(int(d.PageSize), false))
	set("Chip ID", d.ChipIDString())
	set("ICSP", d.ICSPMode())
	cfg := "—"
	if c := a.db.Config(d); c != nil {
		var n []string
		for _, f := range c.Fuses {
			n = append(n, f.Name)
		}
		for _, l := range c.Locks {
			n = append(n, l.Name)
		}
		cfg = strings.Join(n, ", ")
	} else if d.Type == devicedb.TypePLD {
		cfg = "JEDEC fuse map"
	}
	set("Config", cfg)

	if d.Type == devicedb.TypeLogic {
		a.tabs.Select(a.tabLogic)
	} else {
		size := d.CodeBytes()
		a.largeMode = size > maxBufferBytes || d.Type == devicedb.TypeEMMC
		switch {
		case d.Type == devicedb.TypePLD:
			a.codeHolder.Objects = []fyne.CanvasObject{a.jedText}
		case a.largeMode:
			a.code.Resize(0, d.BlankValue)
			a.codeHolder.Objects = []fyne.CanvasObject{container.NewCenter(widget.NewLabel(
				"This device is too large to edit in memory.\nRead and Program work directly with files."))}
		default:
			a.code.Resize(size, d.BlankValue)
			a.codeHolder.Objects = []fyne.CanvasObject{a.codeEd}
		}
		a.codeHolder.Refresh()
		a.data.Resize(d.DataBytes(), 0xff)
		a.tabs.Select(a.tabCode)
	}
	if d.HasData() {
		a.tabData.Text = fmt.Sprintf("Data Memory (%s)", sizeString(d.DataBytes(), false))
	} else {
		a.tabData.Text = "Data Memory (none)"
	}
	a.tabs.Refresh()
	a.opts.refreshForDevice()
	a.cfg.refreshForDevice()
	a.logic.refreshForDevice()
	a.pins.refreshForDevice()
	a.scheduleChecksum()
}

func sizeString(n int, bits bool) string {
	if n == 0 {
		return "—"
	}
	unit := "bytes"
	if bits {
		unit = "fuses"
	}
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB (0x%X %s)", n>>20, n, unit)
	case n >= 1024 && n%1024 == 0:
		return fmt.Sprintf("%d KiB (0x%X %s)", n>>10, n, unit)
	}
	return fmt.Sprintf("%d %s (0x%X)", n, unit, n)
}

// ---- checksum ----

func (a *App) scheduleChecksum() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.checksumTimer != nil {
		a.checksumTimer.Stop()
	}
	a.checksumTimer = time.AfterFunc(250*time.Millisecond, func() {
		c := buffer.Compute(a.code.Bytes())
		txt := fmt.Sprintf("Checksum: %04X (sum16)   CRC32: %08X", c.Sum16, c.CRC32)
		fyne.Do(func() { a.checksumLbl.SetText(txt) })
	})
}

// ---- logging & status ----

func (a *App) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	fyne.Do(func() { a.logView.add(line) })
}

func (a *App) setStatus(s string) {
	fyne.Do(func() { a.status.SetText(s) })
}

func (a *App) showError(err error) {
	fyne.Do(func() { dialog.ShowError(err, a.win) })
}

// requireDevice shows an error and returns false when no device is selected.
func (a *App) requireDevice() bool {
	if a.device == nil {
		dialog.ShowInformation("No device", "Please select a device first (Select IC).", a.win)
		return false
	}
	if a.runner == nil {
		dialog.ShowError(errors.New("minipro executable not found; configure it in File → Settings"), a.win)
		return false
	}
	return true
}

// ---- running minipro ----

// runFunc runs one minipro invocation inside a job.
type runFunc func(args []string) (*minipro.Result, error)

// errFailed signals that minipro reported an error (already logged).
var errFailed = errors.New("operation failed")

// job runs fn in the background with progress, logging and cancellation.
// fn may call run several times; it executes off the UI goroutine.
func (a *App) job(title string, fn func(ctx context.Context, run runFunc) error) {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		dialog.ShowInformation("Busy", "Another operation is still running.", a.win)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.busy, a.cancel = true, cancel
	a.mu.Unlock()

	for _, b := range a.actions {
		b.Disable()
	}
	a.progress.SetValue(0)
	a.status.SetText(title + "…")
	a.logf("──── %s ────", title)
	start := time.Now()

	go func() {
		run := func(args []string) (*minipro.Result, error) {
			args = a.withDatabases(args)
			a.logf("$ minipro %s", shellJoin(args))
			res, err := a.runner.Run(ctx, args, func(ev minipro.Event) {
				fyne.Do(func() {
					switch ev.Kind {
					case minipro.EvProgress:
						if ev.Percent >= 0 {
							a.progress.SetValue(float64(ev.Percent) / 100)
							a.status.SetText(fmt.Sprintf("%s %d%%", ev.Text, ev.Percent))
						} else {
							a.status.SetText(ev.Text)
						}
					case minipro.EvLine:
						if ev.Text != "" {
							a.logView.add(ev.Text)
						}
					}
				})
			}, a.prompt)
			if res != nil && slices.Contains(args, "-z") {
				out := res.Output
				fyne.Do(func() { a.pins.applyResult(out) })
			}
			if err != nil {
				return res, err
			}
			if !res.OK() {
				return res, fmt.Errorf("%w (minipro exit code %d)", errFailed, res.ExitCode)
			}
			return res, nil
		}
		err := fn(ctx, run)
		elapsed := time.Since(start).Round(10 * time.Millisecond)
		fyne.Do(func() {
			a.mu.Lock()
			a.busy, a.cancel = false, nil
			a.mu.Unlock()
			for _, b := range a.actions {
				b.Enable()
			}
			switch {
			case errors.Is(err, context.Canceled):
				a.logView.add(fmt.Sprintf("✗ %s cancelled", title))
				a.status.SetText(title + " cancelled")
			case err != nil:
				a.logView.add(fmt.Sprintf("✗ %s FAILED: %v", title, err))
				a.status.SetText(title + " failed")
				a.progress.SetValue(0)
			default:
				a.logView.add(fmt.Sprintf("✓ %s OK (%v)", title, elapsed))
				a.status.SetText(fmt.Sprintf("%s OK — %v", title, elapsed))
				a.progress.SetValue(1)
			}
		})
	}()
}

// prompt answers minipro's interactive y/n questions with a dialog.
func (a *App) prompt(q string) string {
	ch := make(chan bool, 1)
	fyne.Do(func() {
		dialog.ShowConfirm("minipro asks", q, func(ok bool) { ch <- ok }, a.win)
	})
	if <-ch {
		return "y"
	}
	return "n"
}

func (a *App) stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
}

func shellJoin(args []string) string {
	out := make([]string, len(args))
	for i, s := range args {
		if s == "" || strings.ContainsAny(s, " \t\"'@()$&;|<>*?") {
			out[i] = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		} else {
			out[i] = s
		}
	}
	return strings.Join(out, " ")
}

func (a *App) tmp(name string) string { return filepath.Join(a.tmpDir, name) }

// ---- small widgets ----

type logView struct {
	lines []string
	list  *widget.List
}

func newLogView() *logView {
	l := &logView{}
	l.list = widget.NewList(
		func() int { return len(l.lines) },
		func() fyne.CanvasObject {
			t := widget.NewLabel("")
			t.TextStyle = fyne.TextStyle{Monospace: true}
			t.Truncation = fyne.TextTruncateEllipsis
			return t
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			lbl := o.(*widget.Label)
			lbl.SetText(l.lines[id])
			s := l.lines[id]
			s = s[min(len(s), 9):] // strip the timestamp
			switch {
			case strings.HasPrefix(s, "✗"), strings.HasPrefix(s, "ERROR"), strings.Contains(s, "mismatch"), strings.Contains(s, "failed"):
				lbl.Importance = widget.DangerImportance
			case strings.HasPrefix(s, "✓"):
				lbl.Importance = widget.SuccessImportance
			case strings.HasPrefix(s, "$ "), strings.HasPrefix(s, "────"):
				lbl.Importance = widget.LowImportance
			default:
				lbl.Importance = widget.MediumImportance
			}
			lbl.Refresh()
		},
	)
	return l
}

func (l *logView) add(s string) {
	for _, line := range strings.Split(s, "\n") {
		l.lines = append(l.lines, time.Now().Format("15:04:05 ")+line)
	}
	if len(l.lines) > 5000 {
		l.lines = l.lines[len(l.lines)-5000:]
	}
	l.list.Refresh()
	l.list.ScrollToBottom()
}
