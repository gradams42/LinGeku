package ui

import (
	"context"
	"fmt"
	"image/color"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lingecu/internal/devicedb"
	"lingecu/internal/minipro"
)

// pinState is what the ZIF socket view shows for one socket pin.
type pinState int

const (
	pinEmpty    pinState = iota // no chip pin in this socket position
	pinUnknown                  // tested pin, no result yet
	pinGood                     // good contact
	pinBad                      // bad contact
	pinGround                   // chip GND (drives the test)
	pinUntested                 // chip pin the test does not check (e.g. VCC)
)

func (s pinState) color() color.Color {
	switch s {
	case pinGood:
		return theme.Color(theme.ColorNameSuccess)
	case pinBad:
		return theme.Color(theme.ColorNameError)
	case pinGround:
		return theme.Color(theme.ColorNamePrimary)
	case pinUntested:
		return theme.Color(theme.ColorNameDisabled)
	case pinUnknown:
		return theme.Color(theme.ColorNameWarning)
	}
	return color.Transparent
}

// zifSocket draws the programmer's 40-pin ZIF socket with the chip in it,
// colouring each pin by its contact test result, like XGecu's pin detect view.
type zifSocket struct {
	widget.BaseWidget
	pins     [40]pinState
	chipPins int // DIP pin count of the inserted chip (0 = none)
}

func newZIFSocket() *zifSocket {
	z := &zifSocket{}
	z.ExtendBaseWidget(z)
	return z
}

func (z *zifSocket) CreateRenderer() fyne.WidgetRenderer {
	r := &zifRenderer{z: z}
	r.body = canvas.NewRectangle(color.Transparent)
	r.body.CornerRadius = 8
	r.body.StrokeWidth = 2
	r.lever = canvas.NewRectangle(color.Transparent)
	r.lever.CornerRadius = 4
	r.chip = canvas.NewRectangle(color.Transparent)
	r.chip.CornerRadius = 3
	r.notch = canvas.NewCircle(color.Transparent)
	r.title = canvas.NewText("40-pin ZIF socket", color.Black)
	r.title.TextStyle = fyne.TextStyle{Bold: true}
	r.title.Alignment = fyne.TextAlignCenter
	for i := 0; i < 40; i++ {
		r.holes[i] = canvas.NewCircle(color.Transparent)
		r.holes[i].StrokeWidth = 1.5
		r.zifNum[i] = canvas.NewText(strconv.Itoa(i+1), color.Black)
		r.devNum[i] = canvas.NewText("", color.Black)
		r.devNum[i].TextStyle = fyne.TextStyle{Bold: true}
	}
	return r
}

type zifRenderer struct {
	z                 *zifSocket
	body, lever, chip *canvas.Rectangle
	notch             *canvas.Circle
	title             *canvas.Text
	holes             [40]*canvas.Circle
	zifNum, devNum    [40]*canvas.Text
}

const zifRows = 20

func (r *zifRenderer) MinSize() fyne.Size { return fyne.NewSize(300, 520) }

func (r *zifRenderer) Layout(s fyne.Size) {
	top := float32(56) // title + lever
	row := min((s.Height-top-16)/zifRows, 34)
	w := min(s.Width, row*9)
	x0 := (s.Width - w) / 2
	pinD := row * 0.62
	leftX := x0 + w*0.22
	rightX := x0 + w*0.78

	r.title.Move(fyne.NewPos(0, 0))
	r.title.Resize(fyne.NewSize(s.Width, 22))
	r.lever.Move(fyne.NewPos(x0+w*0.06, 26))
	r.lever.Resize(fyne.NewSize(w*0.12, top-26+row*1.2))
	r.body.Move(fyne.NewPos(x0, top-8))
	r.body.Resize(fyne.NewSize(w, row*zifRows+16))

	pos := func(zif int) (float32, float32) { // zif is 1-based
		if zif <= zifRows {
			return leftX, top + float32(zif-1)*row + row/2
		}
		return rightX, top + float32(40-zif)*row + row/2
	}
	for i := 0; i < 40; i++ {
		cx, cy := pos(i + 1)
		r.holes[i].Move(fyne.NewPos(cx-pinD/2, cy-pinD/2))
		r.holes[i].Resize(fyne.NewSize(pinD, pinD))
		n := r.zifNum[i]
		n.TextSize = theme.TextSize() * 0.75
		ns := n.MinSize()
		if i < zifRows {
			n.Move(fyne.NewPos(cx-pinD/2-ns.Width-6, cy-ns.Height/2))
		} else {
			n.Move(fyne.NewPos(cx+pinD/2+6, cy-ns.Height/2))
		}
		d := r.devNum[i]
		ds := d.MinSize()
		if i < zifRows {
			d.Move(fyne.NewPos(cx+pinD/2+6, cy-ds.Height/2))
		} else {
			d.Move(fyne.NewPos(cx-pinD/2-ds.Width-6, cy-ds.Height/2))
		}
	}
	if n := r.z.chipPins; n > 0 {
		rows := float32(n / 2)
		cx := (leftX + rightX) / 2
		cw := (rightX - leftX) - pinD - 4
		r.chip.Move(fyne.NewPos(cx-cw/2, top+2))
		r.chip.Resize(fyne.NewSize(cw, rows*row-4))
		nd := min(cw*0.3, row*0.7)
		r.notch.Move(fyne.NewPos(cx-nd/2, top+2-nd/2))
		r.notch.Resize(fyne.NewSize(nd, nd))
	}
}

func (r *zifRenderer) Refresh() {
	fg := theme.Color(theme.ColorNameForeground)
	bg := theme.Color(theme.ColorNameInputBackground)
	r.title.Color = fg
	r.body.FillColor = bg
	r.body.StrokeColor = theme.Color(theme.ColorNameInputBorder)
	r.lever.FillColor = theme.Color(theme.ColorNameDisabled)
	r.chip.FillColor = color.NRGBA{R: 0x30, G: 0x30, B: 0x34, A: 0xd0}
	r.notch.FillColor = bg
	visible := r.z.chipPins > 0
	r.chip.Hidden, r.notch.Hidden = !visible, !visible
	for i := 0; i < 40; i++ {
		st := r.z.pins[i]
		h := r.holes[i]
		h.StrokeColor = theme.Color(theme.ColorNamePlaceHolder)
		h.FillColor = st.color()
		if st == pinEmpty {
			h.FillColor = theme.Color(theme.ColorNameBackground)
		}
		r.zifNum[i].Color = theme.Color(theme.ColorNamePlaceHolder)
		d := r.devNum[i]
		d.Color = color.White
		d.Text = ""
		if p := devicedb.ZIFToDevicePin(i+1, r.z.chipPins); p > 0 {
			d.Text = strconv.Itoa(p)
		}
		h.Refresh()
		r.zifNum[i].Refresh()
		d.Refresh()
	}
	for _, o := range []fyne.CanvasObject{r.title, r.body, r.lever, r.chip, r.notch} {
		o.Refresh()
	}
	r.Layout(r.z.Size())
}

func (r *zifRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.body, r.lever, r.title}
	for i := 0; i < 40; i++ {
		objs = append(objs, r.holes[i], r.zifNum[i])
	}
	objs = append(objs, r.chip, r.notch)
	for i := 0; i < 40; i++ {
		objs = append(objs, r.devNum[i])
	}
	return objs
}

func (r *zifRenderer) Destroy() {}

// ---- pin check panel ----

type pinPanel struct {
	a       *App
	content fyne.CanvasObject
	socket  *zifSocket
	status  *widget.Label
	detail  *widget.Label
	roles   []devicedb.PinRole
}

func newPinPanel(a *App) *pinPanel {
	p := &pinPanel{a: a, socket: newZIFSocket()}
	p.status = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.status.Wrapping = fyne.TextWrapWord
	p.detail = widget.NewLabel("")
	p.detail.Wrapping = fyne.TextWrapWord
	run := widget.NewButtonWithIcon("Check pin contact", theme.ViewRefreshIcon(), a.doPinCheck)
	run.Importance = widget.HighImportance

	swatch := func(st pinState, label string) fyne.CanvasObject {
		c := canvas.NewCircle(st.color())
		return container.NewHBox(container.NewGridWrap(fyne.NewSize(14, 14), c), widget.NewLabel(label))
	}
	legend := container.NewVBox(
		widget.NewLabelWithStyle("Legend", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		swatch(pinGood, "Good contact"),
		swatch(pinBad, "Bad contact"),
		swatch(pinUnknown, "Not checked yet"),
		swatch(pinGround, "Chip ground (drives the test)"),
		swatch(pinUntested, "Not covered by the test (e.g. VCC)"),
	)
	note := widget.NewLabel("Tick \"Pin contact check\" in the options to test the socket before every " +
		"read, program, verify or erase, as the XGecu software does.")
	note.Wrapping = fyne.TextWrapWord
	side := container.NewVBox(run, p.status, p.detail, widget.NewSeparator(), legend, widget.NewSeparator(), note)
	split := container.NewHSplit(container.NewPadded(p.socket), container.NewVScroll(side))
	split.Offset = 0.5
	p.content = split
	return p
}

// pinCheckSupport reports whether a contact test can run for the current
// programmer and device, and why not.
func (a *App) pinCheckSupport() (bool, string) {
	d := a.device
	switch {
	case d == nil:
		return false, "Select a device first."
	case a.opts != nil && a.opts.icsp.SelectedIndex() > 0:
		return false, "The pin contact test only works in the ZIF socket, not over ICSP."
	case a.programmer != devicedb.TL866II && a.programmer != devicedb.T48:
		return false, fmt.Sprintf("minipro has no working pin contact test for the %s.", a.programmer.Label())
	case a.db == nil || a.db.PinRoles(d) == nil:
		return false, d.Name + " has no pin map in the database, so its contacts can't be tested."
	}
	return true, ""
}

func (p *pinPanel) refreshForDevice() {
	d := p.a.device
	p.socket.pins = [40]pinState{}
	p.socket.chipPins = 0
	p.roles = nil
	if d != nil && d.Type != devicedb.TypeLogic && p.a.db != nil {
		if n := d.PinCount(); n > 0 && n <= 40 && d.Adapter() == 0 {
			p.socket.chipPins = n
			p.roles = p.a.db.PinRoles(d)
			for pin := 1; pin <= n; pin++ {
				st := pinUntested
				if p.roles != nil {
					switch p.roles[pin-1] {
					case devicedb.PinTested:
						st = pinUnknown
					case devicedb.PinGround:
						st = pinGround
					}
				}
				p.socket.pins[devicedb.DevicePinToZIF(pin, n)-1] = st
			}
		}
	}
	ok, why := p.a.pinCheckSupport()
	switch {
	case d != nil && d.Adapter() != 0:
		p.status.SetText(fmt.Sprintf("%s needs adapter #%03d; the socket view does not apply.", d.Name, d.Adapter()))
	case ok:
		p.status.SetText("Not checked yet.")
	default:
		p.status.SetText(why)
	}
	p.detail.SetText("")
	p.socket.Refresh()
}

var badPinRe = regexp.MustCompile(`Bad contact on pin:\s*(\d+)`)

// applyResult updates the socket from minipro's pin test output.
func (p *pinPanel) applyResult(output string) {
	n := p.socket.chipPins
	if n == 0 || p.roles == nil {
		return
	}
	var bad []int
	for _, m := range badPinRe.FindAllStringSubmatch(output, -1) {
		if v, err := strconv.Atoi(m[1]); err == nil && v >= 1 && v <= n {
			bad = append(bad, v)
		}
	}
	passed := strings.Contains(output, "Pin test passed")
	if !passed && len(bad) == 0 {
		for _, msg := range []string{"Pin test is not supported", "pin test not implemented", "Pin test is not available"} {
			if strings.Contains(output, msg) {
				p.status.SetText(msg + ".")
			}
		}
		return // the test did not run (e.g. no programmer)
	}
	isBad := map[int]bool{}
	for _, b := range bad {
		isBad[b] = true
	}
	good := 0
	for pin := 1; pin <= n; pin++ {
		if p.roles[pin-1] != devicedb.PinTested {
			continue
		}
		st := pinGood
		if isBad[pin] {
			st = pinBad
		} else {
			good++
		}
		p.socket.pins[devicedb.DevicePinToZIF(pin, n)-1] = st
	}
	p.socket.Refresh()
	if len(bad) == 0 {
		p.status.Importance = widget.SuccessImportance
		p.status.SetText(fmt.Sprintf("All %d tested pins have good contact.", good))
		p.detail.SetText("")
		return
	}
	sort.Ints(bad)
	strs := make([]string, len(bad))
	for i, b := range bad {
		strs[i] = strconv.Itoa(b)
	}
	p.status.Importance = widget.DangerImportance
	p.status.SetText(fmt.Sprintf("Bad contact on %d pin(s): %s", len(bad), strings.Join(strs, ", ")))
	p.detail.SetText("Check that the chip sits straight with pin 1 at the top-left (next to the lever), " +
		"that the lever is closed, and that the pins are clean and not bent.")
	p.a.tabs.Select(p.a.tabPins)
}

func (a *App) doPinCheck() {
	if !a.requireDevice() {
		return
	}
	if ok, why := a.pinCheckSupport(); !ok {
		dialog.ShowInformation("Pin contact check", why, a.win)
		return
	}
	a.tabs.Select(a.tabPins)
	o := a.opts.options()
	o.PinCheck = true
	a.job("Pin contact check", func(_ context.Context, run runFunc) error {
		_, err := run(o.Args(minipro.ActPinCheck, ""))
		return err
	})
}
