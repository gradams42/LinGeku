package ui

import (
	"fmt"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"lingecu/internal/devicedb"
)

func (a *App) showDevicePicker() { a.showDevicePickerType(0) }

// showDevicePickerType opens the IC selector, optionally pre-filtered by type.
func (a *App) showDevicePickerType(typ int) {
	if a.db == nil {
		dialog.ShowInformation("Device database", "The device database is not loaded yet (see the log).", a.win)
		return
	}
	all := a.db.Devices(a.programmer)
	mfrs := append([]string{"All manufacturers"}, a.db.Manufacturers(a.programmer)...)

	typeNames := []string{"All types"}
	typeIDs := []int{0}
	var ids []int
	for id := range devicedb.TypeNames {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		typeNames = append(typeNames, devicedb.TypeNames[id])
		typeIDs = append(typeIDs, id)
	}

	var filtered []*devicedb.Device
	var chosen *devicedb.Device
	mfr, curType, pkg := "", typ, "All packages"

	search := widget.NewEntry()
	search.SetPlaceHolder("Search part number, e.g. 27C512, W25Q64, ATMEGA328…")
	countLbl := widget.NewLabel("")
	details := widget.NewLabel("Select a device to see its details.")
	details.Wrapping = fyne.TextWrapWord

	devList := widget.NewList(func() int { return len(filtered) },
		func() fyne.CanvasObject { return widget.NewLabel("template device name @PKG") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			d := filtered[i]
			o.(*widget.Label).SetText(fmt.Sprintf("%s   [%s]", d.Name, d.Manufacturer))
		})

	apply := func() {
		filtered = devicedb.Filter(all, search.Text, mfr, curType, pkg == "SMD only", pkg == "DIP / through-hole")
		countLbl.SetText(fmt.Sprintf("%d of %d devices", len(filtered), len(all)))
		chosen = nil
		devList.UnselectAll()
		devList.Refresh()
		devList.ScrollToTop()
	}
	describe := func(d *devicedb.Device) string {
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n\nManufacturer: %s\nType: %s\nPackage: %s\nSocket: %s\n", d.Name, d.Manufacturer, d.TypeName(), d.Package(), d.SocketInfo())
		if d.Type == devicedb.TypeLogic {
			fmt.Fprintf(&b, "Voltage: %s\nTest vectors: %d\n", d.LogicVoltage, len(d.Vectors))
			return b.String()
		}
		fmt.Fprintf(&b, "Code memory: %s\n", sizeString(d.CodeBytes(), d.Type == devicedb.TypePLD))
		if d.DataBytes() > 0 {
			fmt.Fprintf(&b, "Data memory: %s\n", sizeString(d.DataBytes(), false))
		}
		fmt.Fprintf(&b, "Chip ID: %s\nICSP: %s\nErasable: %v\n", d.ChipIDString(), d.ICSPMode(), d.CanErase())
		if d.ConfigName != "" {
			fmt.Fprintf(&b, "Config profile: %s\n", d.ConfigName)
		}
		if d.Custom {
			b.WriteString("\n(custom device definition)\n")
		}
		return b.String()
	}
	devList.OnSelected = func(i widget.ListItemID) {
		chosen = filtered[i]
		details.SetText(describe(chosen))
	}

	mfrList := widget.NewList(func() int { return len(mfrs) },
		func() fyne.CanvasObject { return widget.NewLabel("Manufacturer name") },
		func(i widget.ListItemID, o fyne.CanvasObject) { o.(*widget.Label).SetText(mfrs[i]) })
	mfrList.OnSelected = func(i widget.ListItemID) {
		mfr = ""
		if i > 0 {
			mfr = mfrs[i]
		}
		apply()
	}

	typeSel := widget.NewSelect(typeNames, func(s string) {
		for i, n := range typeNames {
			if n == s {
				curType = typeIDs[i]
			}
		}
		apply()
	})
	pkgSel := widget.NewSelect([]string{"All packages", "DIP / through-hole", "SMD only"}, func(s string) { pkg = s; apply() })
	recentSel := widget.NewSelect(a.recent, func(s string) {
		search.SetText(s)
	})
	recentSel.PlaceHolder = "Recent devices"
	search.OnChanged = func(string) { apply() }

	for i, id := range typeIDs {
		if id == typ {
			typeSel.SetSelected(typeNames[i])
		}
	}
	pkgSel.SetSelected("All packages")
	apply()

	var dlg *dialog.CustomDialog
	selectBtn := widget.NewButton("Select", func() {
		if chosen == nil && len(filtered) == 1 {
			chosen = filtered[0]
		}
		if chosen == nil {
			return
		}
		a.selectDevice(chosen)
		dlg.Hide()
	})
	selectBtn.Importance = widget.HighImportance
	search.OnSubmitted = func(string) { selectBtn.OnTapped() }
	cancelBtn := widget.NewButton("Cancel", func() { dlg.Hide() })

	top := container.NewBorder(nil, nil, nil, container.NewHBox(typeSel, pkgSel, recentSel), search)
	mid := container.NewHSplit(mfrList, container.NewHSplit(devList, container.NewVScroll(details)))
	mid.Offset = 0.22
	content := container.NewBorder(top, container.NewBorder(nil, nil, countLbl, container.NewHBox(cancelBtn, selectBtn)), nil, nil, mid)

	dlg = dialog.NewCustomWithoutButtons("Select IC — "+a.programmer.Label(), content, a.win)
	dlg.Resize(fyne.NewSize(1100, 700))
	dlg.Show()
	a.win.Canvas().Focus(search)
}

func (a *App) showDeviceInfo() {
	if !a.requireDevice() {
		return
	}
	d := a.device
	var b strings.Builder
	fmt.Fprintf(&b, "Name:            %s\nManufacturer:    %s\nType:            %s\nPackage:         %s\nSocket:          %s\n",
		d.Name, d.Manufacturer, d.TypeName(), d.Package(), d.SocketInfo())
	if d.Type != devicedb.TypeLogic {
		vpp, vcc, vdd := d.DefaultVoltages()
		fmt.Fprintf(&b, "Code memory:     %s\nData memory:     %s\nPage size:       %d\nWord size:       %d bit\n"+
			"Chip ID:         %s\nProtocol:        0x%02X  variant 0x%X\nRead/Write buf:  %d / %d bytes\n"+
			"Voltages (raw):  VPP %s VCC %s VDD %s\nPulse delay:     %d µs\nBlank value:     0x%02X\n"+
			"ICSP:            %s\nErase:           %v\nConfig profile:  %s\n",
			sizeString(d.CodeBytes(), d.Type == devicedb.TypePLD), sizeString(d.DataBytes(), false), d.PageSize, 8*d.WordSize(),
			d.ChipIDString(), d.ProtocolID&0xff, d.Variant, d.ReadBuffer, d.WriteBuffer,
			vpp, vcc, vdd, d.PulseDelay, d.BlankValue, d.ICSPMode(), d.CanErase(), orDash(d.ConfigName))
	}
	text := b.String()
	if a.runner == nil {
		a.textDialog("Device information", text)
		return
	}
	go func() {
		args := a.withDatabases([]string{"-d", d.Name, "-q", string(a.programmer)})
		res, err := a.runner.Simple(t0(), args...)
		if err == nil && strings.TrimSpace(res.Output) != "" {
			text += "\n── minipro -d ──\n" + res.Output
		}
		fyne.Do(func() { a.textDialog("Device information", text) })
	}()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
