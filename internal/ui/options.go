package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"lingecu/internal/devicedb"
	"lingecu/internal/minipro"
)

const defaultChoice = "Default"

// optionsPanel holds the programming options shown in the left column.
type optionsPanel struct {
	a       *App
	content fyne.CanvasObject

	areaCode, areaData, areaConfig, areaCalib, areaUser *widget.Check

	erase, verify, blankFirst, checkID, ignoreID, pinCheck *widget.Check
	unprotect, protect, skipBlank                          *widget.Check
	sizeMode                                               *widget.Select
	icsp                                                   *widget.Select

	vpp, vdd, vcc, spiClock, ovc, partition *widget.Select
	pulse, i2cAddr                          *widget.Entry

	voltBox *fyne.Container
}

func newOptionsPanel(a *App) *optionsPanel {
	p := &optionsPanel{a: a}
	save := func(key string) func(bool) {
		return func(v bool) { a.prefs.SetBool("opt."+key, v) }
	}
	check := func(label, key string, def bool) *widget.Check {
		c := widget.NewCheck(label, save(key))
		c.SetChecked(a.prefs.BoolWithFallback("opt."+key, def))
		return c
	}
	p.areaCode = check("Code memory", "areaCode", true)
	p.areaData = check("Data memory (EEPROM)", "areaData", true)
	p.areaConfig = check("Config / fuses / lock bits", "areaConfig", true)
	p.areaCalib = check("Calibration bytes (read only)", "areaCalib", false)
	p.areaUser = check("User row / encryption table", "areaUser", false)

	p.erase = check("Erase before programming", "erase", true)
	p.verify = check("Verify after programming", "verify", true)
	p.blankFirst = check("Blank check before programming", "blankFirst", false)
	p.checkID = check("Check chip ID", "checkID", true)
	p.ignoreID = check("Continue on ID mismatch", "ignoreID", false)
	p.pinCheck = check("Pin contact check before each operation", "pinCheck", false)
	p.unprotect = check("Disable write protection before", "unprotect", false)
	p.protect = check("Enable write protection after", "protect", false)
	p.skipBlank = check("Skip writing blank (0xFF) blocks", "skipBlank", false)

	p.sizeMode = widget.NewSelect([]string{"Error", "Warn only", "Ignore"}, func(s string) { a.prefs.SetString("opt.sizeMode", s) })
	p.sizeMode.SetSelected(a.prefs.StringWithFallback("opt.sizeMode", "Warn only"))
	p.icsp = widget.NewSelect([]string{"Off (ZIF socket)", "ICSP, programmer supplies VCC", "ICSP, target powered"}, func(string) {
		if a.pins != nil {
			a.pins.refreshForDevice()
			p.refreshPinCheck()
		}
	})
	p.icsp.SetSelectedIndex(0)

	p.vpp = widget.NewSelect(nil, nil)
	p.vdd = widget.NewSelect(append([]string{defaultChoice}, "3.3", "4", "4.5", "5", "5.5", "6.5"), nil)
	p.vcc = widget.NewSelect(nil, nil)
	p.spiClock = widget.NewSelect(nil, nil)
	p.ovc = widget.NewSelect([]string{defaultChoice, "1 (120 mA)", "2 (150 mA)", "3 (180 mA)", "4 (200 mA)",
		"5 (220 mA)", "6 (250 mA)", "7 (300 mA)", "8 (350 mA)", "9 (400 mA)"}, nil)
	p.partition = widget.NewSelect([]string{"user", "boot1", "boot2", "rpmb"}, nil)
	p.pulse = widget.NewEntry()
	p.pulse.SetPlaceHolder("µs (default)")
	p.i2cAddr = widget.NewEntry()
	p.i2cAddr.SetPlaceHolder("0xA0 (default)")
	for _, s := range []*widget.Select{p.vdd, p.ovc} {
		s.SetSelected(defaultChoice)
	}
	p.partition.SetSelected("user")

	heading := func(s string) *widget.Label {
		return widget.NewLabelWithStyle(s, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	}
	p.voltBox = container.New(layout.NewFormLayout())
	p.content = container.NewVBox(
		heading("Memory areas"),
		p.areaCode, p.areaData, p.areaConfig, p.areaCalib, p.areaUser,
		widget.NewSeparator(),
		heading("Operation options"),
		p.erase, p.verify, p.blankFirst, p.checkID, p.ignoreID, p.pinCheck,
		p.unprotect, p.protect, p.skipBlank,
		container.New(layout.NewFormLayout(),
			widget.NewLabel("File size mismatch"), p.sizeMode,
			widget.NewLabel("ICSP"), p.icsp),
		widget.NewSeparator(),
		heading("Voltages & timing"),
		p.voltBox,
	)
	return p
}

func withDefault(vals ...string) []string { return append([]string{defaultChoice}, vals...) }

// refreshForDevice enables the options relevant to the selected device and
// programmer, like the XGecu software greys out unavailable options.
func (p *optionsPanel) refreshForDevice() {
	d, prog := p.a.device, p.a.programmer
	en := func(w fyne.Disableable, on bool) {
		if on {
			w.Enable()
		} else {
			w.Disable()
		}
	}
	has := d != nil && d.Type != devicedb.TypeLogic
	en(p.areaData, has && d.HasData())
	en(p.areaConfig, has && d.HasConfig() && d.Type != devicedb.TypePLD)
	en(p.areaCalib, has && d.HasCalibration())
	en(p.areaUser, has && d.Type == devicedb.TypeMCU)
	en(p.areaCode, has)
	en(p.erase, has && d.CanErase())
	en(p.unprotect, has && d.ProtectBefore())
	en(p.protect, has && d.ProtectAfter())
	en(p.checkID, has && d.HasChipID())
	en(p.ignoreID, has && d.HasChipID())
	en(p.icsp, has && d.SupportsICSP() && prog != devicedb.TL866A)
	if p.icsp.Disabled() {
		p.icsp.SetSelectedIndex(0)
	}
	p.refreshPinCheck()

	// Rebuild the voltage section with the values valid for this programmer.
	var vppVals []string
	switch prog {
	case devicedb.TL866A:
		vppVals = []string{"10", "12.5", "13.5", "14", "15.5", "16", "17", "18", "21"}
	case devicedb.TL866II:
		vppVals = []string{"9", "9.5", "10", "11", "11.5", "12", "12.5", "13", "13.5", "14", "14.5", "15.5", "16", "16.5", "17", "18"}
	default:
		vppVals = []string{"9", "9.5", "10", "11", "11.5", "12", "12.5", "13", "13.5", "14", "14.5", "15.5", "16", "16.5", "17", "18", "21", "25"}
	}
	resetSelect(p.vpp, withDefault(vppVals...))
	if d != nil && d.Type == devicedb.TypeLogic {
		resetSelect(p.vcc, withDefault("1.8", "2.5", "3.3", "5"))
	} else {
		resetSelect(p.vcc, withDefault("3.3", "4", "4.5", "5", "5.5", "6.5"))
	}
	var clocks []string
	switch prog {
	case devicedb.T48:
		clocks = []string{"4", "8", "15", "30"}
	case devicedb.T56:
		clocks = []string{"8", "16", "25", "50"}
	case devicedb.T76:
		if d != nil && d.ProtocolID&0xff == 0x03 || d != nil && d.ProtocolID&0xff == 0x0f {
			clocks = []string{"4", "8", "16", "24", "30", "40", "50", "60"}
		} else {
			clocks = []string{"0.5", "1", "2", "3", "4"}
		}
	}
	resetSelect(p.spiClock, withDefault(clocks...))

	p.voltBox.Objects = nil
	row := func(label string, w fyne.CanvasObject) {
		p.voltBox.Objects = append(p.voltBox.Objects, widget.NewLabel(label), w)
	}
	if d == nil || d.CanAdjustVpp() || d.Type == devicedb.TypePLD {
		row("VPP (V)", p.vpp)
		row("Pulse delay", p.pulse)
	}
	if d == nil || d.CanAdjustVcc() || d.CanAdjustVpp() || d.Type == devicedb.TypeLogic || d.Type == devicedb.TypePLD {
		row("VCC (V)", p.vcc)
	}
	if d == nil || d.CanAdjustVcc() || d.CanAdjustVpp() {
		row("VDD (V)", p.vdd)
	}
	if len(clocks) > 0 && (d == nil || d.IsSPIFlash()) {
		row("SPI clock (MHz)", p.spiClock)
	}
	if prog == devicedb.T48 || prog == devicedb.T56 || prog == devicedb.T76 {
		row("Over-current limit", p.ovc)
	}
	if prog == devicedb.T76 && d != nil && d.IsI2C() {
		row("I²C address", p.i2cAddr)
	}
	if prog == devicedb.T76 && d != nil && d.Type == devicedb.TypeEMMC {
		row("eMMC partition", p.partition)
	}
	if len(p.voltBox.Objects) == 0 {
		row("", widget.NewLabel("No adjustable parameters"))
	}
	p.voltBox.Refresh()
}

func resetSelect(s *widget.Select, opts []string) {
	cur := s.Selected
	s.Options = opts
	s.ClearSelected()
	for _, o := range opts {
		if o == cur {
			s.SetSelected(cur)
			return
		}
	}
	s.SetSelected(defaultChoice)
}

func choice(s *widget.Select) string {
	if s.Selected == "" || s.Selected == defaultChoice {
		return ""
	}
	v := s.Selected
	for i, c := range v {
		if c == ' ' {
			return v[:i]
		}
	}
	return v
}

// options converts the panel state into minipro options.
func (p *optionsPanel) options() minipro.Options {
	a := p.a
	o := minipro.Options{
		SkipErase:     !p.erase.Checked && !p.erase.Disabled(),
		SkipVerify:    !p.verify.Checked,
		SkipID:        !p.checkID.Checked && !p.checkID.Disabled(),
		NoIDError:     p.ignoreID.Checked && !p.ignoreID.Disabled(),
		PinCheck:      p.pinCheck.Checked && !p.pinCheck.Disabled(),
		Unprotect:     p.unprotect.Checked && !p.unprotect.Disabled(),
		Protect:       p.protect.Checked && !p.protect.Disabled(),
		SkipBlank:     p.skipBlank.Checked,
		NoSizeError:   p.sizeMode.Selected != "Error",
		NoSizeWarning: p.sizeMode.Selected == "Ignore",
		ICSP:          p.icsp.SelectedIndex(),
		VPP:           choice(p.vpp),
		VDD:           choice(p.vdd),
		VCC:           choice(p.vcc),
		SPIClock:      choice(p.spiClock),
		OVCLevel:      choice(p.ovc),
		Pulse:         p.pulse.Text,
		I2CAddress:    p.i2cAddr.Text,
		AlgoPath:      a.prefs.String("algoPath"),
	}
	if o.ICSP < 0 {
		o.ICSP = 0
	}
	if a.device != nil {
		o.Device = a.device.Name
		if a.programmer == devicedb.T76 && a.device.Type == devicedb.TypeEMMC && p.partition.Selected != "user" {
			o.Partition = p.partition.Selected
		}
	}
	return o
}

// refreshPinCheck enables the pin check option only where minipro can run
// it; passing -z otherwise would make minipro abort the operation.
func (p *optionsPanel) refreshPinCheck() {
	if ok, _ := p.a.pinCheckSupport(); ok {
		p.pinCheck.Enable()
	} else {
		p.pinCheck.Disable()
	}
}
