package buffer

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	for _, size := range []int{1, 15, 300, 0x10000, 0x23456, 0x1000010} {
		data := make([]byte, size)
		rand.New(rand.NewSource(int64(size))).Read(data)
		for _, f := range []Format{FormatIntelHex, FormatSRecord} {
			var w bytes.Buffer
			var err error
			if f == FormatIntelHex {
				err = EncodeIHex(&w, data, 0)
			} else {
				err = EncodeSRec(&w, data, 0)
			}
			if err != nil {
				t.Fatal(err)
			}
			if Detect(w.Bytes()) != f {
				t.Fatalf("%v size %d: detect failed", f, size)
			}
			img, err := Decode(w.Bytes(), f, 0xff)
			if err != nil {
				t.Fatalf("%v size %d: %v", f, size, err)
			}
			if !bytes.Equal(img.Data, data) {
				t.Fatalf("%v size %d: mismatch (got %d bytes)", f, size, len(img.Data))
			}
		}
	}
}

func TestOps(t *testing.T) {
	b := New(16, 0xff)
	if !b.IsBlank() {
		t.Fatal("not blank")
	}
	b.Fill(2, 6, []byte{0xAA, 0x55})
	if b.Find([]byte{0x55, 0xAA}, 0) != 3 {
		t.Fatal("find")
	}
	b.SwapBytes(2, 6, 2)
	if b.At(2) != 0x55 || b.At(3) != 0xAA {
		t.Fatal("swap")
	}
	if c := Compute([]byte("123456789")); c.CRC16 != 0xBB3D || c.CRC32 != 0xCBF43926 {
		t.Fatalf("crc %+v", c)
	}
	s, e, ok := b.UsedRange()
	if !ok || s != 2 || e != 6 {
		t.Fatal("used range", s, e)
	}
}
