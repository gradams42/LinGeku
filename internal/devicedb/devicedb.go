// Package devicedb reads minipro's infoic.xml and logicic.xml so the GUI can
// browse, search and describe devices without shelling out for every lookup.
package devicedb

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Programmer identifies a family of XGecu programmers.
type Programmer string

const (
	TL866A  Programmer = "tl866a"
	TL866II Programmer = "tl866ii"
	T48     Programmer = "t48"
	T56     Programmer = "t56"
	T76     Programmer = "t76"
)

// Programmers lists every programmer minipro can drive, in display order.
var Programmers = []Programmer{TL866II, T48, T56, T76, TL866A}

// Label is the human readable name of the programmer.
func (p Programmer) Label() string {
	switch p {
	case TL866A:
		return "TL866A/CS"
	case TL866II:
		return "TL866II+"
	case T48:
		return "T48"
	case T56:
		return "T56"
	case T76:
		return "T76"
	}
	return string(p)
}

// database returns the infoic.xml <database type> holding this programmer's chips.
func (p Programmer) database() string {
	switch p {
	case TL866A:
		return "INFOIC"
	case T76:
		return "INFOICT76"
	}
	return "INFOIC2PLUS"
}

// Chip type values from the "type" attribute.
const (
	TypeMemory = 1
	TypeMCU    = 2
	TypePLD    = 3
	TypeSRAM   = 4
	TypeLogic  = 5
	TypeNAND   = 6
	TypeEMMC   = 7
	TypeVGA    = 8
)

// TypeNames maps chip types to display names.
var TypeNames = map[int]string{
	TypeMemory: "EEPROM/Flash",
	TypeMCU:    "MCU/MPU",
	TypePLD:    "PLD/CPLD",
	TypeSRAM:   "SRAM",
	TypeLogic:  "Logic IC",
	TypeNAND:   "NAND",
	TypeEMMC:   "eMMC",
	TypeVGA:    "VGA/HDMI",
}

// Flag bits, mirrored from minipro's database.c.
const (
	flagReversedPackage = 0x00000002
	flagErase           = 0x00000010
	flagID              = 0x00000020
	flagDataAddress     = 0x00001000
	flagBusWidth16      = 0x00002000
	flagOffProtect      = 0x00004000
	flagProtectAfter    = 0x00008000
	flagLockWriteOnly   = 0x00040000
	flagCalibration     = 0x00080000
	flagProgSupport     = 0x00300000

	pkgSMD     = 0x80000000
	pkgICSP    = 0x0000ff00
	pkgAdapter = 0x000000ff
	pkgPins    = 0x3f000000

	pinMapT56Only     = 0x10000000
	pinMapTL866IIOnly = 0x20000000
	pinMapT48Only     = 0x40000000

	customProtocol = 0x80000000
	chipInfoVcc    = 0x0006
	chipInfoVpp    = 0x0007
)

// Device describes one chip entry.
type Device struct {
	Name           string
	Manufacturer   string
	Custom         bool
	Type           int
	ProtocolID     uint32
	Variant        uint32
	ReadBuffer     uint32
	WriteBuffer    uint32
	CodeSize       uint32
	DataSize       uint32
	Data2Size      uint32
	PageSize       uint32
	PagesPerBlock  uint32
	ChipID         uint32
	Voltages       uint32
	PulseDelay     uint32
	Flags          uint32
	ChipInfo       uint32
	PinMap         uint32
	PackageDetails uint32
	ConfigName     string
	BlankValue     uint8
	// Logic IC test data (only for Type == TypeLogic).
	LogicVoltage string
	LogicPins    int
	Vectors      []string
}

// TypeName returns a display name for the chip type.
func (d *Device) TypeName() string {
	if n, ok := TypeNames[d.Type]; ok {
		return n
	}
	return fmt.Sprintf("Type %d", d.Type)
}

// BaseName is the part name without the "@package" suffix.
func (d *Device) BaseName() string {
	if i := strings.IndexByte(d.Name, '@'); i >= 0 {
		return d.Name[:i]
	}
	return d.Name
}

// PinCount returns the package pin count.
func (d *Device) PinCount() int {
	if d.Type == TypeLogic {
		return d.LogicPins
	}
	switch v := (d.PackageDetails & pkgPins) >> 24; v {
	case 0x38:
		return 20
	case 0x3e:
		return 28
	case 0x3f:
		return 32
	case 0x3d:
		return 44
	default:
		return int(v)
	}
}

func (d *Device) isPLCC() bool {
	switch (d.PackageDetails & pkgPins) >> 24 {
	case 0x38, 0x3e, 0x3f, 0x3d:
		return true
	}
	return false
}

// Adapter returns the adapter number required (0 for none).
func (d *Device) Adapter() int { return int(d.PackageDetails & pkgAdapter) }

// ICSPConnector returns the ICSP connection diagram number (0 for none).
func (d *Device) ICSPConnector() int { return int(d.PackageDetails&pkgICSP) >> 8 }

// SMD reports whether the chip is a surface-mount part.
func (d *Device) SMD() bool { return d.PackageDetails&pkgSMD != 0 }

// Package returns a human readable package description.
func (d *Device) Package() string {
	if i := strings.IndexByte(d.Name, '@'); i >= 0 {
		return d.Name[i+1:]
	}
	if d.Type == TypeLogic {
		return fmt.Sprintf("DIP%d", d.LogicPins)
	}
	if n := d.PinCount(); n > 0 {
		if d.isPLCC() {
			return fmt.Sprintf("PLCC%d", n)
		}
		return fmt.Sprintf("DIP%d", n)
	}
	return "ICSP only"
}

// SocketInfo describes how to put the chip into the programmer.
func (d *Device) SocketInfo() string {
	var parts []string
	if a := d.Adapter(); a != 0 {
		parts = append(parts, fmt.Sprintf("Requires adapter #%03d", a))
	} else if n := d.PinCount(); n > 0 {
		parts = append(parts, fmt.Sprintf("Insert directly in ZIF socket (%d pins, pin 1 at top-left)", n))
	}
	if c := d.ICSPConnector(); c != 0 {
		parts = append(parts, fmt.Sprintf("ICSP diagram ICP%03d", c))
	}
	if len(parts) == 0 {
		return "ICSP only"
	}
	return strings.Join(parts, "; ")
}

// CanErase reports whether the chip supports an erase operation.
func (d *Device) CanErase() bool { return d.Flags&flagErase != 0 }

// HasChipID reports whether the chip has a readable ID.
func (d *Device) HasChipID() bool { return d.Flags&flagID != 0 && d.ChipID != 0 }

// ChipIDString formats the expected chip ID.
func (d *Device) ChipIDString() string {
	if d.ChipID == 0 {
		return "—"
	}
	n := 4
	for n > 1 && d.ChipID>>(uint(n-1)*8) == 0 {
		n--
	}
	return fmt.Sprintf("0x%0*X", n*2, d.ChipID)
}

// WordSize is the size in bytes of one memory word.
func (d *Device) WordSize() int {
	if d.Flags&flagBusWidth16 != 0 {
		return 2
	}
	return 1
}

// CodeBytes returns the code memory size in bytes.
func (d *Device) CodeBytes() int {
	if d.Type == TypePLD {
		return int(d.CodeSize) // fuse count, stored as bits
	}
	return int(d.CodeSize) * d.WordSize()
}

// DataBytes returns the data (EEPROM) memory size in bytes.
func (d *Device) DataBytes() int { return int(d.DataSize) }

// HasData reports whether the chip has a separate data/EEPROM area.
func (d *Device) HasData() bool { return d.DataSize > 0 && d.Type == TypeMCU }

// HasConfig reports whether the chip has fuses/locks/config words.
func (d *Device) HasConfig() bool { return d.ConfigName != "" || d.Type == TypePLD }

// HasCalibration reports AVR style calibration bytes.
func (d *Device) HasCalibration() bool { return d.Flags&flagCalibration != 0 }

// ProtectBefore reports whether the chip supports unprotect before write.
func (d *Device) ProtectBefore() bool { return d.Flags&flagOffProtect != 0 }

// ProtectAfter reports whether the chip supports protect after write.
func (d *Device) ProtectAfter() bool { return d.Flags&flagProtectAfter != 0 }

// ReadOnly reports whether the device can only be read.
func (d *Device) ReadOnly() bool {
	if d.ProtocolID&customProtocol != 0 && d.ProtocolID&0xff == 0x01 {
		return true
	}
	return false
}

// ICSPMode describes in-circuit programming support.
func (d *Device) ICSPMode() string {
	switch (d.Flags & flagProgSupport) >> 20 {
	case 1:
		return "ZIF + ICSP"
	case 2:
		return "ICSP only"
	}
	return "ZIF only"
}

// SupportsICSP reports whether ICSP can be used.
func (d *Device) SupportsICSP() bool { return (d.Flags&flagProgSupport)>>20 != 0 }

// CanAdjustVcc reports whether VCC/VDD can be overridden.
func (d *Device) CanAdjustVcc() bool { return d.ChipInfo == chipInfoVcc }

// CanAdjustVpp reports whether VPP and pulse can be overridden.
func (d *Device) CanAdjustVpp() bool { return d.ChipInfo == chipInfoVpp }

// IsSPIFlash reports whether the SPI clock is adjustable (25xx / AT45).
func (d *Device) IsSPIFlash() bool {
	p := d.ProtocolID & 0xff
	return d.ProtocolID&customProtocol == 0 && (p == 0x03 || p == 0x0f || p == 0x04)
}

// IsI2C reports 24Cxx devices (I2C address adjustable on T76).
func (d *Device) IsI2C() bool { return d.ProtocolID&customProtocol == 0 && d.ProtocolID&0xff == 0x01 }

// DefaultVoltages returns the nominal VPP/VCC/VDD codes as display strings.
func (d *Device) DefaultVoltages() (vpp, vcc, vdd string) {
	v := d.Voltages
	return fmt.Sprintf("0x%02X", v&0xff), fmt.Sprintf("0x%X", (v>>8)&0xf), fmt.Sprintf("0x%X", (v>>12)&0xf)
}

// AvailableOn reports whether the chip is supported by the given programmer.
func (d *Device) AvailableOn(p Programmer) bool {
	if p.database() != "INFOIC2PLUS" {
		return true
	}
	only := d.PinMap & (pinMapT56Only | pinMapT48Only | pinMapTL866IIOnly)
	if only == 0 {
		return true
	}
	switch p {
	case TL866II:
		return d.PinMap&pinMapTL866IIOnly != 0
	case T48:
		return d.PinMap&pinMapT48Only != 0
	case T56:
		return d.PinMap&pinMapT56Only != 0
	}
	return true
}

// Setting is one fuse or lock entry from the configuration section.
type Setting struct {
	Name    string
	Mask    uint16
	Default uint16
}

// Config holds fuse, lock and user-ID metadata for a device family.
type Config struct {
	Name    string
	NumUIDs int
	Fuses   []Setting
	Locks   []Setting
}

// DB is a loaded device database.
type DB struct {
	Path    string
	byDB    map[string][]*Device
	configs map[string]*Config
	Logic   []*Device
}

// Load parses infoic.xml (and logicic.xml if logicPath is not empty).
func Load(infoicPath, logicPath string) (*DB, error) {
	db := &DB{Path: infoicPath, byDB: map[string][]*Device{}, configs: map[string]*Config{}}
	data, err := readStripped(infoicPath)
	if err != nil {
		return nil, err
	}
	if err := db.parseInfoic(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("%s: %w", infoicPath, err)
	}
	if logicPath != "" {
		if data, err := readStripped(logicPath); err == nil {
			if err := db.parseLogic(bytes.NewReader(data)); err != nil {
				return nil, fmt.Errorf("%s: %w", logicPath, err)
			}
		}
	}
	return db, nil
}

// readStripped reads an XML file and removes its comments. minipro's
// databases contain comments with "--" inside, which encoding/xml rejects.
func readStripped(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(data))
	for {
		i := bytes.Index(data, []byte("<!--"))
		if i < 0 {
			return append(out, data...), nil
		}
		out = append(out, data[:i]...)
		j := bytes.Index(data[i+4:], []byte("-->"))
		if j < 0 {
			return out, nil
		}
		data = data[i+4+j+3:]
	}
}

func attrs(se xml.StartElement) map[string]string {
	m := make(map[string]string, len(se.Attr))
	for _, a := range se.Attr {
		m[a.Name.Local] = strings.TrimSpace(a.Value)
	}
	return m
}

func num(s string) uint32 {
	v, _ := strconv.ParseUint(strings.TrimSpace(s), 0, 64)
	return uint32(v)
}

func (db *DB) parseInfoic(r io.Reader) error {
	dec := xml.NewDecoder(r)
	var curDB, curMfr string
	var custom bool
	var cfg *Config
	var section string // "fuses" or "locks"
	var settingName string
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			a := attrs(t)
			switch t.Name.Local {
			case "database":
				curDB = a["type"]
			case "manufacturer":
				curMfr, custom = a["name"], false
			case "custom":
				curMfr, custom = a["name"], true
			case "ic":
				d := &Device{
					Name: a["name"], Manufacturer: curMfr, Custom: custom,
					Type:           int(num(a["type"])),
					ProtocolID:     num(a["protocol_id"]),
					Variant:        num(a["variant"]),
					ReadBuffer:     num(a["read_buffer_size"]),
					WriteBuffer:    num(a["write_buffer_size"]),
					CodeSize:       num(a["code_memory_size"]),
					DataSize:       num(a["data_memory_size"]),
					Data2Size:      num(a["data_memory2_size"]),
					PageSize:       num(a["page_size"]),
					PagesPerBlock:  num(a["pages_per_block"]),
					ChipID:         num(a["chip_id"]),
					Voltages:       num(a["voltages"]),
					PulseDelay:     num(a["pulse_delay"]),
					Flags:          num(a["flags"]),
					ChipInfo:       num(a["chip_info"]),
					PinMap:         num(a["pin_map"]),
					PackageDetails: num(a["package_details"]),
					BlankValue:     0xff,
				}
				if bv, ok := a["blank_value"]; ok {
					d.BlankValue = uint8(num(bv))
				}
				if c := a["config"]; !strings.EqualFold(c, "null") {
					d.ConfigName = c
				}
				// One entry may list several aliases: "AT28C256,AT28C256E,...".
				for _, name := range strings.Split(d.Name, ",") {
					if name = strings.TrimSpace(name); name == "" || curDB == "" {
						continue
					}
					alias := *d
					alias.Name = name
					db.byDB[curDB] = append(db.byDB[curDB], &alias)
				}
			case "config":
				cfg = &Config{Name: a["name"], NumUIDs: int(num(a["num_uids"]))}
				db.configs[cfg.Name] = cfg
			case "fuses", "locks":
				section = t.Name.Local
			case "fuse", "lock":
				settingName = a["name"]
				text.Reset()
			}
		case xml.CharData:
			if settingName != "" {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "fuse", "lock":
				if cfg != nil && settingName != "" {
					parts := strings.Split(text.String(), ",")
					s := Setting{Name: settingName}
					if len(parts) > 0 {
						s.Mask = uint16(num(parts[0]))
					}
					if len(parts) > 1 {
						s.Default = uint16(num(parts[1]))
					}
					if section == "locks" {
						cfg.Locks = append(cfg.Locks, s)
					} else {
						cfg.Fuses = append(cfg.Fuses, s)
					}
				}
				settingName = ""
			case "config":
				cfg = nil
			case "database":
				curDB = ""
			}
		}
	}
	return nil
}

func (db *DB) parseLogic(r io.Reader) error {
	dec := xml.NewDecoder(r)
	var mfr string
	var cur *Device
	var text strings.Builder
	inVector := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			a := attrs(t)
			switch t.Name.Local {
			case "manufacturer", "custom":
				mfr = a["name"]
			case "ic":
				pins, _ := strconv.Atoi(a["pins"])
				cur = &Device{Name: a["name"], Manufacturer: mfr, Type: TypeLogic,
					LogicVoltage: a["voltage"], LogicPins: pins}
				db.Logic = append(db.Logic, cur)
			case "vector":
				inVector = true
				text.Reset()
			}
		case xml.CharData:
			if inVector {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "vector":
				if cur != nil {
					cur.Vectors = append(cur.Vectors, strings.Join(strings.Fields(text.String()), " "))
				}
				inVector = false
			case "ic":
				// Expand "40106,7416,7414,..." into one entry per alias.
				if cur != nil && strings.Contains(cur.Name, ",") {
					db.Logic = db.Logic[:len(db.Logic)-1]
					for _, name := range strings.Split(cur.Name, ",") {
						if name = strings.TrimSpace(name); name != "" {
							alias := *cur
							alias.Name = name
							db.Logic = append(db.Logic, &alias)
						}
					}
				}
				cur = nil
			}
		}
	}
	return nil
}

// Devices returns all chips usable on a programmer (memory chips plus logic ICs).
func (db *DB) Devices(p Programmer) []*Device {
	src := db.byDB[p.database()]
	out := make([]*Device, 0, len(src)+len(db.Logic))
	for _, d := range src {
		if d.AvailableOn(p) {
			out = append(out, d)
		}
	}
	out = append(out, db.Logic...)
	return out
}

// Find looks up a device by exact (case-insensitive) name.
func (db *DB) Find(p Programmer, name string) *Device {
	for _, d := range db.Devices(p) {
		if strings.EqualFold(d.Name, name) {
			return d
		}
	}
	return nil
}

// FindByID returns devices whose chip ID matches.
func (db *DB) FindByID(p Programmer, id uint32) []*Device {
	var out []*Device
	for _, d := range db.Devices(p) {
		if d.ChipID == id && id != 0 {
			out = append(out, d)
		}
	}
	return out
}

// Manufacturers returns the sorted list of manufacturers for a programmer.
func (db *DB) Manufacturers(p Programmer) []string {
	seen := map[string]bool{}
	for _, d := range db.Devices(p) {
		seen[d.Manufacturer] = true
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// Config returns the fuse/lock configuration for a device, or nil.
func (db *DB) Config(d *Device) *Config {
	if d == nil || d.ConfigName == "" {
		return nil
	}
	return db.configs[d.ConfigName]
}

// Filter selects devices by search text, manufacturer and type (0 = any type).
func Filter(devs []*Device, search, mfr string, typ int, smdOnly, dipOnly bool) []*Device {
	terms := strings.Fields(strings.ToLower(search))
	var out []*Device
	for _, d := range devs {
		if mfr != "" && d.Manufacturer != mfr {
			continue
		}
		if typ != 0 && d.Type != typ {
			continue
		}
		if smdOnly && !d.SMD() {
			continue
		}
		if dipOnly && d.SMD() {
			continue
		}
		name := strings.ToLower(d.Name)
		ok := true
		for _, t := range terms {
			if !strings.Contains(name, t) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, d)
		}
	}
	return out
}
