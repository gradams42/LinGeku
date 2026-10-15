package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lingecu/internal/minipro"
)

// ---- serial numbers ----

type serialConfig struct {
	Address int
	Length  int
	Start   uint64
	Step    uint64
	Format  string
	Data    bool // write into the data (EEPROM) buffer instead of code
}

var serialFormats = []string{"Decimal ASCII", "Hex ASCII", "Binary little-endian", "Binary big-endian", "BCD (big-endian)"}

// encode renders value as Length bytes in the configured format.
func (c serialConfig) encode(v uint64) ([]byte, error) {
	n := c.Length
	if n <= 0 || n > 32 {
		return nil, errors.New("serial length must be 1–32 bytes")
	}
	out := make([]byte, n)
	switch c.Format {
	case "Decimal ASCII", "Hex ASCII":
		base := 10
		if c.Format == "Hex ASCII" {
			base = 16
		}
		s := strings.ToUpper(strconv.FormatUint(v, base))
		if len(s) > n {
			return nil, fmt.Errorf("serial %s does not fit in %d bytes", s, n)
		}
		copy(out, strings.Repeat("0", n-len(s))+s)
	case "Binary little-endian", "Binary big-endian":
		if n < 8 && v>>(8*uint(n)) != 0 {
			return nil, fmt.Errorf("serial %d does not fit in %d bytes", v, n)
		}
		for i := 0; i < n && i < 8; i++ {
			b := byte(v >> (8 * uint(i)))
			if c.Format == "Binary little-endian" {
				out[i] = b
			} else {
				out[n-1-i] = b
			}
		}
	case "BCD (big-endian)":
		s := strconv.FormatUint(v, 10)
		if len(s) > 2*n {
			return nil, fmt.Errorf("serial %s does not fit in %d BCD bytes", s, n)
		}
		s = strings.Repeat("0", 2*n-len(s)) + s
		for i := 0; i < n; i++ {
			out[i] = (s[2*i]-'0')<<4 | (s[2*i+1] - '0')
		}
	default:
		return nil, errors.New("unknown serial format")
	}
	return out, nil
}

type serialForm struct {
	a                         *App
	addr, length, start, step *widget.Entry
	format, target            *widget.Select
}

func newSerialForm(a *App) *serialForm {
	f := &serialForm{a: a}
	mk := func(key, def string) *widget.Entry {
		e := widget.NewEntry()
		e.SetText(a.prefs.StringWithFallback("serial."+key, def))
		return e
	}
	f.addr, f.length, f.start, f.step = mk("addr", "0x0"), mk("len", "4"), mk("next", "1"), mk("step", "1")
	f.format = widget.NewSelect(serialFormats, nil)
	f.format.SetSelected(a.prefs.StringWithFallback("serial.format", serialFormats[2]))
	f.target = widget.NewSelect([]string{"Code memory", "Data memory"}, nil)
	f.target.SetSelected(a.prefs.StringWithFallback("serial.target", "Code memory"))
	return f
}

func (f *serialForm) items() []*widget.FormItem {
	return []*widget.FormItem{
		widget.NewFormItem("Address", f.addr), widget.NewFormItem("Length (bytes)", f.length),
		widget.NewFormItem("Value / next serial", f.start), widget.NewFormItem("Step", f.step),
		widget.NewFormItem("Format", f.format), widget.NewFormItem("Buffer", f.target),
	}
}

func (f *serialForm) config() (serialConfig, error) {
	var c serialConfig
	var err error
	if c.Address, err = parseNum(f.addr.Text); err != nil {
		return c, err
	}
	if c.Length, err = parseNum(f.length.Text); err != nil {
		return c, err
	}
	if c.Start, err = strconv.ParseUint(strings.TrimSpace(f.start.Text), 0, 64); err != nil {
		return c, fmt.Errorf("invalid serial start %q", f.start.Text)
	}
	if c.Step, err = strconv.ParseUint(strings.TrimSpace(f.step.Text), 0, 64); err != nil {
		return c, fmt.Errorf("invalid serial step %q", f.step.Text)
	}
	c.Format, c.Data = f.format.Selected, f.target.Selected == "Data memory"
	p := f.a.prefs
	p.SetString("serial.addr", f.addr.Text)
	p.SetString("serial.len", f.length.Text)
	p.SetString("serial.next", f.start.Text)
	p.SetString("serial.step", f.step.Text)
	p.SetString("serial.format", f.format.Selected)
	p.SetString("serial.target", f.target.Selected)
	_, err = c.encode(c.Start)
	return c, err
}

// ---- auto program / production ----

func (a *App) showAutoProgram() {
	if !a.requireDevice() || a.isLogic() {
		return
	}
	d := a.device
	w := a.fapp.NewWindow("Auto Program — " + d.Name)

	pref := func(key string, def bool) bool { return a.prefs.BoolWithFallback("auto."+key, def) }
	check := func(label, key string, def bool) *widget.Check {
		c := widget.NewCheck(label, func(v bool) { a.prefs.SetBool("auto."+key, v) })
		c.SetChecked(pref(key, def))
		return c
	}
	stIDCheck := check("Check chip ID", "id", d.HasChipID())
	stErase := check("Erase", "erase", d.CanErase())
	stBlank := check("Blank check", "blank", true)
	stProgram := check("Program (selected memory areas)", "program", true)
	stVerify := check("Verify", "verify", true)
	stProtect := check("Enable protection / lock after programming", "protect", false)
	if !d.CanErase() {
		stErase.SetChecked(false)
		stErase.Disable()
	}
	if !d.HasChipID() {
		stIDCheck.SetChecked(false)
		stIDCheck.Disable()
	}
	if !d.ProtectAfter() {
		stProtect.SetChecked(false)
		stProtect.Disable()
	}

	serialOn := check("Write serial number into each chip", "serial", false)
	sf := newSerialForm(a)

	target := widget.NewEntry()
	target.SetText(a.prefs.StringWithFallback("auto.target", "1"))
	autoStart := check("Start automatically when a chip is inserted (polls the chip ID)", "autostart", false)
	if !d.HasChipID() {
		autoStart.SetChecked(false)
		autoStart.Disable()
	}
	beep := check("Bell on completion", "beep", true)

	okLbl := widget.NewLabelWithStyle("Pass: 0", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	okLbl.Importance = widget.SuccessImportance
	failLbl := widget.NewLabelWithStyle("Fail: 0", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	failLbl.Importance = widget.DangerImportance
	stateLbl := widget.NewLabel("Idle")
	stateLbl.Wrapping = fyne.TextWrapWord
	passes, fails := 0, 0

	var startBtn *widget.Button
	startBtn = widget.NewButton("Start", func() {
		n, err := strconv.Atoi(strings.TrimSpace(target.Text))
		if err != nil || n < 0 {
			dialog.ShowError(errors.New("chip count must be a number (0 = unlimited)"), w)
			return
		}
		a.prefs.SetString("auto.target", target.Text)
		var serial *serialConfig
		if serialOn.Checked {
			c, err := sf.config()
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			serial = &c
		}
		cfgText, cfgErr := a.cfg.configText()
		plan := autoPlan{
			idCheck: stIDCheck.Checked, erase: stErase.Checked, blank: stBlank.Checked,
			program: stProgram.Checked, verify: stVerify.Checked, protect: stProtect.Checked,
			count: n, autoStart: autoStart.Checked, serial: serial,
			codeImg: a.code.Bytes(), dataImg: a.data.Bytes(), jed: a.jedText.Text,
			cfgText: cfgText, cfgErr: cfgErr,
		}
		status := func(s string) { fyne.Do(func() { stateLbl.SetText(s) }) }
		result := func(ok bool) {
			fyne.Do(func() {
				if ok {
					passes++
				} else {
					fails++
				}
				okLbl.SetText(fmt.Sprintf("Pass: %d", passes))
				failLbl.SetText(fmt.Sprintf("Fail: %d", fails))
			})
		}
		a.job("Auto program "+d.Name, func(ctx context.Context, run runFunc) error {
			err := a.autoProgram(ctx, run, w, plan, status, result)
			if beep.Checked {
				fmt.Print("\a")
			}
			return err
		})
	})
	startBtn.Importance = widget.HighImportance
	stopBtn := widget.NewButton("Stop", a.stop)
	reset := widget.NewButton("Reset counters", func() {
		passes, fails = 0, 0
		okLbl.SetText("Pass: 0")
		failLbl.SetText("Fail: 0")
	})

	steps := widget.NewCard("Operations (in order)", "", container.NewVBox(stIDCheck, stErase, stBlank, stProgram, stVerify, stProtect))
	serialCard := widget.NewCard("Serial number", "", container.NewVBox(serialOn, widget.NewForm(sf.items()...)))
	prod := widget.NewCard("Production", "", container.NewVBox(
		widget.NewForm(widget.NewFormItem("Number of chips (0 = unlimited)", target)), autoStart, beep))
	counters := widget.NewCard("Status", "", container.NewVBox(container.NewHBox(okLbl, failLbl, reset), stateLbl))

	w.SetContent(container.NewBorder(nil, container.NewHBox(startBtn, stopBtn), nil, nil,
		container.NewVScroll(container.NewVBox(
			container.NewGridWithColumns(2, steps, prod), serialCard, counters))))
	w.Resize(fyne.NewSize(760, 720))
	w.Show()
}

type autoPlan struct {
	idCheck, erase, blank, program, verify, protect bool
	count                                           int
	autoStart                                       bool
	serial                                          *serialConfig
	codeImg, dataImg                                []byte
	jed, cfgText                                    string
	cfgErr                                          error
}

// autoProgram runs the configured steps for each chip.
func (a *App) autoProgram(ctx context.Context, run runFunc, w fyne.Window, p autoPlan, status func(string), result func(bool)) error {
	o := a.opts.options()
	areas := a.selectedAreas()
	failures := 0
	for n := 0; p.count == 0 || n < p.count; n++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Wait for the next chip.
		if p.autoStart {
			if n > 0 {
				status("Remove the programmed chip…")
				if err := a.waitChip(ctx, o, false); err != nil {
					return err
				}
			}
			status(fmt.Sprintf("Insert chip #%d…", n+1))
			if err := a.waitChip(ctx, o, true); err != nil {
				return err
			}
		} else if n > 0 {
			ch := make(chan bool, 1)
			fyne.Do(func() {
				dialog.ShowConfirm("Next chip", fmt.Sprintf("Insert chip #%d and press Yes to program it.", n+1),
					func(ok bool) { ch <- ok }, w)
			})
			select {
			case ok := <-ch:
				if !ok {
					return nil
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		status(fmt.Sprintf("Chip #%d: working…", n+1))
		a.logf("── Chip #%d ──", n+1)
		code, data := append([]byte(nil), p.codeImg...), append([]byte(nil), p.dataImg...)
		var serialVal uint64
		if p.serial != nil {
			sv, _ := strconv.ParseUint(a.prefs.StringWithFallback("serial.next", "1"), 0, 64)
			serialVal = sv
			b, err := p.serial.encode(sv)
			if err != nil {
				return err
			}
			buf := code
			if p.serial.Data {
				buf = data
			}
			if p.serial.Address+len(b) > len(buf) {
				return errors.New("serial number address is outside the buffer")
			}
			copy(buf[p.serial.Address:], b)
			a.logf("Serial number %d (% X) at 0x%X", sv, b, p.serial.Address)
		}
		err := a.programChip(run, o, areas, p, code, data)
		if err == nil && p.serial != nil {
			a.prefs.SetString("serial.next", strconv.FormatUint(serialVal+p.serial.Step, 10))
		}
		result(err == nil)
		if err != nil {
			failures++
			a.logf("✗ Chip #%d FAILED: %v", n+1, err)
			status(fmt.Sprintf("Chip #%d FAILED: %v", n+1, err))
			if errors.Is(err, context.Canceled) {
				return err
			}
		} else {
			a.logf("✓ Chip #%d OK", n+1)
			status(fmt.Sprintf("Chip #%d OK", n+1))
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d chip(s) failed", failures)
	}
	return nil
}

// programChip runs the selected steps on one chip.
func (a *App) programChip(run runFunc, o minipro.Options, areas []area, p autoPlan, code, data []byte) error {
	if p.idCheck {
		io := o
		io.NoIDError = false
		if _, err := run(io.Args(minipro.ActReadID, "")); err != nil {
			return fmt.Errorf("chip ID: %w", err)
		}
	}
	if p.erase {
		if _, err := run(o.Args(minipro.ActErase, "")); err != nil {
			return fmt.Errorf("erase: %w", err)
		}
	}
	if p.blank {
		bo := o
		bo.Page = ""
		if _, err := run(bo.Args(minipro.ActBlankCheck, "")); err != nil {
			return fmt.Errorf("blank check: %w", err)
		}
	}
	if p.program {
		po := o
		po.SkipErase = p.erase || o.SkipErase // already erased above
		po.SkipVerify = !p.verify             // minipro verifies before protecting
		po.Protect = p.protect
		return a.writeAreas(run, po, areas, code, data, p.jed, p.cfgText, p.cfgErr)
	}
	if p.verify {
		return a.verifyAreas(run, o, areas, code, data, p.jed, p.cfgText, p.cfgErr)
	}
	return nil
}

// waitChip polls the chip ID until a chip is present (or absent).
func (a *App) waitChip(ctx context.Context, o minipro.Options, present bool) error {
	o.NoIDError = true
	want := a.device.ChipID
	for {
		res, err := a.runner.Run(ctx, a.withDatabases(o.Args(minipro.ActReadID, "")), nil, nil)
		if err != nil {
			return err
		}
		id, ok := minipro.ParseChipID(res.Output)
		has := ok && id != 0 && id != 0xffff && id != 0xffffff && id != 0xffffffff && (want == 0 || id == want)
		if has == present {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
	}
}
