package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lingecu/internal/devicedb"
	"lingecu/internal/minipro"
)

// logicPanel runs minipro's logic IC (and SRAM) tests.
type logicPanel struct {
	a       *App
	content fyne.CanvasObject

	title         *widget.Label
	vectors       *widget.Label
	result        *widget.Label
	output        *widget.Label
	loop          *widget.Check
	passes, fails int
}

func newLogicPanel(a *App) *logicPanel {
	p := &logicPanel{a: a}
	p.title = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.vectors = widget.NewLabel("")
	p.vectors.TextStyle = fyne.TextStyle{Monospace: true}
	p.output = widget.NewLabel("")
	p.output.TextStyle = fyne.TextStyle{Monospace: true}
	p.result = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	p.loop = widget.NewCheck("Repeat until stopped", nil)

	legend := widget.NewLabel("Vector legend: 0/1 drive input · L/H expect output · X don't care · Z high-Z · C clock · G GND · V VCC")
	legend.Wrapping = fyne.TextWrapWord

	run := widget.NewButtonWithIcon("Test", theme.MediaPlayIcon(), p.run)
	run.Importance = widget.HighImportance
	ident := widget.NewButtonWithIcon("Identify unknown IC", theme.SearchIcon(), p.identify)
	pick := widget.NewButtonWithIcon("Select logic IC…", theme.ListIcon(), func() { a.showDevicePickerType(devicedb.TypeLogic) })
	save := widget.NewButtonWithIcon("Save report…", theme.DocumentSaveIcon(), p.saveReport)

	p.content = container.NewBorder(
		container.NewVBox(container.NewHBox(pick, run, ident, p.loop, save), p.title, legend, p.result),
		nil, nil, nil,
		container.NewHSplit(
			widget.NewCard("Test vectors", "", container.NewScroll(p.vectors)),
			widget.NewCard("Last test output", "", container.NewScroll(p.output)),
		),
	)
	return p
}

func (p *logicPanel) refreshForDevice() {
	d := p.a.device
	p.result.SetText("")
	if d == nil || (d.Type != devicedb.TypeLogic && d.Type != devicedb.TypeSRAM) {
		p.title.SetText("Select a 74/40xx logic IC or an SRAM to test it.")
		p.vectors.SetText("")
		return
	}
	if d.Type == devicedb.TypeSRAM {
		p.title.SetText(d.Name + " — SRAM test (writes and reads back test patterns)")
		p.vectors.SetText("")
		return
	}
	p.title.SetText(fmt.Sprintf("%s — %d pins, %s, %d test vectors", d.Name, d.LogicPins, d.LogicVoltage, len(d.Vectors)))
	var b strings.Builder
	b.WriteString("     ")
	for i := 1; i <= d.LogicPins; i++ {
		fmt.Fprintf(&b, "%-3d", i)
	}
	b.WriteByte('\n')
	for i, v := range d.Vectors {
		fmt.Fprintf(&b, "%03d  ", i)
		for _, f := range strings.Fields(v) {
			fmt.Fprintf(&b, "%-3s", f)
		}
		b.WriteByte('\n')
	}
	p.vectors.SetText(b.String())
}

func (p *logicPanel) run() {
	a := p.a
	if !a.requireDevice() {
		return
	}
	d := a.device
	if d.Type != devicedb.TypeLogic && d.Type != devicedb.TypeSRAM {
		dialog.ShowInformation("Logic test", "Select a logic IC or SRAM first.", a.win)
		return
	}
	o := a.opts.options()
	loop := p.loop.Checked
	p.passes, p.fails = 0, 0
	a.job("Logic test "+d.Name, func(ctx context.Context, run runFunc) error {
		for {
			res, err := run(o.Args(minipro.ActLogicTest, ""))
			if res != nil {
				out := res.Stdout
				if out == "" {
					out = res.Output
				}
				ok := err == nil
				fyne.Do(func() {
					p.output.SetText(out)
					if ok {
						p.passes++
					} else {
						p.fails++
					}
					status := "PASS"
					p.result.Importance = widget.SuccessImportance
					if !ok {
						status = "FAIL"
						p.result.Importance = widget.DangerImportance
					}
					p.result.SetText(fmt.Sprintf("%s   (passes %d, failures %d)", status, p.passes, p.fails))
				})
			}
			if !loop || ctx.Err() != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(300 * time.Millisecond):
			}
		}
	})
}

// identify tests every logic IC with the same pin count until one passes.
func (p *logicPanel) identify() {
	a := p.a
	if a.db == nil || a.runner == nil {
		a.requireDevice()
		return
	}
	pinSel := widget.NewSelect([]string{"14", "16", "20", "24"}, nil)
	pinSel.SetSelected("14")
	dialog.ShowForm("Identify logic IC", "Start", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Pin count", pinSel),
	}, func(ok bool) {
		if !ok {
			return
		}
		var pins int
		fmt.Sscan(pinSel.Selected, &pins)
		var cands []*devicedb.Device
		for _, d := range a.db.Logic {
			if d.LogicPins == pins {
				cands = append(cands, d)
			}
		}
		o := a.opts.options()
		a.job(fmt.Sprintf("Identify %d-pin logic IC (%d candidates)", pins, len(cands)), func(ctx context.Context, run runFunc) error {
			var found []string
			for i, d := range cands {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				fyne.Do(func() {
					a.progress.SetValue(float64(i) / float64(len(cands)))
					a.status.SetText("Trying " + d.Name)
				})
				o.Device = d.Name
				// Run quietly: a failing candidate is expected, not an error.
				res, err := a.runner.Run(ctx, a.withDatabases(o.Args(minipro.ActLogicTest, "")), nil, a.prompt)
				if err != nil {
					return err
				}
				if strings.Contains(res.Output, "Overcurrent") || strings.Contains(res.Output, "No programmer") {
					return errors.New(strings.TrimSpace(res.Output))
				}
				if res.OK() {
					found = append(found, d.Name)
					a.logf("  %s: PASS", d.Name)
				}
			}
			fyne.Do(func() {
				if len(found) == 0 {
					p.result.SetText("No matching logic IC found")
				} else {
					p.result.SetText("Matches: " + strings.Join(found, ", "))
				}
			})
			if len(found) == 0 {
				return errors.New("no match")
			}
			return nil
		})
	}, a.win)
}

func (p *logicPanel) saveReport() {
	if p.output.Text == "" {
		return
	}
	p.a.fileSaveDialog("Save logic test report", "logic_test.txt", func(path string) {
		txt := p.title.Text + "\n" + p.result.Text + "\n\n" + p.output.Text
		if err := os.WriteFile(path, []byte(txt), 0o644); err != nil {
			p.a.showError(err)
		}
	})
}
