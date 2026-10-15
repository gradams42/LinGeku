package ui

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"lingecu/internal/devicedb"
	"lingecu/internal/minipro"
	"lingecu/internal/project"
)

func layoutSpacerObj() fyne.CanvasObject { return layout.NewSpacer() }

func t0() context.Context { return context.Background() }

func (a *App) addShortcuts() {
	c := a.win.Canvas()
	add := func(key fyne.KeyName, mod fyne.KeyModifier, f func()) {
		c.AddShortcut(&desktop.CustomShortcut{KeyName: key, Modifier: mod}, func(fyne.Shortcut) { f() })
	}
	ctrl := fyne.KeyModifierShortcutDefault
	add(fyne.KeyO, ctrl, a.openDialog)
	add(fyne.KeyS, ctrl, a.saveDialog)
	add(fyne.KeyD, ctrl, a.showDevicePicker)
	add(fyne.KeyR, ctrl, a.doRead)
	add(fyne.KeyP, ctrl, a.doProgram)
	add(fyne.KeyE, ctrl, a.doVerify)
	add(fyne.KeyB, ctrl, a.doBlankCheck)
	add(fyne.KeyG, ctrl, a.gotoDialog)
	add(fyne.KeyF, ctrl, a.findDialog)
	add(fyne.KeyI, ctrl, a.doReadID)
	add(fyne.KeyA, ctrl|fyne.KeyModifierShift, a.showAutoProgram)
	add(fyne.KeyEscape, 0, a.stop)
}

// ---- settings ----

func (a *App) showSettings() {
	entry := func(key, placeholder string) *widget.Entry {
		e := widget.NewEntry()
		e.SetText(a.prefs.String(key))
		e.SetPlaceHolder(placeholder)
		return e
	}
	bin := entry("miniproPath", "auto-detect ($PATH)")
	infoic := entry("infoicPath", "auto-detect (minipro share dir)")
	logicic := entry("logicicPath", "auto-detect")
	algo := entry("algoPath", "minipro default (T56/T76 only)")
	browse := func(e *widget.Entry) fyne.CanvasObject {
		return container.NewBorder(nil, nil, nil, widget.NewButton("…", func() {
			a.fileOpenDialog("Choose file", func(p string) { fyne.Do(func() { e.SetText(p) }) })
		}), e)
	}
	theme := widget.NewSelect([]string{"System", "Light", "Dark"}, nil)
	theme.SetSelected(a.prefs.StringWithFallback("theme", "System"))

	d := dialog.NewForm("Settings", "Save", "Cancel", []*widget.FormItem{
		widget.NewFormItem("minipro executable", browse(bin)),
		widget.NewFormItem("infoic.xml", browse(infoic)),
		widget.NewFormItem("logicic.xml", browse(logicic)),
		widget.NewFormItem("algorithm.xml", browse(algo)),
		widget.NewFormItem("Theme", theme),
	}, func(ok bool) {
		if !ok {
			return
		}
		reloadDB := a.prefs.String("infoicPath") != infoic.Text || a.prefs.String("logicicPath") != logicic.Text
		a.prefs.SetString("miniproPath", strings.TrimSpace(bin.Text))
		a.prefs.SetString("infoicPath", strings.TrimSpace(infoic.Text))
		a.prefs.SetString("logicicPath", strings.TrimSpace(logicic.Text))
		a.prefs.SetString("algoPath", strings.TrimSpace(algo.Text))
		a.prefs.SetString("theme", theme.Selected)
		applyTheme(a.fapp, theme.Selected)
		if path, err := minipro.Locate(a.prefs.String("miniproPath")); err == nil {
			a.runner = &minipro.Runner{Binary: path}
			a.logf("Using minipro at %s", path)
		} else {
			a.logf("ERROR: %v", err)
		}
		if reloadDB {
			go a.loadDatabase()
		}
	}, a.win)
	d.Resize(fyne.NewSize(700, 380))
	d.Show()
}

// ---- projects ----

func (a *App) saveProjectDialog() {
	if a.device == nil {
		dialog.ShowInformation("Save project", "Select a device first.", a.win)
		return
	}
	name := strings.NewReplacer("@", "_", "/", "_", " ", "_").Replace(a.device.Name) + ".lgp"
	a.fileSaveDialog("Save project", name, func(path string) {
		p := &project.Project{
			Programmer: string(a.programmer),
			Device:     a.device.Name,
			Options:    a.optionState(),
			JEDEC:      a.jedText.Text,
			ConfigKeys: a.cfg.keys,
			Config:     a.cfg.vals,
			SourceFile: a.filePath,
		}
		if !a.largeMode {
			p.Code = a.code.Bytes()
		}
		p.Data = a.data.Bytes()
		if err := project.Save(path, p); err != nil {
			a.showError(err)
			return
		}
		a.logf("Project saved to %s", path)
	})
}

func (a *App) loadProjectDialog() {
	a.fileOpenDialog("Load project", func(path string) {
		p, err := project.Load(path)
		if err != nil {
			a.showError(err)
			return
		}
		fyne.Do(func() { a.applyProject(p, path) })
	})
}

func (a *App) applyProject(p *project.Project, path string) {
	if a.db == nil {
		dialog.ShowInformation("Load project", "The device database is not loaded yet.", a.win)
		return
	}
	prog := devicedb.Programmer(p.Programmer)
	if prog != a.programmer {
		a.programmer = prog
		a.progSelect.SetSelected(prog.Label())
	}
	d := a.db.Find(prog, p.Device)
	if d == nil {
		dialog.ShowError(errorf("device %s from the project is not in the database", p.Device), a.win)
		return
	}
	a.selectDevice(d)
	a.setOptionState(p.Options)
	if len(p.Code) > 0 {
		a.code.Load(p.Code, true)
	}
	if len(p.Data) > 0 {
		a.data.Load(p.Data, true)
	}
	a.jedText.SetText(p.JEDEC)
	if len(p.ConfigKeys) > 0 {
		a.cfg.setValues(p.ConfigKeys, p.Config)
	}
	a.filePath = p.SourceFile
	a.win.SetTitle("LinGecu — " + path)
	a.logf("Project loaded from %s", path)
}

// optionState captures the options panel as strings.
func (a *App) optionState() map[string]string {
	o := a.opts
	m := map[string]string{}
	for k, c := range o.checks() {
		if c.Checked {
			m[k] = "1"
		} else {
			m[k] = "0"
		}
	}
	for k, s := range o.selects() {
		m[k] = s.Selected
	}
	m["pulse"], m["i2c"] = o.pulse.Text, o.i2cAddr.Text
	return m
}

func (a *App) setOptionState(m map[string]string) {
	o := a.opts
	for k, c := range o.checks() {
		if v, ok := m[k]; ok {
			c.SetChecked(v == "1")
		}
	}
	for k, s := range o.selects() {
		if v, ok := m[k]; ok && v != "" {
			s.SetSelected(v)
		}
	}
	o.pulse.SetText(m["pulse"])
	o.i2cAddr.SetText(m["i2c"])
}

func (p *optionsPanel) checks() map[string]*widget.Check {
	return map[string]*widget.Check{
		"areaCode": p.areaCode, "areaData": p.areaData, "areaConfig": p.areaConfig, "areaCalib": p.areaCalib,
		"areaUser": p.areaUser, "erase": p.erase, "verify": p.verify, "blankFirst": p.blankFirst,
		"checkID": p.checkID, "ignoreID": p.ignoreID, "pinCheck": p.pinCheck, "unprotect": p.unprotect,
		"protect": p.protect, "skipBlank": p.skipBlank,
	}
}

func (p *optionsPanel) selects() map[string]*widget.Select {
	return map[string]*widget.Select{
		"sizeMode": p.sizeMode, "icsp": p.icsp, "vpp": p.vpp, "vdd": p.vdd, "vcc": p.vcc,
		"spiClock": p.spiClock, "ovc": p.ovc, "partition": p.partition,
	}
}

// withDatabases adds --infoic/--logicic so minipro always reads the same
// database files the GUI shows (essential for the bundled minipro, whose
// compiled-in share directory does not exist).
func (a *App) withDatabases(args []string) []string {
	a.mu.Lock()
	infoic, logic := a.infoicPath, a.logicicPath
	a.mu.Unlock()
	has := func(flag string) bool {
		for _, s := range args {
			if s == flag {
				return true
			}
		}
		return false
	}
	if len(args) > 0 && (args[0] == "-V" || args[0] == "-k" || args[0] == "-t" || args[0] == "-F") {
		return args // these never read the database
	}
	out := append([]string(nil), args...)
	if infoic != "" && !has("--infoic") {
		out = append(out, "--infoic", infoic)
	}
	if logic != "" && !has("--logicic") {
		out = append(out, "--logicic", logic)
	}
	return out
}

// installUdevRules copies minipro's udev rules into /etc/udev/rules.d via
// pkexec so the programmer can be used without root.
func (a *App) installUdevRules() {
	const dest = "/etc/udev/rules.d/60-minipro.rules"
	src := ""
	if a.bundle != nil {
		src = a.bundle.UdevRules
	}
	if src == "" {
		if _, err := os.Stat(dest); err == nil {
			dialog.ShowInformation("udev rules", "The minipro udev rules are already installed.", a.win)
			return
		}
		dialog.ShowInformation("udev rules", "This build does not bundle minipro. Install the rules with\n"+
			"'sudo make install' in the minipro source tree.", a.win)
		return
	}
	dialog.ShowConfirm("Install udev rules",
		"Install "+dest+" so XGecu programmers can be used without root?\nYou will be asked for your password.",
		func(ok bool) {
			if !ok {
				return
			}
			go func() {
				script := "install -m 0644 \"$1\" " + dest + " && udevadm control --reload-rules && udevadm trigger"
				out, err := exec.Command("pkexec", "sh", "-c", script, "sh", src).CombinedOutput()
				if len(out) > 0 {
					a.logf("%s", strings.TrimSpace(string(out)))
				}
				if err != nil {
					a.showError(errorf("installing udev rules failed: %v", err))
					return
				}
				a.logf("✓ udev rules installed; reconnect the programmer")
			}()
		}, a.win)
}
