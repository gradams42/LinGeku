package buffer

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Format is a file format for buffer images.
type Format int

const (
	FormatAuto Format = iota
	FormatBinary
	FormatIntelHex
	FormatSRecord
	FormatJEDEC
)

// String returns the format's display name.
func (f Format) String() string {
	switch f {
	case FormatBinary:
		return "Binary"
	case FormatIntelHex:
		return "Intel HEX"
	case FormatSRecord:
		return "Motorola S-Record"
	case FormatJEDEC:
		return "JEDEC (.jed)"
	}
	return "Auto"
}

// MiniproFlag returns the value for minipro's -f option ("" for binary).
func (f Format) MiniproFlag() string {
	switch f {
	case FormatIntelHex:
		return "ihex"
	case FormatSRecord:
		return "srec"
	}
	return ""
}

// FormatFromExt guesses a format from a filename.
func FormatFromExt(name string) Format {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".hex", ".ihex", ".ihx", ".ihe", ".a43", ".i86":
		return FormatIntelHex
	case ".s19", ".s28", ".s37", ".srec", ".srec19", ".mot", ".s", ".sx":
		return FormatSRecord
	case ".jed":
		return FormatJEDEC
	}
	return FormatBinary
}

// Detect inspects content to decide between Intel HEX, S-Record and binary.
func Detect(data []byte) Format {
	t := bytes.TrimLeft(data, " \t\r\n")
	if len(t) == 0 {
		return FormatBinary
	}
	isText := func() bool {
		for _, c := range data[:min(len(data), 4096)] {
			if c != '\r' && c != '\n' && c != '\t' && (c < 0x20 || c > 0x7e) {
				return false
			}
		}
		return true
	}
	switch {
	case t[0] == ':' && isText():
		return FormatIntelHex
	case t[0] == 'S' && len(t) > 1 && t[1] >= '0' && t[1] <= '9' && isText():
		return FormatSRecord
	case bytes.HasPrefix(t, []byte{0x02}) || (bytes.Contains(data[:min(len(data), 2048)], []byte("*QF")) && isText()):
		return FormatJEDEC
	}
	return FormatBinary
}

// Image is decoded file content placed at absolute addresses.
type Image struct {
	Data []byte // contiguous image starting at Base
	Base int
	Used int // number of bytes actually defined by the file
}

// ReadFile loads a file in the given (or auto-detected) format.
func ReadFile(path string, f Format) (*Image, Format, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, f, err
	}
	if f == FormatAuto {
		f = Detect(raw)
	}
	img, err := Decode(raw, f, 0xff)
	return img, f, err
}

// Decode converts raw file bytes into an image.
func Decode(raw []byte, f Format, blank byte) (*Image, error) {
	switch f {
	case FormatIntelHex:
		return decodeIHex(raw, blank)
	case FormatSRecord:
		return decodeSRec(raw, blank)
	case FormatJEDEC:
		return nil, errors.New("JEDEC files are passed straight to minipro and cannot be edited as bytes")
	}
	return &Image{Data: raw, Used: len(raw)}, nil
}

type chunk struct {
	addr int
	data []byte
}

func assemble(chunks []chunk, blank byte) *Image {
	if len(chunks) == 0 {
		return &Image{}
	}
	lo, hi := chunks[0].addr, 0
	used := 0
	for _, c := range chunks {
		lo = min(lo, c.addr)
		hi = max(hi, c.addr+len(c.data))
		used += len(c.data)
	}
	// XGecu loads files at their absolute address; keep data at 0 unless the
	// whole image sits high in memory (e.g. 0x8000 for a 27C256 in a ROM map).
	base := 0
	if lo >= 0x10000 && hi-lo < lo {
		base = lo
	}
	out := bytes.Repeat([]byte{blank}, hi-base)
	for _, c := range chunks {
		copy(out[c.addr-base:], c.data)
	}
	return &Image{Data: out, Base: base, Used: used}
}

func decodeIHex(raw []byte, blank byte) (*Image, error) {
	var chunks []chunk
	upper := 0
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" {
			continue
		}
		if s[0] != ':' {
			return nil, fmt.Errorf("line %d: missing ':'", line)
		}
		rec, err := hex.DecodeString(s[1:])
		if err != nil || len(rec) < 5 {
			return nil, fmt.Errorf("line %d: malformed record", line)
		}
		n := int(rec[0])
		if len(rec) != n+5 {
			return nil, fmt.Errorf("line %d: length mismatch", line)
		}
		var sum byte
		for _, v := range rec {
			sum += v
		}
		if sum != 0 {
			return nil, fmt.Errorf("line %d: checksum error", line)
		}
		addr := int(rec[1])<<8 | int(rec[2])
		data := rec[4 : 4+n]
		switch rec[3] {
		case 0x00:
			chunks = append(chunks, chunk{upper + addr, append([]byte(nil), data...)})
		case 0x01:
			return assemble(chunks, blank), nil
		case 0x02:
			if n == 2 {
				upper = (int(data[0])<<8 | int(data[1])) << 4
			}
		case 0x04:
			if n == 2 {
				upper = (int(data[0])<<8 | int(data[1])) << 16
			}
		case 0x03, 0x05:
			// start address records: ignored
		default:
			return nil, fmt.Errorf("line %d: unknown record type %02X", line, rec[3])
		}
	}
	return assemble(chunks, blank), sc.Err()
}

func decodeSRec(raw []byte, blank byte) (*Image, error) {
	var chunks []chunk
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" {
			continue
		}
		if len(s) < 4 || s[0] != 'S' {
			return nil, fmt.Errorf("line %d: not an S-record", line)
		}
		rec, err := hex.DecodeString(s[2:])
		if err != nil || len(rec) < 1 || len(rec) != int(rec[0])+1 {
			return nil, fmt.Errorf("line %d: malformed record", line)
		}
		var sum byte
		for _, v := range rec[:len(rec)-1] {
			sum += v
		}
		if ^sum != rec[len(rec)-1] {
			return nil, fmt.Errorf("line %d: checksum error", line)
		}
		alen := map[byte]int{'1': 2, '2': 3, '3': 4}[s[1]]
		if alen == 0 {
			continue // S0 header, S5/S6 counts, S7-S9 start address
		}
		addr := 0
		for _, v := range rec[1 : 1+alen] {
			addr = addr<<8 | int(v)
		}
		chunks = append(chunks, chunk{addr, append([]byte(nil), rec[1+alen:len(rec)-1]...)})
	}
	return assemble(chunks, blank), sc.Err()
}

// WriteFile saves data in the requested format.
func WriteFile(path string, data []byte, f Format) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(file)
	switch f {
	case FormatIntelHex:
		err = EncodeIHex(w, data, 0)
	case FormatSRecord:
		err = EncodeSRec(w, data, 0)
	default:
		_, err = w.Write(data)
	}
	if err == nil {
		err = w.Flush()
	}
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	return err
}

// EncodeIHex writes Intel HEX, using extended linear address records when
// the data passes 64 KiB.
func EncodeIHex(w io.Writer, data []byte, base int) error {
	rec := func(typ byte, addr int, payload []byte) error {
		b := []byte{byte(len(payload)), byte(addr >> 8), byte(addr), typ}
		b = append(b, payload...)
		var sum byte
		for _, v := range b {
			sum += v
		}
		b = append(b, -sum)
		_, err := fmt.Fprintf(w, ":%s\r\n", strings.ToUpper(hex.EncodeToString(b)))
		return err
	}
	upper := -1
	for off := 0; off < len(data); off += 16 {
		addr := base + off
		if addr>>16 != upper {
			upper = addr >> 16
			if upper != 0 || base+len(data) > 0x10000 {
				if err := rec(0x04, 0, []byte{byte(upper >> 8), byte(upper)}); err != nil {
					return err
				}
			}
		}
		end := min(off+16, len(data))
		if err := rec(0x00, addr&0xffff, data[off:end]); err != nil {
			return err
		}
	}
	return rec(0x01, 0, nil)
}

// EncodeSRec writes Motorola S-records, picking S1/S2/S3 by address range.
func EncodeSRec(w io.Writer, data []byte, base int) error {
	top := base + len(data)
	kind, alen := byte('1'), 2
	switch {
	case top > 0xffffff:
		kind, alen = '3', 4
	case top > 0xffff:
		kind, alen = '2', 3
	}
	rec := func(t byte, al, addr int, payload []byte) error {
		b := []byte{byte(al + len(payload) + 1)}
		for i := al - 1; i >= 0; i-- {
			b = append(b, byte(addr>>(8*i)))
		}
		b = append(b, payload...)
		var sum byte
		for _, v := range b {
			sum += v
		}
		b = append(b, ^sum)
		_, err := fmt.Fprintf(w, "S%c%s\r\n", t, strings.ToUpper(hex.EncodeToString(b)))
		return err
	}
	if err := rec('0', 2, 0, []byte("LinGecu")); err != nil {
		return err
	}
	for off := 0; off < len(data); off += 16 {
		end := min(off+16, len(data))
		if err := rec(kind, alen, base+off, data[off:end]); err != nil {
			return err
		}
	}
	return rec('9'-(kind-'1'), alen, 0, nil)
}
