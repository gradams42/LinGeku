package ui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lingecu/internal/devicedb"
	"lingecu/internal/minipro"
)

// configPanel edits fuses, lock bits and user IDs (minipro's -c config page).
type configPanel struct {
	a       *App
	content fyne.CanvasObject

	keys     []string
	vals     map[string]string
	meta     map[string]devicedb.Setting
	rows     *fyne.Container
	raw      *widget.Entry
	extra    *widget.Label
	note     *widget.Label
	updating bool
}

func newConfigPanel(a *App) *configPanel {
	p := &configPanel{a: a, vals: map[string]string{}, meta: map[string]devicedb.Setting{}}
	p.rows = container.NewVBox()
	p.raw = widget.NewMultiLineEntry()
	p.raw.TextStyle = fyne.TextStyle{Monospace: true}
	p.extra = widget.NewLabel("")
	p.extra.TextStyle = fyne.TextStyle{Monospace: true}
	p.note = widget.NewLabel("")
	p.note.Wrapping = fyne.TextWrapWord

	applyRaw := widget.NewButton("Apply text", func() {
		keys, vals := minipro.ParseConfig(p.raw.Text)
		p.setValues(keys, vals)
	})
	tabs := container.NewAppTabs(
		container.NewTabItem("Bits", container.NewVScroll(p.rows)),
		container.NewTabItem("Raw text", container.NewBorder(nil, container.NewHBox(applyRaw), nil, nil, p.raw)),
	)
	tabs.OnSelected = func(*container.TabItem) { p.syncRaw() }

	btn := func(label string, icon fyne.Resource, f func()) *widget.Button {
		return widget.NewButtonWithIcon(label, icon, f)
	}
	bar := container.NewHBox(
		btn("Read config", theme.DownloadIcon(), p.readFromChip),
		btn("Write config", theme.UploadIcon(), p.writeToChip),
		btn("Verify config", theme.ConfirmIcon(), p.verifyChip),
		btn("Defaults", theme.ContentUndoIcon(), p.loadDefaults),
		btn("Load…", theme.FolderOpenIcon(), p.loadFile),
		btn("Save…", theme.DocumentSaveIcon(), p.saveFile),
	)
	p.content = container.NewBorder(container.NewVBox(bar, p.note), p.extra, nil, nil, tabs)
	return p
}

func (p *configPanel) refreshForDevice() {
	d := p.a.device
	p.keys, p.vals, p.meta = nil, map[string]string{}, map[string]devicedb.Setting{}
	p.extra.SetText("")
	switch {
	case d == nil:
		p.note.SetText("Select a device.")
	case d.Type == devicedb.TypePLD:
		p.note.SetText("For PLD/GAL devices the fuse map is the JEDEC file in the Code Memory tab.")
	case !d.HasConfig():
		p.note.SetText(d.Name + " has no configuration bits.")
	default:
		p.note.SetText("Values are hex. For AVR fuses a bit value of 0 means \"programmed\". " +
			"Read the chip first to see every config word minipro expects; all must be present when writing.")
		p.loadDefaults()
		return
	}
	p.rebuild()
}

// loadDefaults fills values from the device database defaults.
func (p *configPanel) loadDefaults() {
	d := p.a.device
	if d == nil || p.a.db == nil {
		return
	}
	cfg := p.a.db.Config(d)
	if cfg == nil {
		p.rebuild()
		return
	}
	var keys []string
	vals := map[string]string{}
	add := func(s devicedb.Setting) {
		p.meta[s.Name] = s
		keys = append(keys, s.Name)
		vals[s.Name] = fmtVal(s.Default, s.Mask)
	}
	for _, f := range cfg.Fuses {
		add(f)
	}
	for i := 0; i < cfg.NumUIDs; i++ {
		k := fmt.Sprintf("user_id%d", i)
		keys = append(keys, k)
		vals[k] = "0xff"
	}
	for _, l := range cfg.Locks {
		add(l)
	}
	p.setValues(keys, vals)
}

func fmtVal(v, mask uint16) string {
	if mask > 0xff {
		return fmt.Sprintf("0x%04x", v)
	}
	return fmt.Sprintf("0x%02x", v)
}

func (p *configPanel) setValues(keys []string, vals map[string]string) {
	p.keys, p.vals = keys, vals
	p.rebuild()
}

// loadFromChip shows data read from the chip's config/calibration/user page.
func (p *configPanel) loadFromChip(page string, raw []byte) {
	switch page {
	case "config":
		keys, vals := minipro.ParseConfig(string(raw))
		p.setValues(keys, vals)
		p.a.tabs.Select(p.a.tabConfig)
	case "calibration":
		p.extra.SetText("Calibration: " + strings.TrimSpace(string(raw)))
	case "user":
		var b strings.Builder
		for i, v := range raw {
			if i > 0 && i%16 == 0 {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "%02X ", v)
		}
		p.extra.SetText("User row:\n" + b.String())
	}
}

func (p *configPanel) syncRaw() {
	p.raw.SetText(minipro.FormatConfig(p.keys, p.vals))
}

// rebuild recreates one row per config word with a hex entry and bit boxes.
func (p *configPanel) rebuild() {
	p.rows.Objects = nil
	for _, k := range p.keys {
		k := k
		meta, hasMeta := p.meta[k]
		width := 8
		mask := uint16(0xff)
		if hasMeta {
			mask = meta.Mask
			if mask > 0xff {
				width = 16
			}
		} else if v, err := strconv.ParseUint(p.vals[k], 0, 32); err == nil && v > 0xff {
			width, mask = 16, 0xffff
		}
		entry := widget.NewEntry()
		entry.SetText(p.vals[k])
		var checks []*widget.Check
		setChecks := func(v uint64) {
			p.updating = true
			for i, c := range checks {
				bit := width - 1 - i
				c.SetChecked(v&(1<<bit) != 0)
			}
			p.updating = false
		}
		grid := container.NewGridWithColumns(8)
		for i := 0; i < width; i++ {
			bit := width - 1 - i
			c := widget.NewCheck(fmt.Sprintf("%d", bit), nil)
			if mask&(1<<bit) == 0 {
				c.Disable()
			}
			c.OnChanged = func(on bool) {
				if p.updating {
					return
				}
				v, _ := strconv.ParseUint(p.vals[k], 0, 32)
				if on {
					v |= 1 << bit
				} else {
					v &^= 1 << bit
				}
				p.vals[k] = fmtVal(uint16(v), mask)
				p.updating = true
				entry.SetText(p.vals[k])
				p.updating = false
			}
			checks = append(checks, c)
			grid.Add(c)
		}
		entry.OnChanged = func(s string) {
			if p.updating {
				return
			}
			p.vals[k] = strings.TrimSpace(s)
			if v, err := strconv.ParseUint(p.vals[k], 0, 32); err == nil {
				setChecks(v)
			}
		}
		if v, err := strconv.ParseUint(p.vals[k], 0, 32); err == nil {
			setChecks(v)
		}
		title := k
		if hasMeta {
			title = fmt.Sprintf("%s  (mask 0x%X, default 0x%X)", k, meta.Mask, meta.Default)
		}
		card := widget.NewCard("", title, container.NewBorder(nil, nil,
			container.NewGridWrap(fyne.NewSize(110, entry.MinSize().Height), entry), nil, grid))
		p.rows.Add(card)
	}
	if len(p.keys) == 0 {
		p.rows.Add(widget.NewLabel("No configuration values loaded."))
	}
	p.rows.Refresh()
	p.syncRaw()
}

// configText returns the config in minipro's file format, validating values.
func (p *configPanel) configText() (string, error) {
	for _, k := range p.keys {
		if _, err := strconv.ParseUint(p.vals[k], 0, 32); err != nil {
			return "", fmt.Errorf("config value %s=%q is not a number", k, p.vals[k])
		}
	}
	if len(p.keys) == 0 {
		return "", fmt.Errorf("no config values: read the chip config or load defaults first")
	}
	return minipro.FormatConfig(p.keys, p.vals), nil
}

func (p *configPanel) readFromChip() {
	a := p.a
	if !a.requireDevice() || !a.device.HasConfig() {
		return
	}
	d, o := a.device, a.opts.options()
	a.job("Read config "+d.Name, func(_ context.Context, run runFunc) error {
		return a.readArea(run, o, d, area{"config", "Config"})
	})
}

func (p *configPanel) writeToChip() {
	a := p.a
	if !a.requireDevice() || !a.device.HasConfig() {
		return
	}
	if _, err := p.configText(); err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	dialog.ShowConfirm("Write config", "Write these configuration bits to "+a.device.Name+
		"?\nWrong fuse settings can lock you out of the chip.", func(ok bool) {
		if ok {
			a.job("Write config "+a.device.Name, a.programSteps(a.device, a.opts.options(), []area{{"config", "Config"}}))
		}
	}, a.win)
}

func (p *configPanel) verifyChip() {
	a := p.a
	if !a.requireDevice() || !a.device.HasConfig() {
		return
	}
	txt, err := p.configText()
	if err != nil {
		dialog.ShowError(err, a.win)
		return
	}
	o := a.opts.options()
	o.Page = "config"
	f := a.tmp("verify.conf")
	a.job("Verify config", func(_ context.Context, run runFunc) error {
		if err := os.WriteFile(f, []byte(txt), 0o644); err != nil {
			return err
		}
		_, err := run(o.Args(minipro.ActVerify, f))
		return err
	})
}

func (p *configPanel) loadFile() {
	p.a.fileOpenDialog("Load config file", func(path string) {
		raw, err := os.ReadFile(path)
		if err != nil {
			p.a.showError(err)
			return
		}
		fyne.Do(func() {
			keys, vals := minipro.ParseConfig(string(raw))
			p.setValues(keys, vals)
		})
	})
}

func (p *configPanel) saveFile() {
	txt, err := p.configText()
	if err != nil {
		dialog.ShowError(err, p.a.win)
		return
	}
	name := "config"
	if p.a.device != nil {
		name = p.a.device.BaseName()
	}
	p.a.fileSaveDialog("Save config file", name+".fuses.conf", func(path string) {
		if err := os.WriteFile(path, []byte(txt), 0o644); err != nil {
			p.a.showError(err)
		}
	})
}
