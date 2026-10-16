package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"lingecu/internal/buffer"
	"lingecu/internal/devicedb"
	"lingecu/internal/minipro"
)

// area is one memory region of the device.
type area struct {
	page string // minipro -c value
	name string
}

// selectedAreas returns the memory areas ticked in the options panel that
// the current device actually has.
func (a *App) selectedAreas() []area {
	d := a.device
	var out []area
	if d.Type == devicedb.TypePLD {
		return []area{{"", "JEDEC"}}
	}
	if a.opts.areaCode.Checked || (!d.HasData() && !d.HasConfig()) {
		out = append(out, area{"code", "Code"})
	}
	if d.HasData() && a.opts.areaData.Checked {
		out = append(out, area{"data", "Data"})
	}
	if d.HasConfig() && a.opts.areaConfig.Checked {
		out = append(out, area{"config", "Config"})
	}
	if d.HasCalibration() && a.opts.areaCalib.Checked {
		out = append(out, area{"calibration", "Calibration"})
	}
	if a.opts.areaUser.Checked && d.Type == devicedb.TypeMCU {
		out = append(out, area{"user", "User"})
	}
	return out
}

// ---- Read ----

func (a *App) doRead() {
	if !a.requireDevice() || a.isLogic() {
		return
	}
	if a.largeMode {
		a.fileSaveDialog("Read device to file", a.device.BaseName()+".bin", func(path string) {
			o := a.opts.options()
			a.job("Read "+a.device.Name, func(_ context.Context, run runFunc) error {
				_, err := run(o.Args(minipro.ActRead, path))
				return err
			})
		})
		return
	}
	d, o, areas := a.device, a.opts.options(), a.selectedAreas()
	a.job("Read "+d.Name, func(_ context.Context, run runFunc) error {
		for _, ar := range areas {
			if err := a.readArea(run, o, d, ar); err != nil {
				return err
			}
		}
		return nil
	})
}

// readArea reads one area from the chip into its buffer or panel.
func (a *App) readArea(run runFunc, o minipro.Options, d *devicedb.Device, ar area) error {
	o.Page = ar.page
	switch ar.page {
	case "":
		f := a.tmp("read.jed")
		if _, err := run(o.Args(minipro.ActRead, f)); err != nil {
			return err
		}
		txt, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		fyne.Do(func() { a.jedText.SetText(string(txt)) })
	case "config", "calibration", "user":
		f := a.tmp(ar.page + ".conf")
		if ar.page == "user" {
			f = a.tmp("user.bin")
		}
		if _, err := run(o.Args(minipro.ActRead, f)); err != nil {
			return err
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		fyne.Do(func() { a.cfg.loadFromChip(ar.page, raw) })
	default:
		f := a.tmp(ar.page + ".bin")
		if _, err := run(o.Args(minipro.ActRead, f)); err != nil {
			return err
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		buf := a.code
		if ar.page == "data" {
			buf = a.data
		}
		buf.Load(raw, true)
		a.logf("%s memory: %d bytes loaded into buffer", ar.name, len(raw))
	}
	return nil
}

// ---- Program ----

func (a *App) doProgram() {
	if !a.requireDevice() || a.isLogic() {
		return
	}
	if a.device.ReadOnly() {
		dialog.ShowInformation("Read only", a.device.Name+" can only be read.", a.win)
		return
	}
	if a.largeMode {
		a.fileOpenDialog("Program device from file", func(path string) {
			o := a.opts.options()
			a.job("Program "+a.device.Name, func(_ context.Context, run runFunc) error {
				_, err := run(o.Args(minipro.ActWrite, path))
				return err
			})
		})
		return
	}
	a.job("Program "+a.device.Name, a.programSteps(a.device, a.opts.options(), a.selectedAreas()))
}

// programSteps writes the selected areas from the current buffers.
func (a *App) programSteps(d *devicedb.Device, o minipro.Options, areas []area) func(context.Context, runFunc) error {
	codeImg, dataImg := a.code.Bytes(), a.data.Bytes()
	jed := a.jedText.Text
	cfgText, cfgErr := a.cfg.configText()
	blankFirst := a.opts.blankFirst.Checked
	return func(_ context.Context, run runFunc) error {
		if blankFirst && !o.SkipErase {
			a.logf("Note: blank check before programming skipped because the chip is erased first")
		} else if blankFirst {
			bo := o
			bo.Page = ""
			if _, err := run(bo.Args(minipro.ActBlankCheck, "")); err != nil {
				return fmt.Errorf("blank check: %w", err)
			}
		}
		return a.writeAreas(run, o, areas, codeImg, dataImg, jed, cfgText, cfgErr)
	}
}

// writeAreas programs each area. Only the first write may erase the chip
// (otherwise programming data would wipe the code just written), and
// protection is enabled only with the last area.
func (a *App) writeAreas(run runFunc, o minipro.Options, areas []area, codeImg, dataImg []byte, jed, cfgText string, cfgErr error) error {
	for i, ar := range areas {
		wo := o
		wo.Page = ar.page
		if i > 0 {
			wo.SkipErase, wo.Unprotect = true, false
		}
		if i < len(areas)-1 {
			wo.Protect = false
		}
		f, content, err := a.areaFile("write", ar, codeImg, dataImg, jed, cfgText, cfgErr)
		if err != nil {
			return err
		}
		if f == "" {
			continue
		}
		if content != nil {
			if err := writeTemp(f, content); err != nil {
				return err
			}
		}
		if _, err := run(wo.Args(minipro.ActWrite, f)); err != nil {
			return fmt.Errorf("%s area: %w", ar.name, err)
		}
	}
	return nil
}

// verifyAreas compares each area on the chip with the given images.
func (a *App) verifyAreas(run runFunc, o minipro.Options, areas []area, codeImg, dataImg []byte, jed, cfgText string, cfgErr error) error {
	for _, ar := range areas {
		vo := o
		vo.Page = ar.page
		f, content, err := a.areaFile("verify", ar, codeImg, dataImg, jed, cfgText, cfgErr)
		if err != nil {
			return err
		}
		if f == "" || ar.page == "user" {
			continue
		}
		if err := writeTemp(f, content); err != nil {
			return err
		}
		if _, err := run(vo.Args(minipro.ActVerify, f)); err != nil {
			return fmt.Errorf("%s area: %w", ar.name, err)
		}
	}
	return nil
}

// areaFile returns the temp file name and content for an area. An empty
// name means the area is skipped; nil content means the file already exists.
func (a *App) areaFile(prefix string, ar area, codeImg, dataImg []byte, jed, cfgText string, cfgErr error) (string, []byte, error) {
	switch ar.page {
	case "":
		if strings.TrimSpace(jed) == "" {
			return "", nil, errors.New("no JEDEC data: open a .jed file first")
		}
		return a.tmp(prefix + ".jed"), []byte(jed), nil
	case "config":
		if cfgErr != nil {
			return "", nil, cfgErr
		}
		return a.tmp(prefix + ".conf"), []byte(cfgText), nil
	case "calibration":
		return "", nil, nil // read-only
	case "user":
		f := a.tmp("user.bin")
		if _, err := os.Stat(f); err != nil {
			a.logf("User area: no data loaded, skipped")
			return "", nil, nil
		}
		return f, nil, nil
	case "data":
		return a.tmp(prefix + "_data.bin"), dataImg, nil
	}
	return a.tmp(prefix + "_code.bin"), codeImg, nil
}

func writeTemp(path string, content []byte) error { return os.WriteFile(path, content, 0o644) }

// ---- Verify ----

func (a *App) doVerify() {
	if !a.requireDevice() || a.isLogic() {
		return
	}
	if a.largeMode {
		a.fileOpenDialog("Verify device against file", func(path string) {
			o := a.opts.options()
			a.job("Verify "+a.device.Name, func(_ context.Context, run runFunc) error {
				_, err := run(o.Args(minipro.ActVerify, path))
				return err
			})
		})
		return
	}
	d, o, areas := a.device, a.opts.options(), a.selectedAreas()
	codeImg, dataImg, jed := a.code.Bytes(), a.data.Bytes(), a.jedText.Text
	cfgText, cfgErr := a.cfg.configText()
	a.job("Verify "+d.Name, func(_ context.Context, run runFunc) error {
		return a.verifyAreas(run, o, areas, codeImg, dataImg, jed, cfgText, cfgErr)
	})
}

// ---- Blank check / erase / ID ----

func (a *App) doBlankCheck() {
	if !a.requireDevice() || a.isLogic() {
		return
	}
	o := a.opts.options()
	o.Page = ""
	a.job("Blank check "+a.device.Name, func(_ context.Context, run runFunc) error {
		_, err := run(o.Args(minipro.ActBlankCheck, ""))
		return err
	})
}

func (a *App) doErase() {
	if !a.requireDevice() || a.isLogic() {
		return
	}
	if !a.device.CanErase() {
		dialog.ShowInformation("Erase", a.device.Name+" does not support electrical erase.", a.win)
		return
	}
	dialog.ShowConfirm("Erase", "Erase all contents of "+a.device.Name+"?", func(ok bool) {
		if !ok {
			return
		}
		o := a.opts.options()
		a.job("Erase "+a.device.Name, func(_ context.Context, run runFunc) error {
			_, err := run(o.Args(minipro.ActErase, ""))
			return err
		})
	}, a.win)
}

func (a *App) doReadID() {
	if !a.requireDevice() || a.isLogic() {
		return
	}
	d, o := a.device, a.opts.options()
	o.NoIDError = true // report the ID even when it does not match
	a.job("Read ID "+d.Name, func(_ context.Context, run runFunc) error {
		res, err := run(o.Args(minipro.ActReadID, ""))
		if res == nil {
			return err
		}
		id, ok := minipro.ParseChipID(res.Output)
		if !ok {
			if err == nil {
				err = errors.New("no chip ID reported (device may not have one)")
			}
			return err
		}
		if d.ChipID != 0 && id != d.ChipID {
			a.logf("Chip ID 0x%X does NOT match expected %s", id, d.ChipIDString())
			a.offerDevicesForID(id)
			return errors.New("chip ID mismatch")
		}
		a.logf("Chip ID 0x%X matches %s", id, d.Name)
		return err
	})
}

// doIdentify reads the ID with the current device's protocol and lists every
// database device with that ID.
func (a *App) doIdentify() {
	if !a.requireDevice() || a.isLogic() {
		return
	}
	o := a.opts.options()
	o.NoIDError = true
	a.job("Identify chip", func(_ context.Context, run runFunc) error {
		res, _ := run(o.Args(minipro.ActReadID, ""))
		if res == nil {
			return errFailed
		}
		id, ok := minipro.ParseChipID(res.Output)
		if !ok {
			return errors.New("no chip ID reported")
		}
		a.offerDevicesForID(id)
		return nil
	})
}

func (a *App) offerDevicesForID(id uint32) {
	if a.db == nil {
		return
	}
	devs := a.db.FindByID(a.programmer, id)
	fyne.Do(func() {
		if len(devs) == 0 {
			dialog.ShowInformation("Identify", fmt.Sprintf("No device in the database has chip ID 0x%X.", id), a.win)
			return
		}
		a.chooseDeviceDialog(fmt.Sprintf("Devices with chip ID 0x%X", id), devs)
	})
}

// chooseDeviceDialog lets the user pick one of devs.
func (a *App) chooseDeviceDialog(title string, devs []*devicedb.Device) {
	sel := -1
	list := widget.NewList(func() int { return len(devs) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(fmt.Sprintf("%s  —  %s, %s", devs[i].Name, devs[i].Manufacturer, devs[i].Package()))
		})
	list.OnSelected = func(i widget.ListItemID) { sel = i }
	dlg := dialog.NewCustomConfirm(title, "Select", "Cancel", container.NewGridWrap(fyne.NewSize(520, 320), list), func(ok bool) {
		if ok && sel >= 0 {
			a.selectDevice(devs[sel])
		}
	}, a.win)
	dlg.Show()
}

var autodetectIDRe = regexp.MustCompile(`ID:\s*(0x[0-9A-Fa-f]+)`)

func (a *App) doSPIAutodetect(pins int) {
	if a.runner == nil {
		a.requireDevice()
		return
	}
	args := []string{"-a", strconv.Itoa(pins)}
	if a.opts.pinCheck.Checked {
		args = append(args, "-z")
	}
	a.job(fmt.Sprintf("Auto-detect SPI flash (%d-pin)", pins), func(_ context.Context, run runFunc) error {
		res, err := run(args)
		if err != nil {
			return err
		}
		var devs []*devicedb.Device
		for _, l := range strings.Split(res.Stdout, "\n") {
			if l = strings.TrimSpace(l); l == "" || a.db == nil {
				continue
			}
			if d := a.db.Find(a.programmer, l); d != nil {
				devs = append(devs, d)
			}
		}
		if len(devs) == 0 && a.db != nil {
			if m := autodetectIDRe.FindStringSubmatch(res.Output); m != nil {
				id, _ := strconv.ParseUint(m[1], 0, 32)
				devs = a.db.FindByID(a.programmer, uint32(id))
			}
		}
		if len(devs) > 0 {
			fyne.Do(func() { a.chooseDeviceDialog("Detected SPI flash", devs) })
		}
		return nil
	})
}

// ---- programmer tools ----

func (a *App) doHardwareCheck() {
	if a.runner == nil {
		a.requireDevice()
		return
	}
	dialog.ShowConfirm("Programmer self-test",
		"Remove any chip from the ZIF socket before running the hardware check. Continue?", func(ok bool) {
			if ok {
				a.job("Hardware self-test", func(_ context.Context, run runFunc) error {
					_, err := run([]string{"-t"})
					return err
				})
			}
		}, a.win)
}

func (a *App) firmwareUpdateDialog() {
	if a.runner == nil {
		a.requireDevice()
		return
	}
	dialog.ShowConfirm("Firmware update",
		"Select the firmware file for your programmer:\n\n"+
			"  TL866A/CS: update.dat\n  TL866II+: updateII.dat\n  T48: UpdateT48.dat\n  T56: updateT56.dat\n  T76: updateT76.dat\n\n"+
			"Do not unplug the programmer during the update. Continue?", func(ok bool) {
			if !ok {
				return
			}
			a.fileOpenDialog("Firmware update file", func(path string) {
				a.job("Firmware update", func(_ context.Context, run runFunc) error {
					_, err := run([]string{"-F", path})
					return err
				})
			})
		}, a.win)
}

func (a *App) customCommandDialog() {
	if a.runner == nil {
		a.requireDevice()
		return
	}
	e := widget.NewEntry()
	if a.device != nil {
		e.SetText("-p '" + a.device.Name + "' ")
	}
	dialog.ShowForm("Run minipro command", "Run", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("minipro", e)}, func(ok bool) {
			if !ok {
				return
			}
			args, err := splitArgs(e.Text)
			if err != nil {
				dialog.ShowError(err, a.win)
				return
			}
			a.job("minipro "+e.Text, func(_ context.Context, run runFunc) error {
				res, err := run(args)
				if res != nil && res.Stdout != "" {
					a.logf("%s", strings.TrimRight(res.Stdout, "\n"))
				}
				return err
			})
		}, a.win)
}

// splitArgs splits a command line honouring single and double quotes.
func splitArgs(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	var quote rune
	inArg := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inArg = r, true
		case r == ' ' || r == '\t':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

func (a *App) showMiniproVersion() {
	if a.runner == nil {
		a.requireDevice()
		return
	}
	go func() {
		res, err := a.runner.Simple(context.Background(), "-V")
		if err != nil {
			a.showError(err)
			return
		}
		fyne.Do(func() { a.textDialog("minipro version", res.Output) })
	}()
}

func (a *App) showSupportedProgrammers() {
	a.textDialog("Supported programmers",
		"minipro (and therefore LinGecu) supports:\n\n"+
			"  • TL866A / TL866CS\n  • TL866II+\n  • T48\n  • T56 (experimental, needs algorithm.xml)\n  • T76 (experimental, needs algorithm.xml)\n")
}

func (a *App) algorithmsHelp() {
	a.textDialog("T56 / T76 algorithms",
		"The T56 and T76 load FPGA algorithms from the host. These belong to XGecu and\n"+
			"cannot be shipped with minipro. Generate algorithm.xml with the script in the\n"+
			"minipro source tree:\n\n"+
			"    minipro/dump-alg-minipro.bash <share dir>\n\n"+
			"The share dir is shown in Help → minipro version. Alternatively point\n"+
			"File → Settings → algorithm.xml at an existing file.")
}

func (a *App) showAbout() {
	mp := "system minipro"
	if a.bundle != nil {
		mp = "bundled minipro\n" + a.bundle.Upstream
	}
	a.textDialog("About LinGecu",
		"LinGecu "+Version+"\n\n"+
			"A graphical front end for minipro, the open source driver for\n"+
			"XGecu TL866A/CS, TL866II+, T48, T56 and T76 programmers.\n\n"+
			"Using "+mp+"\n\n"+
			"minipro: https://gitlab.com/DavidGriffith/minipro\n"+
			"License: GPL-3.0-or-later")
}

func (a *App) textDialog(title, text string) {
	t := widget.NewLabel(text)
	t.TextStyle = fyne.TextStyle{Monospace: true}
	dialog.ShowCustom(title, "Close", container.NewVScroll(t), a.win)
}

func (a *App) isLogic() bool {
	if a.device != nil && a.device.Type == devicedb.TypeLogic {
		a.tabs.Select(a.tabLogic)
		dialog.ShowInformation("Logic IC", "Logic ICs can only be tested. Use the Logic IC Test tab.", a.win)
		return true
	}
	return false
}

// ---- file dialogs ----

func (a *App) fileOpenDialog(title string, cb func(path string)) {
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			return
		}
		r.Close()
		cb(r.URI().Path())
	}, a.win)
	d.SetTitleText(title)
	a.setDialogDir(d)
	showFileDialog(d)
}

func (a *App) fileSaveDialog(title, name string, cb func(path string)) {
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil || w == nil {
			return
		}
		w.Close()
		a.prefs.SetString("lastDir", dirOf(w.URI().Path()))
		cb(w.URI().Path())
	}, a.win)
	d.SetTitleText(title)
	d.SetFileName(name)
	a.setDialogDir(d)
	showFileDialog(d)
}

// showFileDialog shows d at a usable size. Fyne 2.8 panics if a FileDialog
// is resized before Show, because its window does not exist yet.
func showFileDialog(d *dialog.FileDialog) {
	d.Show()
	d.Resize(fyne.NewSize(900, 600))
}

func (a *App) setDialogDir(d *dialog.FileDialog) {
	if dir := a.prefs.String("lastDir"); dir != "" {
		if l, err := storage.ListerForURI(storage.NewFileURI(dir)); err == nil {
			d.SetLocation(l)
		}
	}
}

func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return p
}

// openFile loads a file into the code buffer (or JEDEC view for PLDs).
func (a *App) openFile(path string, f buffer.Format) {
	a.openFileAt(path, f, 0, a.code)
}

func (a *App) openFileAt(path string, f buffer.Format, offset int, buf *buffer.Buffer) {
	a.prefs.SetString("lastDir", dirOf(path))
	if f == buffer.FormatAuto && buffer.FormatFromExt(path) == buffer.FormatJEDEC {
		f = buffer.FormatJEDEC
	}
	if f == buffer.FormatJEDEC || (a.device != nil && a.device.Type == devicedb.TypePLD) {
		txt, err := os.ReadFile(path)
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		a.jedText.SetText(string(txt))
		a.tabs.Select(a.tabCode)
		a.logf("Loaded JEDEC file %s", path)
		return
	}
	img, fmtUsed, err := buffer.ReadFile(path, f)
	if err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	data := img.Data
	if img.Base != 0 {
		a.logf("File data starts at 0x%X; loaded relative to that address", img.Base)
	}
	if offset == 0 {
		if buf.Len() > 0 && len(data) != buf.Len() {
			a.logf("Warning: file size %d differs from device size %d", len(data), buf.Len())
		}
		buf.Load(data, buf.Len() > 0)
	} else {
		buf.Write(offset, data)
	}
	buf.SetModified(false)
	if buf == a.code {
		a.filePath = path
		a.win.SetTitle("LinGecu — " + path)
	}
	a.logf("Loaded %s (%s, %d bytes) at 0x%X", path, fmtUsed, img.Used, offset)
}

func (a *App) openDialog() {
	fmtSel := widget.NewSelect([]string{"Auto", "Binary", "Intel HEX", "Motorola S-Record", "JEDEC (.jed)"}, nil)
	fmtSel.SetSelected("Auto")
	target := widget.NewRadioGroup([]string{"Code memory", "Data memory"}, nil)
	target.SetSelected("Code memory")
	target.Horizontal = true
	dialog.ShowForm("Open file", "Choose file…", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Format", fmtSel),
		widget.NewFormItem("Load into", target),
	}, func(ok bool) {
		if !ok {
			return
		}
		f := buffer.Format(fmtSel.SelectedIndex())
		buf := a.code
		if target.Selected == "Data memory" {
			buf = a.data
		}
		a.fileOpenDialog("Open file", func(p string) { fyne.Do(func() { a.openFileAt(p, f, 0, buf) }) })
	}, a.win)
}

func (a *App) openAtOffsetDialog() {
	off := widget.NewEntry()
	off.SetText("0x0")
	dialog.ShowForm("Open file at offset", "Choose file…", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Buffer offset", off),
	}, func(ok bool) {
		if !ok {
			return
		}
		o, err := parseNum(off.Text)
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		a.fileOpenDialog("Open file", func(p string) {
			fyne.Do(func() { a.openFileAt(p, buffer.FormatAuto, o, a.code) })
		})
	}, a.win)
}

func (a *App) saveDialog() {
	if a.device != nil && a.device.Type == devicedb.TypePLD {
		a.fileSaveDialog("Save JEDEC", a.device.BaseName()+".jed", func(p string) {
			if err := os.WriteFile(p, []byte(a.jedText.Text), 0o644); err != nil {
				a.showError(err)
			}
		})
		return
	}
	a.saveBufferDialog(false)
}

func (a *App) saveSelectionDialog() { a.saveBufferDialog(true) }

func (a *App) saveBufferDialog(selection bool) {
	fmtSel := widget.NewSelect([]string{"Binary", "Intel HEX", "Motorola S-Record"}, nil)
	fmtSel.SetSelected("Binary")
	src := widget.NewRadioGroup([]string{"Code memory", "Data memory"}, nil)
	src.SetSelected("Code memory")
	src.Horizontal = true
	dialog.ShowForm("Save buffer", "Choose file…", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Format", fmtSel),
		widget.NewFormItem("Buffer", src),
	}, func(ok bool) {
		if !ok {
			return
		}
		ed := a.codeEd
		if src.Selected == "Data memory" {
			ed = a.dataEd
		}
		data := ed.Buffer().Bytes()
		if selection {
			s, e, ok := ed.Selection()
			if !ok {
				dialog.ShowInformation("Save selection", "Nothing is selected.", a.win)
				return
			}
			data = data[s:e]
		}
		f := buffer.Format(fmtSel.SelectedIndex() + 1)
		ext := map[buffer.Format]string{buffer.FormatBinary: ".bin", buffer.FormatIntelHex: ".hex", buffer.FormatSRecord: ".s19"}[f]
		name := "buffer"
		if a.device != nil {
			name = strings.NewReplacer("@", "_", "/", "_", " ", "_").Replace(a.device.Name)
		}
		a.fileSaveDialog("Save buffer", name+ext, func(p string) {
			if err := buffer.WriteFile(p, data, f); err != nil {
				a.showError(err)
				return
			}
			if !selection {
				ed.Buffer().SetModified(false)
			}
			a.logf("Saved %d bytes to %s (%s)", len(data), p, f)
		})
	}, a.win)
}

// parseNum parses decimal, 0x hex or trailing-h hex numbers.
func parseNum(s string) (int, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "_", ""))
	if strings.HasSuffix(strings.ToLower(s), "h") {
		s = "0x" + s[:len(s)-1]
	}
	v, err := strconv.ParseInt(s, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", s)
	}
	return int(v), nil
}
