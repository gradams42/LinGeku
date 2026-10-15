package ui

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"lingecu/internal/buffer"
)

const bytesPerRow = 16

// HexEditor is a virtualised hex/ASCII editor over a buffer.Buffer.
type HexEditor struct {
	widget.BaseWidget

	buf      *buffer.Buffer
	list     *widget.List
	ReadOnly bool

	cursor    int
	nibble    int // 0 = high nibble next, 1 = low nibble next
	ascii     bool
	selAnchor int // -1 when no selection
	selEnd    int
	focused   bool

	// OnCursor is called when the cursor or selection moves.
	OnCursor func(off int)

	charW, rowH float32
}

// NewHexEditor creates an editor for buf.
func NewHexEditor(buf *buffer.Buffer) *HexEditor {
	e := &HexEditor{buf: buf, selAnchor: -1}
	e.ExtendBaseWidget(e)
	e.measure()
	e.list = widget.NewList(
		func() int { return (e.buf.Len() + bytesPerRow - 1) / bytesPerRow },
		func() fyne.CanvasObject { return newHexRow(e) },
		func(id widget.ListItemID, o fyne.CanvasObject) { o.(*hexRow).setRow(id) },
	)
	buf.OnChange(func() { fyne.Do(e.list.Refresh) })
	return e
}

// SetBuffer switches the edited buffer.
func (e *HexEditor) SetBuffer(buf *buffer.Buffer) {
	e.buf = buf
	buf.OnChange(func() { fyne.Do(e.list.Refresh) })
	e.cursor, e.nibble, e.selAnchor = 0, 0, -1
	e.list.Refresh()
	e.list.ScrollToTop()
}

// Buffer returns the edited buffer.
func (e *HexEditor) Buffer() *buffer.Buffer { return e.buf }

func (e *HexEditor) measure() {
	sz := fyne.MeasureText("0", theme.TextSize(), fyne.TextStyle{Monospace: true})
	e.charW, e.rowH = sz.Width, sz.Height+2
}

// CreateRenderer implements fyne.Widget.
func (e *HexEditor) CreateRenderer() fyne.WidgetRenderer {
	header := canvas.NewText(headerText(), theme.Color(theme.ColorNamePrimary))
	header.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	return &hexEditorRenderer{e: e, header: header}
}

func headerText() string {
	var b strings.Builder
	b.WriteString(" Address   ")
	for i := 0; i < bytesPerRow; i++ {
		if i == 8 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%02X ", i)
	}
	b.WriteString("  ASCII")
	return b.String()
}

type hexEditorRenderer struct {
	e      *HexEditor
	header *canvas.Text
}

func (r *hexEditorRenderer) Layout(s fyne.Size) {
	hh := r.header.MinSize().Height + 4
	r.header.Move(fyne.NewPos(theme.Padding(), 2))
	r.e.list.Move(fyne.NewPos(0, hh))
	r.e.list.Resize(fyne.NewSize(s.Width, s.Height-hh))
}

func (r *hexEditorRenderer) MinSize() fyne.Size {
	w := r.e.charW*float32(len(headerText())+bytesPerRow+4) + 2*theme.Padding() + theme.ScrollBarSize()
	return fyne.NewSize(w, 200)
}

func (r *hexEditorRenderer) Refresh() {
	r.header.Color = theme.Color(theme.ColorNamePrimary)
	r.header.Refresh()
	r.e.list.Refresh()
}

func (r *hexEditorRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.header, r.e.list}
}

func (r *hexEditorRenderer) Destroy() {}

// Column positions, in characters.
func hexCol(i int) int {
	c := 11 + i*3
	if i >= 8 {
		c++
	}
	return c
}

func asciiCol(i int) int { return hexCol(bytesPerRow-1) + 5 + i }

// ---- cursor & selection ----

// Cursor returns the cursor offset.
func (e *HexEditor) Cursor() int { return e.cursor }

// Selection returns the selected range [start, end); ok is false when nothing
// is selected.
func (e *HexEditor) Selection() (start, end int, ok bool) {
	if e.selAnchor < 0 {
		return 0, 0, false
	}
	s, t := e.selAnchor, e.selEnd
	if s > t {
		s, t = t, s
	}
	return s, t + 1, true
}

// SetSelection selects [start, end).
func (e *HexEditor) SetSelection(start, end int) {
	if end <= start {
		e.selAnchor = -1
	} else {
		e.selAnchor, e.selEnd = start, end-1
	}
	e.Goto(start)
}

// ClearSelection removes the selection.
func (e *HexEditor) ClearSelection() {
	e.selAnchor = -1
	e.list.Refresh()
}

// Goto moves the cursor to off and scrolls it into view.
func (e *HexEditor) Goto(off int) {
	n := e.buf.Len()
	if n == 0 {
		return
	}
	off = max(0, min(off, n-1))
	e.cursor, e.nibble = off, 0
	e.list.ScrollTo(off / bytesPerRow)
	e.list.Refresh()
	if e.OnCursor != nil {
		e.OnCursor(off)
	}
}

func (e *HexEditor) moveCursor(off int, extend bool) {
	if extend {
		if e.selAnchor < 0 {
			e.selAnchor = e.cursor
		}
		e.selEnd = max(0, min(off, e.buf.Len()-1))
	} else {
		e.selAnchor = -1
	}
	e.Goto(off)
}

func (e *HexEditor) selected(off int) bool {
	s, t, ok := e.Selection()
	return ok && off >= s && off < t
}

// ---- focus & keyboard ----

// FocusGained implements fyne.Focusable.
func (e *HexEditor) FocusGained() { e.focused = true; e.list.Refresh() }

// FocusLost implements fyne.Focusable.
func (e *HexEditor) FocusLost() { e.focused = false; e.list.Refresh() }

// TypedRune edits the byte under the cursor.
func (e *HexEditor) TypedRune(r rune) {
	if e.ReadOnly || e.buf.Len() == 0 {
		return
	}
	if e.ascii {
		if r < 0x20 || r > 0x7e {
			return
		}
		e.buf.Set(e.cursor, byte(r))
		e.moveCursor(e.cursor+1, false)
		return
	}
	var v byte
	switch {
	case r >= '0' && r <= '9':
		v = byte(r - '0')
	case r >= 'a' && r <= 'f':
		v = byte(r-'a') + 10
	case r >= 'A' && r <= 'F':
		v = byte(r-'A') + 10
	default:
		return
	}
	old := e.buf.At(e.cursor)
	if e.nibble == 0 {
		e.buf.Set(e.cursor, v<<4|old&0x0f)
		e.nibble = 1
		e.list.Refresh()
	} else {
		e.buf.Set(e.cursor, old&0xf0|v)
		e.moveCursor(e.cursor+1, false)
	}
}

// TypedKey handles navigation keys.
func (e *HexEditor) TypedKey(k *fyne.KeyEvent) {
	shift := false
	if d, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok {
		shift = d.CurrentKeyModifiers()&fyne.KeyModifierShift != 0
	}
	page := bytesPerRow * max(1, int(e.list.Size().Height/max(e.rowH, 1))-1)
	switch k.Name {
	case fyne.KeyLeft:
		e.moveCursor(e.cursor-1, shift)
	case fyne.KeyRight:
		e.moveCursor(e.cursor+1, shift)
	case fyne.KeyUp:
		e.moveCursor(e.cursor-bytesPerRow, shift)
	case fyne.KeyDown:
		e.moveCursor(e.cursor+bytesPerRow, shift)
	case fyne.KeyPageUp:
		e.moveCursor(e.cursor-page, shift)
	case fyne.KeyPageDown:
		e.moveCursor(e.cursor+page, shift)
	case fyne.KeyHome:
		e.moveCursor(e.cursor-e.cursor%bytesPerRow, shift)
	case fyne.KeyEnd:
		e.moveCursor(e.cursor-e.cursor%bytesPerRow+bytesPerRow-1, shift)
	case fyne.KeyTab:
		e.ascii = !e.ascii
		e.nibble = 0
		e.list.Refresh()
	case fyne.KeyEscape:
		e.ClearSelection()
	}
}

// AcceptsTab keeps Tab for switching between hex and ASCII columns.
func (e *HexEditor) AcceptsTab() bool { return true }

func (e *HexEditor) focus() {
	if c := fyne.CurrentApp().Driver().CanvasForObject(e); c != nil {
		c.Focus(e)
	}
}

// ---- rows ----

type hexRow struct {
	widget.BaseWidget
	e   *HexEditor
	row int
}

func newHexRow(e *HexEditor) *hexRow {
	r := &hexRow{e: e}
	r.ExtendBaseWidget(r)
	return r
}

func (r *hexRow) setRow(row int) {
	r.row = row
	r.Refresh()
}

func (r *hexRow) hit(pos fyne.Position) (off int, ascii bool, ok bool) {
	col := int((pos.X - theme.Padding()) / r.e.charW)
	base := r.row * bytesPerRow
	for i := 0; i < bytesPerRow; i++ {
		if c := hexCol(i); col >= c && col < c+3 {
			return base + i, false, base+i < r.e.buf.Len()
		}
		if col == asciiCol(i) {
			return base + i, true, base+i < r.e.buf.Len()
		}
	}
	return 0, false, false
}

// MouseDown selects the byte under the pointer; shift extends the selection.
func (r *hexRow) MouseDown(ev *desktop.MouseEvent) {
	off, ascii, ok := r.hit(ev.Position)
	r.e.focus()
	if !ok {
		return
	}
	r.e.ascii = ascii
	r.e.moveCursor(off, ev.Modifier&fyne.KeyModifierShift != 0)
}

// MouseUp implements desktop.Mouseable.
func (r *hexRow) MouseUp(*desktop.MouseEvent) {}

// Dragged extends the selection while dragging across bytes.
func (r *hexRow) Dragged(ev *fyne.DragEvent) {
	pos := ev.Position
	rowShift := int(pos.Y / r.e.rowH)
	if pos.Y < 0 {
		rowShift--
	}
	tmp := &hexRow{e: r.e, row: r.row + rowShift}
	pos.Y = 0
	if off, _, ok := tmp.hit(pos); ok {
		r.e.moveCursor(off, true)
	}
}

// DragEnd implements fyne.Draggable.
func (r *hexRow) DragEnd() {}

func (r *hexRow) CreateRenderer() fyne.WidgetRenderer {
	rr := &hexRowRenderer{r: r}
	rr.addr = monoText()
	for i := 0; i < bytesPerRow; i++ {
		rr.hex[i] = monoText()
		rr.asc[i] = monoText()
		rr.selHex[i] = canvas.NewRectangle(color.Transparent)
		rr.selAsc[i] = canvas.NewRectangle(color.Transparent)
	}
	return rr
}

func monoText() *canvas.Text {
	t := canvas.NewText("", theme.Color(theme.ColorNameForeground))
	t.TextStyle = fyne.TextStyle{Monospace: true}
	return t
}

type hexRowRenderer struct {
	r              *hexRow
	addr           *canvas.Text
	hex, asc       [bytesPerRow]*canvas.Text
	selHex, selAsc [bytesPerRow]*canvas.Rectangle
}

func (rr *hexRowRenderer) Layout(fyne.Size) {
	e, p := rr.r.e, theme.Padding()
	x := func(col int) float32 { return p + float32(col)*e.charW }
	rr.addr.Move(fyne.NewPos(x(0), 1))
	for i := 0; i < bytesPerRow; i++ {
		rr.hex[i].Move(fyne.NewPos(x(hexCol(i)), 1))
		rr.asc[i].Move(fyne.NewPos(x(asciiCol(i)), 1))
		rr.selHex[i].Move(fyne.NewPos(x(hexCol(i))-e.charW/2, 0))
		rr.selHex[i].Resize(fyne.NewSize(e.charW*3, e.rowH))
		rr.selAsc[i].Move(fyne.NewPos(x(asciiCol(i)), 0))
		rr.selAsc[i].Resize(fyne.NewSize(e.charW, e.rowH))
	}
}

func (rr *hexRowRenderer) MinSize() fyne.Size {
	e := rr.r.e
	return fyne.NewSize(e.charW*float32(asciiCol(bytesPerRow))+2*theme.Padding(), e.rowH)
}

func (rr *hexRowRenderer) Refresh() {
	e := rr.r.e
	fg := theme.Color(theme.ColorNameForeground)
	dim := theme.Color(theme.ColorNamePlaceHolder)
	sel := theme.Color(theme.ColorNameSelection)
	base := rr.r.row * bytesPerRow
	data := e.buf.Slice(base, bytesPerRow)
	rr.addr.Text = fmt.Sprintf("%08X:", base)
	rr.addr.Color = theme.Color(theme.ColorNamePrimary)
	rr.addr.Refresh()
	blank := e.buf.Blank()
	for i := 0; i < bytesPerRow; i++ {
		off := base + i
		h, a := rr.hex[i], rr.asc[i]
		rr.selHex[i].FillColor, rr.selAsc[i].FillColor = color.Transparent, color.Transparent
		if i >= len(data) {
			h.Text, a.Text = "", ""
		} else {
			v := data[i]
			h.Text = fmt.Sprintf("%02X", v)
			if v >= 0x20 && v < 0x7f {
				a.Text = string(rune(v))
			} else {
				a.Text = "."
			}
			if v == blank {
				h.Color, a.Color = dim, dim
			} else {
				h.Color, a.Color = fg, fg
			}
			if e.selected(off) {
				rr.selHex[i].FillColor, rr.selAsc[i].FillColor = sel, sel
			}
			if off == e.cursor {
				hl := theme.Color(theme.ColorNamePrimary)
				if !e.focused {
					hl = theme.Color(theme.ColorNameDisabled)
				}
				if e.ascii {
					rr.selAsc[i].FillColor = hl
					a.Color = theme.Color(theme.ColorNameBackground)
					rr.selHex[i].FillColor = sel
				} else {
					rr.selHex[i].FillColor = hl
					h.Color = theme.Color(theme.ColorNameBackground)
					rr.selAsc[i].FillColor = sel
				}
			}
		}
		h.Refresh()
		a.Refresh()
		rr.selHex[i].Refresh()
		rr.selAsc[i].Refresh()
	}
}

func (rr *hexRowRenderer) Objects() []fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, 0, 4*bytesPerRow+2)
	for i := 0; i < bytesPerRow; i++ {
		objs = append(objs, rr.selHex[i], rr.selAsc[i])
	}
	objs = append(objs, rr.addr)
	for i := 0; i < bytesPerRow; i++ {
		objs = append(objs, rr.hex[i], rr.asc[i])
	}
	return objs
}

func (rr *hexRowRenderer) Destroy() {}
