package minipro

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArgs(t *testing.T) {
	o := Options{Device: "AT28C256", SkipVerify: true, VPP: "12", Page: "code"}
	got := strings.Join(o.Args(ActWrite, "x.bin"), " ")
	want := "-p AT28C256 -w x.bin -c code -v -o vpp=12"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	got = strings.Join(Options{Device: "7404", VCC: "3.3"}.Args(ActLogicTest, ""), " ")
	if got != "-p 7404 -T -o vcc=3.3" {
		t.Fatalf("logic args %q", got)
	}
}

func TestParseChipID(t *testing.T) {
	if id, ok := ParseChipID("Chip ID mismatch: expected 0x1E950F, got 0x1E9514 (ATmega328)\n"); !ok || id != 0x1E9514 {
		t.Fatal(id, ok)
	}
	if id, ok := ParseChipID("Chip ID: 0x1E950F  OK\n"); !ok || id != 0x1E950F {
		t.Fatal(id, ok)
	}
}

// TestRunProgressAndPrompt uses a shell script standing in for minipro.
func TestRunProgressAndPrompt(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "minipro")
	script := "#!/bin/sh\n" +
		"printf '\\r\\033[KReading Code...  10%%' >&2\n" +
		"printf '\\r\\033[KReading Code...  99%%' >&2\n" +
		"printf '\\r\\033[KReading Code...  12 ms  OK\\n' >&2\n" +
		"printf 'Do you want to continue with firmware update? y/n:' >&2\n" +
		"read a\necho \"answer=$a\"\nexit 3\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var progress []int
	var asked string
	r := &Runner{Binary: fake}
	res, err := r.Run(context.Background(), nil, func(ev Event) {
		if ev.Kind == EvProgress && ev.Percent >= 0 {
			progress = append(progress, ev.Percent)
		}
	}, func(q string) string { asked = q; return "y" })
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || !strings.Contains(res.Stdout, "answer=y") {
		t.Fatalf("result %+v", res)
	}
	if len(progress) != 2 || progress[1] != 99 {
		t.Fatalf("progress %v", progress)
	}
	if !strings.Contains(asked, "firmware update") || !strings.Contains(res.Output, "Reading Code...  12 ms  OK") {
		t.Fatalf("asked %q output %q", asked, res.Output)
	}
}

func TestDetectParsing(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "minipro")
	ver := "Found T48 00.1.38 (0x126)\nWarning: T48 support is not yet complete!\nWarning: Firmware is out of date.\n  Expected  01.1.39 (0x127)\n  Found     00.1.38 (0x126)\nDevice code: 5AB28017\nminipro version 0.7.4\nT48:\t\t29739 devices, 0 custom\nT76:\t\t34607 devices, 0 custom\n"
	script := "#!/bin/sh\nif [ \"$1\" = -k ]; then echo 't48: T48' >&2; else printf '" +
		strings.ReplaceAll(ver, "\n", "\\n") + "' >&2; fi\n"
	os.WriteFile(fake, []byte(script), 0o755)
	info, err := (&Runner{Binary: fake}).Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Found || info.Model != "t48" || info.Firmware != "T48 00.1.38 (0x126)" {
		t.Fatalf("%+v", info)
	}
	if len(info.Warnings) != 2 || !strings.Contains(info.Warnings[1], "expected 01.1.39") {
		t.Fatalf("warnings %q", info.Warnings)
	}
}
