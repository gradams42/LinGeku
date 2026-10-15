// Package minipro drives the minipro command-line programmer. LinGecu never
// talks to the USB hardware itself; every chip operation is a minipro run.
package minipro

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Action is a chip operation.
type Action int

const (
	ActRead Action = iota
	ActWrite
	ActVerify
	ActErase
	ActBlankCheck
	ActReadID
	ActLogicTest
	ActPinCheck
)

func (a Action) String() string {
	return [...]string{"Read", "Program", "Verify", "Erase", "Blank check", "Read ID", "Logic test", "Pin check"}[a]
}

// ICSP modes.
const (
	ICSPOff = iota
	ICSPWithVcc
	ICSPNoVcc
)

// Options mirrors minipro's per-operation command-line switches.
type Options struct {
	Device string
	Page   string // "", code, data, config, user, calibration
	Format string // "", ihex, srec

	SkipErase     bool // -e
	ForceErase    bool // -E together with -w (erase config page too)
	SkipVerify    bool // -v
	SkipID        bool // -x
	NoIDError     bool // -y
	PinCheck      bool // -z
	Unprotect     bool // -u
	Protect       bool // -P
	SkipBlank     bool // -B
	NoSizeError   bool // -s
	NoSizeWarning bool // -S
	ICSP          int

	Fuses, UID, Lock bool // --fuses/--uid/--lock filters for the config page

	VPP, VDD, VCC, Pulse, SPIClock, OVCLevel, I2CAddress string
	Partition                                            string // T76 eMMC

	InfoicPath, LogicicPath, AlgoPath string
	LogicOut                          string
}

// Args builds the minipro argument list for an action.
func (o Options) Args(a Action, file string) []string {
	args := []string{"-p", o.Device}
	switch a {
	case ActRead:
		args = append(args, "-r", file)
	case ActWrite:
		if o.ForceErase {
			args = append(args, "-E")
		}
		args = append(args, "-w", file)
	case ActVerify:
		args = append(args, "-m", file)
	case ActErase:
		args = append(args, "-E")
	case ActBlankCheck:
		args = append(args, "-b")
	case ActReadID:
		args = append(args, "-D")
	case ActLogicTest:
		args = append(args, "-T")
		if o.LogicOut != "" {
			args = append(args, "--logicic_out", o.LogicOut)
		}
	case ActPinCheck:
		args = append(args, "-z")
	}
	if a != ActLogicTest && a != ActPinCheck {
		if o.Page != "" && a != ActErase && a != ActReadID {
			args = append(args, "-c", o.Page)
		}
		if o.Format != "" && a == ActRead {
			args = append(args, "-f", o.Format)
		}
		if o.Fuses {
			args = append(args, "--fuses")
		}
		if o.UID {
			args = append(args, "--uid")
		}
		if o.Lock {
			args = append(args, "--lock")
		}
		flag := func(on bool, f string) {
			if on {
				args = append(args, f)
			}
		}
		if a == ActWrite {
			flag(o.SkipErase, "-e")
			flag(o.SkipVerify, "-v")
			flag(o.Unprotect, "-u")
			flag(o.Protect, "-P")
			flag(o.SkipBlank, "-B")
		}
		if a == ActWrite || a == ActVerify {
			flag(o.NoSizeError && !o.NoSizeWarning, "-s")
			flag(o.NoSizeWarning, "-S")
		}
		if a == ActRead || a == ActVerify || a == ActBlankCheck {
			flag(o.SkipID, "-x")
		}
		flag(o.NoIDError, "-y")
		if o.PinCheck && a != ActReadID {
			args = append(args, "-z")
		}
		switch o.ICSP {
		case ICSPWithVcc:
			args = append(args, "-i")
		case ICSPNoVcc:
			args = append(args, "-I")
		}
		if o.Partition != "" {
			args = append(args, "--partition", o.Partition)
		}
	}
	opt := func(name, v string) {
		if strings.TrimSpace(v) != "" {
			args = append(args, "-o", name+"="+strings.TrimSpace(v))
		}
	}
	if a == ActWrite {
		opt("vpp", o.VPP)
		opt("vdd", o.VDD)
		opt("pulse", o.Pulse)
	}
	if a == ActWrite || a == ActLogicTest {
		opt("vcc", o.VCC)
	}
	opt("spi_clock", o.SPIClock)
	opt("ovc_level", o.OVCLevel)
	opt("address", o.I2CAddress)
	return append(args, o.globalArgs()...)
}

func (o Options) globalArgs() []string {
	var args []string
	if o.InfoicPath != "" {
		args = append(args, "--infoic", o.InfoicPath)
	}
	if o.LogicicPath != "" {
		args = append(args, "--logicic", o.LogicicPath)
	}
	if o.AlgoPath != "" {
		args = append(args, "--algorithms", o.AlgoPath)
	}
	return args
}

// EventKind distinguishes output events.
type EventKind int

const (
	EvLine     EventKind = iota // a completed line of output
	EvProgress                  // a progress update (label + percent)
)

// Event is one piece of minipro output.
type Event struct {
	Kind    EventKind
	Text    string
	Percent int
	Stdout  bool
}

// Prompter answers an interactive question minipro asks on stdin.
type Prompter func(question string) string

// Result summarises a finished run.
type Result struct {
	Args     []string
	ExitCode int
	Output   string // combined, cleaned output
	Stdout   string
}

// OK reports whether minipro exited successfully.
func (r *Result) OK() bool { return r.ExitCode == 0 }

// Runner executes minipro.
type Runner struct {
	Binary string
}

// Locate finds a minipro binary: an explicit path, one next to the LinGecu
// executable, or $PATH.
func Locate(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit, nil
		}
		return "", fmt.Errorf("minipro not found at %s", explicit)
	}
	var cands []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		cands = append(cands, filepath.Join(dir, "minipro"))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return c, nil
		}
	}
	if p, err := exec.LookPath("minipro"); err == nil {
		return p, nil
	}
	return "", errors.New("minipro executable not found; install minipro (see README) or set its path in Settings")
}

var (
	ansiRe     = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	progressRe = regexp.MustCompile(`^(.*?)\s*(\d{1,3})%\s*$`)
)

// Run executes minipro with args, streaming output events to handler.
func (r *Runner) Run(ctx context.Context, args []string, handler func(Event), prompt Prompter) (*Result, error) {
	if r.Binary == "" {
		return nil, errors.New("minipro executable not configured")
	}
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	res := &Result{Args: args}
	var mu sync.Mutex
	var all, out strings.Builder
	emit := func(ev Event) {
		mu.Lock()
		if ev.Kind == EvLine {
			all.WriteString(ev.Text)
			all.WriteByte('\n')
			if ev.Stdout {
				out.WriteString(ev.Text)
				out.WriteByte('\n')
			}
		}
		mu.Unlock()
		if handler != nil {
			handler(ev)
		}
	}
	answer := func(q string) {
		a := "n"
		if prompt != nil {
			a = prompt(q)
		}
		io.WriteString(stdin, a+"\n")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); scan(stdout, true, emit, answer) }()
	go func() { defer wg.Done(); scan(stderr, false, emit, answer) }()
	wg.Wait()
	err = cmd.Wait()
	stdin.Close()
	res.Output, res.Stdout = all.String(), out.String()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			return res, nil
		}
		return res, err
	}
	return res, nil
}

// scan splits a stream on \r and \n, turning carriage-return rewrites into
// progress events, and detects interactive prompts left without a newline.
func scan(rd io.Reader, isStdout bool, emit func(Event), answer func(string)) {
	br := bufio.NewReader(rd)
	var cur bytes.Buffer
	last := "" // last progress text, so partial reads are not reported twice
	flushProgress := func() {
		s := strings.TrimRight(ansiRe.ReplaceAllString(cur.String(), ""), " ")
		if s == "" || s == last {
			return
		}
		last = s
		if m := progressRe.FindStringSubmatch(s); m != nil {
			p, _ := strconv.Atoi(m[2])
			emit(Event{Kind: EvProgress, Text: strings.TrimSpace(m[1]), Percent: p, Stdout: isStdout})
		} else {
			emit(Event{Kind: EvProgress, Text: s, Percent: -1, Stdout: isStdout})
		}
	}
	buf := make([]byte, 4096)
	for {
		n, err := br.Read(buf)
		for _, c := range buf[:n] {
			switch c {
			case '\n':
				s := strings.TrimRight(ansiRe.ReplaceAllString(cur.String(), ""), " \r")
				emit(Event{Kind: EvLine, Text: s, Stdout: isStdout})
				cur.Reset()
				last = ""
			case '\r':
				flushProgress()
				cur.Reset()
			default:
				cur.WriteByte(c)
			}
		}
		// A partial line waiting for input is an interactive question.
		if br.Buffered() == 0 && cur.Len() > 0 {
			s := strings.TrimSpace(ansiRe.ReplaceAllString(cur.String(), ""))
			if strings.HasSuffix(s, "y/n:") || strings.HasSuffix(s, "(y/n)") {
				emit(Event{Kind: EvLine, Text: s, Stdout: isStdout})
				cur.Reset()
				answer(s)
			} else {
				flushProgress()
			}
		}
		if err != nil {
			if cur.Len() > 0 {
				emit(Event{Kind: EvLine, Text: strings.TrimRight(ansiRe.ReplaceAllString(cur.String(), ""), " \r"), Stdout: isStdout})
			}
			return
		}
	}
}

// Simple runs minipro and returns the result without streaming.
func (r *Runner) Simple(ctx context.Context, args ...string) (*Result, error) {
	return r.Run(ctx, args, nil, nil)
}

// Version returns minipro's version banner and share directory.
func (r *Runner) Version(ctx context.Context) (banner, shareDir string, err error) {
	res, err := r.Simple(ctx, "-V")
	if err != nil {
		return "", "", err
	}
	for _, l := range strings.Split(res.Output, "\n") {
		if strings.HasPrefix(l, "minipro version") {
			banner = strings.TrimSpace(l)
		}
		if i := strings.Index(l, "Share dir:"); i >= 0 {
			shareDir = strings.TrimSpace(l[i+len("Share dir:"):])
		}
	}
	return banner, shareDir, nil
}

// ProgrammerInfo describes a connected programmer.
type ProgrammerInfo struct {
	Found    bool
	Model    string // tl866a, tl866ii, t48, t56, t76
	Label    string
	Firmware string
	Warnings []string // e.g. "Firmware is out of date. Expected 01.1.39 ..."
	Raw      string
}

// Detect queries the connected programmer with -k.
func (r *Runner) Detect(ctx context.Context) (*ProgrammerInfo, error) {
	res, err := r.Simple(ctx, "-k")
	if err != nil {
		return nil, err
	}
	out := res.Output
	// -k only names the model; -V also reports firmware and its warnings.
	if v, err := r.Simple(ctx, "-V"); err == nil {
		out += v.Output
	}
	info := &ProgrammerInfo{Raw: strings.TrimSpace(out)}
	// The model comes only from -k: -V also lists "T76: 34607 devices" etc.
	for _, l := range strings.Split(res.Output, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), ":"); ok {
			switch strings.ToLower(k) {
			case "tl866a", "tl866ii", "t48", "t56", "t76":
				info.Found, info.Model, info.Label = true, strings.ToLower(k), strings.TrimSpace(v)
			}
		}
	}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "Found ") && info.Firmware == "":
			info.Firmware = strings.TrimPrefix(l, "Found ")
		case strings.HasPrefix(l, "Warning:"):
			info.Warnings = append(info.Warnings, strings.TrimSpace(strings.TrimPrefix(l, "Warning:")))
		case strings.HasPrefix(l, "Expected") && len(info.Warnings) > 0:
			info.Warnings[len(info.Warnings)-1] += " (expected " + strings.TrimSpace(strings.TrimPrefix(l, "Expected")) + ")"
		}
	}
	return info, nil
}

var (
	chipIDRe  = regexp.MustCompile(`Chip ID:\s*(0x[0-9A-Fa-f]+)`)
	chipGotRe = regexp.MustCompile(`expected 0x[0-9A-Fa-f]+, got (0x[0-9A-Fa-f]+)`)
)

// ParseChipID finds the chip ID in a run's output, including the actual ID
// reported on a mismatch.
func ParseChipID(out string) (uint32, bool) {
	m := chipGotRe.FindStringSubmatch(out)
	if m == nil {
		m = chipIDRe.FindStringSubmatch(out)
	}
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseUint(m[1], 0, 32)
	return uint32(v), err == nil
}

// ParseConfig parses a minipro fuses/config file (name=value lines).
func ParseConfig(text string) (keys []string, vals map[string]string) {
	vals = map[string]string{}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			if k2, v2, ok2 := strings.Cut(l, ":"); ok2 {
				k, v, ok = k2, v2, true
			}
		}
		if ok {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if _, dup := vals[k]; !dup {
				keys = append(keys, k)
			}
			vals[k] = v
		}
	}
	return keys, vals
}

// FormatConfig renders name=value lines in minipro's format.
func FormatConfig(keys []string, vals map[string]string) string {
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, vals[k])
	}
	return b.String()
}
