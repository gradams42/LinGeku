// Package buffer holds the in-memory image edited in the hex editor, plus the
// buffer operations XGecu offers (fill, find, checksum, byte swap, compare).
package buffer

import (
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"sync"
)

// Buffer is a resizable byte image with change tracking. It is safe for use
// from the UI goroutine and from background operations.
type Buffer struct {
	mu       sync.RWMutex
	data     []byte
	blank    byte
	modified bool
	onChange []func()
}

// New creates a buffer of size bytes filled with the blank value.
func New(size int, blank byte) *Buffer {
	b := &Buffer{blank: blank}
	b.data = bytes.Repeat([]byte{blank}, size)
	return b
}

// OnChange registers a callback run after every mutation.
func (b *Buffer) OnChange(f func()) {
	b.mu.Lock()
	b.onChange = append(b.onChange, f)
	b.mu.Unlock()
}

func (b *Buffer) changed() {
	b.mu.RLock()
	cbs := append([]func(){}, b.onChange...)
	b.mu.RUnlock()
	for _, f := range cbs {
		f()
	}
}

// Len returns the buffer size.
func (b *Buffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.data)
}

// Blank returns the erased-state byte value.
func (b *Buffer) Blank() byte { return b.blank }

// Modified reports whether the buffer has unsaved edits.
func (b *Buffer) Modified() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.modified
}

// SetModified sets the dirty flag.
func (b *Buffer) SetModified(m bool) {
	b.mu.Lock()
	b.modified = m
	b.mu.Unlock()
}

// Bytes returns a copy of the buffer contents.
func (b *Buffer) Bytes() []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]byte(nil), b.data...)
}

// Slice copies up to n bytes starting at off into a new slice.
func (b *Buffer) Slice(off, n int) []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if off >= len(b.data) || off < 0 {
		return nil
	}
	end := min(off+n, len(b.data))
	return append([]byte(nil), b.data[off:end]...)
}

// At returns the byte at off.
func (b *Buffer) At(off int) byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if off < 0 || off >= len(b.data) {
		return 0
	}
	return b.data[off]
}

// Set writes one byte.
func (b *Buffer) Set(off int, v byte) {
	b.mu.Lock()
	if off >= 0 && off < len(b.data) && b.data[off] != v {
		b.data[off] = v
		b.modified = true
	}
	b.mu.Unlock()
	b.changed()
}

// Load replaces the buffer contents. If the data is shorter than the buffer
// the remainder is filled with the blank value; the buffer grows if needed.
func (b *Buffer) Load(data []byte, keepSize bool) {
	b.mu.Lock()
	size := len(b.data)
	if !keepSize || len(data) > size {
		size = len(data)
	}
	nd := bytes.Repeat([]byte{b.blank}, size)
	copy(nd, data)
	b.data = nd
	b.modified = false
	b.mu.Unlock()
	b.changed()
}

// Write copies data into the buffer at off, growing it if necessary.
func (b *Buffer) Write(off int, data []byte) {
	b.mu.Lock()
	if need := off + len(data); need > len(b.data) {
		b.data = append(b.data, bytes.Repeat([]byte{b.blank}, need-len(b.data))...)
	}
	copy(b.data[off:], data)
	b.modified = true
	b.mu.Unlock()
	b.changed()
}

// Resize changes the buffer size, padding with the blank value.
func (b *Buffer) Resize(size int, blank byte) {
	b.mu.Lock()
	b.blank = blank
	if size < len(b.data) {
		b.data = b.data[:size]
	} else {
		b.data = append(b.data, bytes.Repeat([]byte{blank}, size-len(b.data))...)
	}
	b.mu.Unlock()
	b.changed()
}

func (b *Buffer) checkRange(start, end int) error {
	if start < 0 || end > len(b.data) || start > end {
		return fmt.Errorf("range 0x%X-0x%X is outside the buffer (size 0x%X)", start, end, len(b.data))
	}
	return nil
}

// Fill writes a repeating pattern over [start, end).
func (b *Buffer) Fill(start, end int, pattern []byte) error {
	if len(pattern) == 0 {
		return errors.New("empty fill pattern")
	}
	b.mu.Lock()
	if err := b.checkRange(start, end); err != nil {
		b.mu.Unlock()
		return err
	}
	for i := start; i < end; i++ {
		b.data[i] = pattern[(i-start)%len(pattern)]
	}
	b.modified = true
	b.mu.Unlock()
	b.changed()
	return nil
}

// FillRandom fills [start, end) with pseudo random bytes.
func (b *Buffer) FillRandom(start, end int, rnd func([]byte)) error {
	b.mu.Lock()
	if err := b.checkRange(start, end); err != nil {
		b.mu.Unlock()
		return err
	}
	rnd(b.data[start:end])
	b.modified = true
	b.mu.Unlock()
	b.changed()
	return nil
}

// Copy copies n bytes from src to dst inside the buffer.
func (b *Buffer) Copy(src, dst, n int) error {
	b.mu.Lock()
	if err := b.checkRange(src, src+n); err != nil {
		b.mu.Unlock()
		return err
	}
	if err := b.checkRange(dst, dst+n); err != nil {
		b.mu.Unlock()
		return err
	}
	copy(b.data[dst:dst+n], b.data[src:src+n])
	b.modified = true
	b.mu.Unlock()
	b.changed()
	return nil
}

// SwapBytes swaps adjacent byte pairs (16-bit endianness) or reverses each
// group of width bytes (width 4 swaps 32-bit words).
func (b *Buffer) SwapBytes(start, end, width int) error {
	if width < 2 {
		return errors.New("swap width must be at least 2")
	}
	b.mu.Lock()
	if err := b.checkRange(start, end); err != nil {
		b.mu.Unlock()
		return err
	}
	for i := start; i+width <= end; i += width {
		w := b.data[i : i+width]
		for l, r := 0, width-1; l < r; l, r = l+1, r-1 {
			w[l], w[r] = w[r], w[l]
		}
	}
	b.modified = true
	b.mu.Unlock()
	b.changed()
	return nil
}

// Invert XORs every byte in [start, end) with 0xFF.
func (b *Buffer) Invert(start, end int) error {
	b.mu.Lock()
	if err := b.checkRange(start, end); err != nil {
		b.mu.Unlock()
		return err
	}
	for i := start; i < end; i++ {
		b.data[i] ^= 0xff
	}
	b.modified = true
	b.mu.Unlock()
	b.changed()
	return nil
}

// Find searches for needle starting at from; it wraps around once.
// It returns -1 when not found.
func (b *Buffer) Find(needle []byte, from int) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(needle) == 0 || len(b.data) == 0 {
		return -1
	}
	if from < 0 || from >= len(b.data) {
		from = 0
	}
	if i := bytes.Index(b.data[from:], needle); i >= 0 {
		return from + i
	}
	if i := bytes.Index(b.data[:min(len(b.data), from+len(needle)-1)], needle); i >= 0 {
		return i
	}
	return -1
}

// Replace replaces every occurrence of old with repl (same length required)
// and returns the number of replacements.
func (b *Buffer) Replace(old, repl []byte) (int, error) {
	if len(old) == 0 || len(old) != len(repl) {
		return 0, errors.New("search and replacement must be non-empty and the same length")
	}
	b.mu.Lock()
	n := 0
	for i := 0; i+len(old) <= len(b.data); {
		j := bytes.Index(b.data[i:], old)
		if j < 0 {
			break
		}
		copy(b.data[i+j:], repl)
		i += j + len(old)
		n++
	}
	if n > 0 {
		b.modified = true
	}
	b.mu.Unlock()
	if n > 0 {
		b.changed()
	}
	return n, nil
}

// IsBlank reports whether every byte equals the blank value.
func (b *Buffer) IsBlank() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, v := range b.data {
		if v != b.blank {
			return false
		}
	}
	return true
}

// UsedRange returns the extent of non-blank data ([start, end)); ok is false
// when the buffer is entirely blank.
func (b *Buffer) UsedRange() (start, end int, ok bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	start, end = -1, -1
	for i, v := range b.data {
		if v != b.blank {
			if start < 0 {
				start = i
			}
			end = i + 1
		}
	}
	return start, end, start >= 0
}

// Checksums is the set of checksums the XGecu software shows.
type Checksums struct {
	Sum8   uint8
	Sum16  uint16
	Sum32  uint32
	Word16 uint16 // sum of little-endian 16-bit words
	CRC16  uint16 // CRC-16/ARC
	CRC32  uint32
	XOR8   uint8
}

// Checksum computes checksums over [start, end).
func (b *Buffer) Checksum(start, end int) (Checksums, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if err := b.checkRange(start, end); err != nil {
		return Checksums{}, err
	}
	return Compute(b.data[start:end]), nil
}

// Compute computes checksums over data.
func Compute(data []byte) Checksums {
	var c Checksums
	var sum uint32
	var x uint8
	var crc16 uint16
	for i, v := range data {
		sum += uint32(v)
		x ^= v
		if i%2 == 0 {
			w := uint16(v)
			if i+1 < len(data) {
				w |= uint16(data[i+1]) << 8
			}
			c.Word16 += w
		}
		crc16 ^= uint16(v)
		for k := 0; k < 8; k++ {
			if crc16&1 != 0 {
				crc16 = crc16>>1 ^ 0xA001
			} else {
				crc16 >>= 1
			}
		}
	}
	c.Sum8, c.Sum16, c.Sum32, c.XOR8, c.CRC16 = uint8(sum), uint16(sum), sum, x, crc16
	c.CRC32 = crc32.ChecksumIEEE(data)
	return c
}

// Diff is one differing byte between two images.
type Diff struct {
	Offset int
	A, B   byte
}

// Compare returns up to limit differences between a and b. Bytes past the end
// of the shorter slice are compared against blank.
func Compare(a, b []byte, blank byte, limit int) (diffs []Diff, total int) {
	n := max(len(a), len(b))
	for i := 0; i < n; i++ {
		x, y := blank, blank
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			total++
			if len(diffs) < limit {
				diffs = append(diffs, Diff{i, x, y})
			}
		}
	}
	return diffs, total
}
