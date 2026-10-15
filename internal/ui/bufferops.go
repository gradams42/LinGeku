package ui

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lingecu/internal/buffer"
)

// activeEditor returns the hex editor of the visible memory tab.
func (a *App) activeEditor() *HexEditor {
	if a.tabs.Selected() == a.tabData {
		return a.dataEd
	}
	return a.codeEd
}

// rangeEntries returns start/end entries prefilled with the selection or the
// whole buffer.
func (a *App) rangeEntries(ed *HexEditor) (*widget.Entry, *widget.Entry) {
	s, e, ok := ed.Selection()
	if !ok {
		s, e = 0, ed.Buffer().Len()
	}
	start, end := widget.NewEntry(), widget.NewEntry()
	start.SetText(fmt.Sprintf("0x%X", s))
	end.SetText(fmt.Sprintf("0x%X", max(e-1, 0)))
	return start, end
}

// parseRange converts inclusive start/end entries to [start, end).
func parseRange(s, e *widget.Entry) (int, int, error) {
	start, err := parseNum(s.Text)
	if err != nil {
		return 0, 0, err
	}
	end, err := parseNum(e.Text)
	if err != nil {
		return 0, 0, err
	}
	if end < start {
		return 0, 0, errors.New("end address is before start address")
	}
	return start, end + 1, nil
}

// parsePattern accepts hex bytes ("AA 55", "aa55") or a quoted ASCII string.
func parsePattern(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return []byte(s[1 : len(s)-1]), nil
	}
	clean := strings.NewReplacer(" ", "", ",", "", "0x", "", "0X", "").Replace(s)
	if len(clean)%2 == 1 {
		clean = "0" + clean
	}
	b, err := hex.DecodeString(clean)
	if err != nil || len(b) == 0 {
		return nil, fmt.Errorf("invalid pattern %q: use hex bytes like \"AA 55\" or a quoted string", s)
	}
	return b, nil
}

func (a *App) gotoDialog() {
	ed := a.activeEditor()
	e := widget.NewEntry()
	e.SetText(fmt.Sprintf("0x%X", ed.Cursor()))
	dialog.ShowForm("Go to address", "Go", "Cancel", []*widget.FormItem{widget.NewFormItem("Address", e)}, func(ok bool) {
		if !ok {
			return
		}
		off, err := parseNum(e.Text)
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		ed.Goto(off)
	}, a.win)
}

func (a *App) selectBlockDialog() {
	ed := a.activeEditor()
	s, e := a.rangeEntries(ed)
	dialog.ShowForm("Select block", "Select", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Start", s), widget.NewFormItem("End (inclusive)", e),
	}, func(ok bool) {
		if !ok {
			return
		}
		start, end, err := parseRange(s, e)
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		ed.SetSelection(start, end)
	}, a.win)
}

func (a *App) findDialog() {
	ed := a.activeEditor()
	find, repl := widget.NewEntry(), widget.NewEntry()
	find.SetPlaceHolder(`hex bytes "DE AD BE EF" or "text"`)
	repl.SetPlaceHolder("optional replacement, same length")
	var dlg dialog.Dialog
	next := widget.NewButton("Find next", func() {
		pat, err := parsePattern(find.Text)
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		from := ed.Cursor() + 1
		if _, _, ok := ed.Selection(); !ok {
			from = ed.Cursor()
		}
		if off := ed.Buffer().Find(pat, from); off >= 0 {
			ed.SetSelection(off, off+len(pat))
		} else {
			dialog.ShowInformation("Find", "Pattern not found.", a.win)
		}
	})
	replAll := widget.NewButton("Replace all", func() {
		pat, err := parsePattern(find.Text)
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		rp, err := parsePattern(repl.Text)
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		n, err := ed.Buffer().Replace(pat, rp)
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		a.logf("Replaced %d occurrence(s)", n)
	})
	form := widget.NewForm(widget.NewFormItem("Find", find), widget.NewFormItem("Replace with", repl))
	dlg = dialog.NewCustom("Find / replace", "Close", container.NewVBox(form, container.NewHBox(next, replAll)), a.win)
	dlg.Resize(fyne.NewSize(480, 220))
	dlg.Show()
}

func (a *App) fillDialog() {
	ed := a.activeEditor()
	s, e := a.rangeEntries(ed)
	pat := widget.NewEntry()
	pat.SetText(fmt.Sprintf("%02X", ed.Buffer().Blank()))
	random := widget.NewCheck("Random data", nil)
	dialog.ShowForm("Fill block", "Fill", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Start", s), widget.NewFormItem("End (inclusive)", e),
		widget.NewFormItem("Pattern", pat), widget.NewFormItem("", random),
	}, func(ok bool) {
		if !ok {
			return
		}
		start, end, err := parseRange(s, e)
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		if random.Checked {
			err = ed.Buffer().FillRandom(start, end, func(b []byte) { rand.Read(b) })
		} else {
			var p []byte
			if p, err = parsePattern(pat.Text); err == nil {
				err = ed.Buffer().Fill(start, end, p)
			}
		}
		if err != nil {
			dialog.ShowError(err, a.win)
		}
	}, a.win)
}

func (a *App) copyBlockDialog() {
	ed := a.activeEditor()
	s, e := a.rangeEntries(ed)
	dst := widget.NewEntry()
	dst.SetText("0x0")
	dialog.ShowForm("Copy block", "Copy", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Source start", s), widget.NewFormItem("Source end (inclusive)", e),
		widget.NewFormItem("Destination", dst),
	}, func(ok bool) {
		if !ok {
			return
		}
		start, end, err := parseRange(s, e)
		if err == nil {
			var d int
			if d, err = parseNum(dst.Text); err == nil {
				err = ed.Buffer().Copy(start, d, end-start)
			}
		}
		if err != nil {
			dialog.ShowError(err, a.win)
		}
	}, a.win)
}

func (a *App) swapDialog() {
	ed := a.activeEditor()
	s, e := a.rangeEntries(ed)
	mode := widget.NewSelect([]string{"16-bit (AB → BA)", "32-bit (ABCD → DCBA)"}, nil)
	mode.SetSelectedIndex(0)
	dialog.ShowForm("Swap bytes", "Swap", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Start", s), widget.NewFormItem("End (inclusive)", e), widget.NewFormItem("Mode", mode),
	}, func(ok bool) {
		if !ok {
			return
		}
		start, end, err := parseRange(s, e)
		if err == nil {
			err = ed.Buffer().SwapBytes(start, end, 2<<mode.SelectedIndex())
		}
		if err != nil {
			dialog.ShowError(err, a.win)
		}
	}, a.win)
}

func (a *App) invertSelection() {
	ed := a.activeEditor()
	s, e, ok := ed.Selection()
	if !ok {
		s, e = 0, ed.Buffer().Len()
	}
	if err := ed.Buffer().Invert(s, e); err != nil {
		dialog.ShowError(err, a.win)
	}
}

func (a *App) clearBuffer() {
	ed := a.activeEditor()
	dialog.ShowConfirm("Clear buffer", fmt.Sprintf("Fill the whole buffer with 0x%02X?", ed.Buffer().Blank()), func(ok bool) {
		if ok {
			ed.Buffer().Fill(0, ed.Buffer().Len(), []byte{ed.Buffer().Blank()})
			ed.Buffer().SetModified(false)
		}
	}, a.win)
}

func (a *App) checksumDialog() {
	ed := a.activeEditor()
	s, e := a.rangeEntries(ed)
	out := widget.NewLabel("")
	out.TextStyle = fyne.TextStyle{Monospace: true}
	calc := func() {
		start, end, err := parseRange(s, e)
		if err != nil {
			out.SetText(err.Error())
			return
		}
		c, err := ed.Buffer().Checksum(start, end)
		if err != nil {
			out.SetText(err.Error())
			return
		}
		out.SetText(fmt.Sprintf(
			"Range        0x%X – 0x%X (%d bytes)\n\nSum (8-bit)  %02X\nSum (16-bit) %04X\nSum (32-bit) %08X\n"+
				"Word sum 16  %04X\nXOR          %02X\nCRC-16/ARC   %04X\nCRC-32       %08X",
			start, end-1, end-start, c.Sum8, c.Sum16, c.Sum32, c.Word16, c.XOR8, c.CRC16, c.CRC32))
	}
	calc()
	form := widget.NewForm(widget.NewFormItem("Start", s), widget.NewFormItem("End (inclusive)", e))
	content := container.NewVBox(form, widget.NewButton("Calculate", calc), out)
	dialog.ShowCustom("Checksum", "Close", content, a.win)
}

func (a *App) compareDialog() {
	ed := a.activeEditor()
	a.fileOpenDialog("Compare buffer with file", func(path string) {
		img, _, err := buffer.ReadFile(path, buffer.FormatAuto)
		if err != nil {
			a.showError(err)
			return
		}
		diffs, total := buffer.Compare(ed.Buffer().Bytes(), img.Data, ed.Buffer().Blank(), 2000)
		fyne.Do(func() {
			if total == 0 {
				dialog.ShowInformation("Compare", "The buffer and the file are identical.", a.win)
				return
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%d differing byte(s)", total)
			if total > len(diffs) {
				fmt.Fprintf(&b, " (first %d shown)", len(diffs))
			}
			b.WriteString("\n\nAddress     Buffer  File\n")
			for _, d := range diffs {
				fmt.Fprintf(&b, "%08X    %02X      %02X\n", d.Offset, d.A, d.B)
			}
			if len(diffs) > 0 {
				ed.Goto(diffs[0].Offset)
			}
			a.textDialog("Compare result", b.String())
		})
	})
}

// serialInsertDialog writes a number at an address once (the auto-program
// window applies serial numbers automatically per chip).
func (a *App) serialInsertDialog() {
	ed := a.activeEditor()
	s := newSerialForm(a)
	dialog.ShowForm("Insert value", "Insert", "Cancel", s.items(), func(ok bool) {
		if !ok {
			return
		}
		cfg, err := s.config()
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		b, err := cfg.encode(cfg.Start)
		if err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		ed.Buffer().Write(cfg.Address, b)
		ed.SetSelection(cfg.Address, cfg.Address+len(b))
	}, a.win)
}
