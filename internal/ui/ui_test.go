//go:build ci

package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"lingecu/internal/devicedb"
	"lingecu/internal/minipro"
)

// fakeMinipro emulates minipro with a file standing in for the chip.
const fakeMinipro = `#!/bin/sh
echo "$@" >> "$FAKE_DIR/args.log"
chip="$FAKE_DIR/chip.bin"
page=code
while [ $# -gt 0 ]; do
  case "$1" in
    -c) page="$2"; shift ;;
    -r) act=read; file="$2"; shift ;;
    -w) act=write; file="$2"; shift ;;
    -m) act=verify; file="$2"; shift ;;
    -D) act=id ;;
    -b) act=blank ;;
    -E) [ -z "$act" ] && act=erase ;;
    -T) act=logic ;;
    -z) pincheck=1 ;;
    -V) echo "minipro version 0.7.4     A free and open TL866 series programmer" >&2; echo "Share dir: $FAKE_DIR" >&2; exit 0 ;;
    -k) echo "t48: T48" >&2; exit 0 ;;
  esac
  shift
done
echo "Found T48 00.1.31 (0x11f)" >&2
if [ -n "$pincheck" ]; then
  if [ -f "$FAKE_DIR/badpins" ]; then
    for p in $(cat "$FAKE_DIR/badpins"); do echo "Bad contact on pin:$p" >&2; done
    exit 1
  fi
  echo "Pin test passed." >&2
fi
case "$act" in
  read)
    printf '\r\033[KReading %s...  50%%' "$page" >&2
    if [ "$page" = config ]; then
      printf 'lfuse=0x62\nhfuse=0xd9\nefuse=0xff\nlock=0xff\n' > "$file"
    elif [ "$page" = data ]; then
      head -c 1024 /dev/zero | tr '\000' '\245' > "$file"
    else
      cp "$chip" "$file"
    fi
    printf '\r\033[KReading %s...  5 ms  OK\n' "$page" >&2 ;;
  write)
    printf '\r\033[KWriting %s...  10%%' "$page" >&2
    cp "$file" "$FAKE_DIR/written_$page"
    [ "$page" = code ] && cp "$file" "$chip"
    printf '\r\033[KWriting %s...  5 ms  OK\n' "$page" >&2 ;;
  verify)
    if [ "$page" = code ] && ! cmp -s "$file" "$chip"; then echo "Verification failed at address 0x0000" >&2; exit 1; fi
    echo "Verification OK" >&2 ;;
  id) echo "Chip ID: 0x1E950F  OK" >&2 ;;
  logic) echo "Pin 1 2 3"; echo "Logic test successful." >&2 ;;
esac
exit 0
`

func waitIdle(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	time.Sleep(20 * time.Millisecond)
	for {
		a.mu.Lock()
		busy := a.busy
		a.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func logText(a *App) string { return strings.Join(a.logView.lines, "\n") }

func TestEndToEnd(t *testing.T) {
	share := devicedb.FindShareDir("../../third_party/minipro")
	if share == "" {
		t.Skip("minipro database not found; install minipro or set MINIPRO_SHARE")
	}
	infoic, logic := filepath.Join(share, "infoic.xml"), filepath.Join(share, "logicic.xml")
	dir := t.TempDir()
	t.Setenv("FAKE_DIR", dir)
	bin := filepath.Join(dir, "minipro")
	if err := os.WriteFile(bin, []byte(fakeMinipro), 0o755); err != nil {
		t.Fatal(err)
	}
	chip := bytes.Repeat([]byte("LinGecu!"), 4096) // 32 KiB
	os.WriteFile(filepath.Join(dir, "chip.bin"), chip, 0o644)

	fa := test.NewApp()
	defer fa.Quit()
	a := newApp(fa)
	defer os.RemoveAll(a.tmpDir)
	a.runner = &minipro.Runner{Binary: bin}
	db, err := devicedb.Load(infoic, logic)
	if err != nil {
		t.Fatal(err)
	}
	a.db = db
	a.setProgrammer(devicedb.T48)

	// EEPROM: read, edit, program, verify.
	a.selectDevice(db.Find(devicedb.T48, "AT28C256"))
	if a.code.Len() != 32768 {
		t.Fatalf("code buffer size %d", a.code.Len())
	}
	a.doRead()
	waitIdle(t, a)
	if !bytes.Equal(a.code.Bytes(), chip) {
		t.Fatalf("read did not load chip contents:\n%s", logText(a))
	}
	a.codeEd.Goto(0)
	a.codeEd.TypedRune('1')
	a.codeEd.TypedRune('2')
	if a.code.At(0) != 0x12 || a.codeEd.Cursor() != 1 {
		t.Fatalf("hex edit failed: %02X cursor %d", a.code.At(0), a.codeEd.Cursor())
	}
	a.doProgram()
	waitIdle(t, a)
	written, _ := os.ReadFile(filepath.Join(dir, "written_code"))
	if !bytes.Equal(written, a.code.Bytes()) {
		t.Fatalf("program wrote wrong data:\n%s", logText(a))
	}
	a.doVerify()
	waitIdle(t, a)
	if !strings.Contains(logText(a), "✓ Verify AT28C256 OK") {
		t.Fatalf("verify did not pass:\n%s", logText(a))
	}
	a.code.Set(5, 0x00)
	a.doVerify()
	waitIdle(t, a)
	if !strings.Contains(logText(a), "✗ Verify AT28C256 FAILED") {
		t.Fatalf("verify mismatch not reported:\n%s", logText(a))
	}
	if !strings.Contains(a.status.Text, "failed") {
		t.Fatalf("status %q", a.status.Text)
	}

	// MCU: code + data + config, only the first write may erase.
	os.Remove(filepath.Join(dir, "args.log"))
	a.selectDevice(db.Find(devicedb.T48, "ATMEGA328P@DIP28"))
	if len(a.cfg.keys) != 4 || a.cfg.vals["lfuse"] != "0x62" {
		t.Fatalf("fuse defaults: %v %v", a.cfg.keys, a.cfg.vals)
	}
	a.doRead()
	waitIdle(t, a)
	if a.data.At(0) != 0xA5 || a.cfg.vals["hfuse"] != "0xd9" {
		t.Fatalf("MCU read failed:\n%s", logText(a))
	}
	a.doProgram()
	waitIdle(t, a)
	args, _ := os.ReadFile(filepath.Join(dir, "args.log"))
	var writes []string
	for _, l := range strings.Split(string(args), "\n") {
		if strings.Contains(l, " -w ") {
			writes = append(writes, l)
		}
	}
	if len(writes) != 3 || strings.Contains(writes[0], " -e") || !strings.Contains(writes[1], "-c data") ||
		!strings.Contains(writes[1], " -e") || !strings.Contains(writes[2], "-c config") {
		t.Fatalf("unexpected write sequence:\n%s", strings.Join(writes, "\n"))
	}
	conf, _ := os.ReadFile(filepath.Join(dir, "written_config"))
	if !strings.Contains(string(conf), "lfuse=0x62") {
		t.Fatalf("config file: %q", conf)
	}

	// Read ID matches the expected signature.
	a.doReadID()
	waitIdle(t, a)
	if !strings.Contains(logText(a), "matches ATMEGA328P@DIP28") {
		t.Fatalf("read ID:\n%s", logText(a))
	}

	// Logic IC.
	a.selectDevice(db.Find(devicedb.T48, "7404"))
	if !strings.Contains(a.logic.title.Text, "7404") {
		t.Fatalf("logic title %q", a.logic.title.Text)
	}
	a.logic.run()
	waitIdle(t, a)
	if !strings.Contains(a.logic.result.Text, "PASS") {
		t.Fatalf("logic result %q\n%s", a.logic.result.Text, logText(a))
	}

	// Pin contact check: a bad pin aborts the read and shows in the socket view.
	a.selectDevice(db.Find(devicedb.T48, "A25L05PT@DIP8"))
	if a.opts.pinCheck.Disabled() {
		t.Fatal("pin check should be available for A25L05PT on a T48")
	}
	a.opts.pinCheck.SetChecked(true)
	os.WriteFile(filepath.Join(dir, "badpins"), []byte("3 6"), 0o644)
	a.doRead()
	waitIdle(t, a)
	z := a.pins.socket.pins
	// DIP8 in the ZIF: pins 1-4 -> ZIF 1-4, pins 5-8 -> ZIF 37-40.
	if z[2] != pinBad || z[37] != pinBad || z[0] != pinGood || z[3] != pinGround || z[39] != pinGood {
		t.Fatalf("socket states %v\n%s", z, logText(a))
	}
	if !strings.Contains(a.pins.status.Text, "3, 6") || a.tabs.Selected() != a.tabPins {
		t.Fatalf("pin status %q", a.pins.status.Text)
	}
	os.Remove(filepath.Join(dir, "badpins"))
	a.doPinCheck()
	waitIdle(t, a)
	if a.pins.socket.pins[2] != pinGood || !strings.Contains(a.pins.status.Text, "All 7 tested pins") {
		t.Fatalf("after reseat: %v %q", a.pins.socket.pins, a.pins.status.Text)
	}
	// Not offered where minipro can't test (T76) and never passed as -z.
	a.setProgrammer(devicedb.T76)
	a.selectDevice(db.Find(devicedb.T76, "A25L05PT@DIP8"))
	if !a.opts.pinCheck.Disabled() || a.opts.options().PinCheck {
		t.Fatal("pin check must be disabled on the T76")
	}
	a.setProgrammer(devicedb.T48)

	// Project round trip.
	a.selectDevice(db.Find(devicedb.T48, "AT28C256"))
	a.code.Fill(0, 16, []byte{0xCA, 0xFE})
	state := a.optionState()
	if state["verify"] != "1" {
		t.Fatalf("option state %v", state)
	}
}
