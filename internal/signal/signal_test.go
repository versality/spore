package signal

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) }

func newCfg(t *testing.T) Config {
	t.Helper()
	return Config{
		StateDir:    t.TempDir(),
		Tool:        "build.sh",
		Owner:       "ops",
		TicketAfter: 3,
		Now:         fixedNow,
		Logger:      &bytes.Buffer{},
	}
}

func TestNormalizeLine(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"foo\tbar 12345", "foo bar <n>"},
		{"open /tmp/abc/def: 99999", "open /tmp/... <n>"},
		{"event at 2026-05-10T12:00:00.123-0400Z done", "event at <timestamp> done"},
		{"path /nix/store/12345xyz keep", "path /nix/store/12345xyz keep"},
		{"PID 12345 done", "PID <n> done"},
	}
	for _, c := range cases {
		got := NormalizeLine(c.in)
		if got != c.want {
			t.Errorf("NormalizeLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHashSignal_Stable(t *testing.T) {
	a := HashSignal(SeverityWarning, "tool", "deprecated foo")
	b := HashSignal(SeverityWarning, "tool", "deprecated foo")
	if a != b {
		t.Fatalf("hash unstable: %s vs %s", a, b)
	}
	if len(a) != 12 {
		t.Fatalf("hash length = %d, want 12", len(a))
	}
	if HashSignal(SeverityError, "tool", "deprecated foo") == a {
		t.Fatalf("severity should change hash")
	}
	if HashSignal(SeverityWarning, "other", "deprecated foo") == a {
		t.Fatalf("tool should change hash")
	}
}

func TestMatchesSignal(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"warning: deprecated", true},
		{"WARN something", true},
		{"failed to connect", true},
		{"will be removed in 3.0", true},
		{"[ok] done", false},
		{"all good", false},
		{"warner is a name", false}, // "warn" not on a non-alpha boundary -> "warner" should not match
	}
	for _, tc := range tests {
		got := MatchesSignal(tc.line)
		if got != tc.want {
			t.Errorf("MatchesSignal(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestProcess_Passthrough_RecordsExitError(t *testing.T) {
	cfg := newCfg(t)
	res, err := Process(cfg, []byte("all good\n"), "build.sh -v", 7)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", res.ExitCode)
	}
	if len(res.Signals) != 1 {
		t.Fatalf("signals = %d, want 1", len(res.Signals))
	}
	s := res.Signals[0]
	if s.Severity != SeverityError {
		t.Errorf("severity = %s, want error", s.Severity)
	}
	if s.Action != ActionBlock {
		t.Errorf("action = %s, want block", s.Action)
	}
	if !strings.HasPrefix(s.Summary, "exit=7 cmd=") {
		t.Errorf("summary = %q, want prefix 'exit=7 cmd='", s.Summary)
	}
	if s.Count != 1 {
		t.Errorf("count = %d, want 1", s.Count)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.StateDir, "seen.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), s.Hash) {
		t.Errorf("ledger missing hash %s\n%s", s.Hash, raw)
	}
}

func TestProcess_ScansWarningsAndDedupes(t *testing.T) {
	cfg := newCfg(t)
	output := []byte(`starting
warning: foo deprecated
warning: foo deprecated
[ok] notice the word warning here is in the brackets
all clean
`)
	res, err := Process(cfg, output, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	if len(res.Signals) != 1 {
		t.Fatalf("signals = %d (want 1 after dedup): %+v", len(res.Signals), res.Signals)
	}
	if res.Signals[0].Severity != SeverityWarning {
		t.Errorf("severity = %s", res.Signals[0].Severity)
	}
}

func TestProcess_TicketAfterThreshold(t *testing.T) {
	cfg := newCfg(t)
	cfg.TicketAfter = 3
	out := []byte("warning: leaky pipe\n")
	for i := 1; i <= 4; i++ {
		res, err := Process(cfg, out, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Signals) != 1 {
			t.Fatalf("iter %d: signals = %d", i, len(res.Signals))
		}
		s := res.Signals[0]
		if s.Count != i {
			t.Errorf("iter %d: count = %d, want %d", i, s.Count, i)
		}
		switch i {
		case 1, 2:
			if s.Action != ActionLog {
				t.Errorf("iter %d: action = %s, want log", i, s.Action)
			}
		case 3, 4:
			if s.Action != ActionTicketCandidate {
				t.Errorf("iter %d: action = %s, want ticket-candidate", i, s.Action)
			}
		}
	}
	// Verify events.jsonl has 4 lines, last with count=4.
	raw, err := os.ReadFile(filepath.Join(cfg.StateDir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("events lines = %d, want 4", len(lines))
	}
	var last struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(lines[3]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Count != 4 {
		t.Errorf("last event count = %d, want 4", last.Count)
	}
}

func TestProcess_DryRunWritesNothing(t *testing.T) {
	cfg := newCfg(t)
	cfg.DryRun = true
	logger := &bytes.Buffer{}
	cfg.Logger = logger
	_, err := Process(cfg, []byte("warning: stale\n"), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dry-run wrote files: %v", names)
	}
	if !strings.Contains(logger.String(), "dry-run") {
		t.Errorf("logger missing dry-run marker: %q", logger.String())
	}
}

func TestProcess_DedupKeyDigestMatchesAcrossNoise(t *testing.T) {
	cfg := newCfg(t)
	first, err := Process(cfg, []byte("error: PID 99999 failed at /tmp/abc/scratch.txt\n"), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Signals) != 1 {
		t.Fatalf("first signals = %d", len(first.Signals))
	}
	second, err := Process(cfg, []byte("error: PID 12345 failed at /tmp/different/path.log\n"), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Signals) != 1 {
		t.Fatalf("second signals = %d", len(second.Signals))
	}
	if first.Signals[0].Hash != second.Signals[0].Hash {
		t.Errorf("digest unstable across noise: %s vs %s\n  first=%q\n  second=%q",
			first.Signals[0].Hash, second.Signals[0].Hash,
			first.Signals[0].Summary, second.Signals[0].Summary)
	}
	if second.Signals[0].Count != 2 {
		t.Errorf("second count = %d, want 2", second.Signals[0].Count)
	}
}

func TestProcess_AutoMintFiresOnceThenSkipsMinted(t *testing.T) {
	cfg := newCfg(t)
	cfg.AutoMint = true
	cfg.TicketAfter = 1 // first sighting already crosses
	var calls int
	cfg.Mint = func(hash, title, body string) error {
		calls++
		if !strings.Contains(body, "Hash: "+hash) {
			t.Errorf("body missing hash %s: %s", hash, body)
		}
		return nil
	}
	if _, err := Process(cfg, []byte("warning: zonk\n"), "", 0); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls after first run = %d, want 1", calls)
	}
	if _, err := Process(cfg, []byte("warning: zonk\n"), "", 0); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("auto-mint re-fired: calls = %d, want still 1", calls)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.StateDir, "minted.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "\n") != 1 {
		t.Errorf("minted.tsv lines = %d, want 1: %s", strings.Count(string(raw), "\n"), raw)
	}
}

func TestCapture_PassesExitCodeAndScansOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only")
	}
	cfg := newCfg(t)
	var out, errBuf bytes.Buffer
	output, exit, err := Capture(context.Background(),
		[]string{"sh", "-c", "echo warning: deprecated >&2; echo regular line; exit 4"},
		false, "build.sh", &out, &errBuf)
	if err != nil {
		t.Fatal(err)
	}
	if exit != 4 {
		t.Fatalf("exit = %d, want 4", exit)
	}
	if !strings.Contains(string(output), "warning: deprecated") {
		t.Errorf("captured missing warning: %q", output)
	}
	res, err := Process(cfg, output, ShellQuote([]string{"sh", "-c", "echo ..."}), exit)
	if err != nil {
		t.Fatal(err)
	}
	// Two signals: the exit=4 error and the deprecated warning.
	if len(res.Signals) != 2 {
		t.Fatalf("signals = %d, want 2", len(res.Signals))
	}
	var sawErr, sawWarn bool
	for _, s := range res.Signals {
		switch s.Severity {
		case SeverityError:
			sawErr = true
			if !strings.HasPrefix(s.Summary, "exit=4") {
				t.Errorf("err summary = %q", s.Summary)
			}
		case SeverityWarning:
			sawWarn = true
		}
	}
	if !sawErr || !sawWarn {
		t.Errorf("missing severity coverage: err=%v warn=%v", sawErr, sawWarn)
	}
}

func TestCapture_PreserveStreamsKeepsStdoutAndStderrSeparate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only")
	}
	var out, errBuf bytes.Buffer
	output, exit, err := Capture(context.Background(),
		[]string{"sh", "-c", "echo regular; echo error: bad >&2; exit 0"},
		true, "x.sh", &out, &errBuf)
	if err != nil {
		t.Fatal(err)
	}
	if exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	if !strings.Contains(out.String(), "regular") || strings.Contains(out.String(), "error: bad") {
		t.Errorf("preserve stdout = %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "error: bad") || strings.Contains(errBuf.String(), "regular") {
		t.Errorf("preserve stderr = %q", errBuf.String())
	}
	if !strings.Contains(string(output), "regular") || !strings.Contains(string(output), "error: bad") {
		t.Errorf("scan view should be union: %q", output)
	}
}

func TestProcess_LedgerUpdatesInPlace(t *testing.T) {
	cfg := newCfg(t)
	if _, err := Process(cfg, []byte("warning: pin\n"), "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Process(cfg, []byte("warning: pin\n"), "", 0); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.StateDir, "seen.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("ledger lines = %d, want 1: %s", len(lines), raw)
	}
	fields := strings.Split(lines[0], "\t")
	if len(fields) != 9 {
		t.Fatalf("ledger fields = %d, want 9: %v", len(fields), fields)
	}
	count, err := strconv.Atoi(fields[3])
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestShellQuote(t *testing.T) {
	got := ShellQuote([]string{"go", "test", "./..."})
	if got != "go test ./..." {
		t.Errorf("ShellQuote simple = %q", got)
	}
	got = ShellQuote([]string{"echo", "hi there"})
	if got != "echo 'hi there'" {
		t.Errorf("ShellQuote spaces = %q", got)
	}
	got = ShellQuote([]string{"echo", "it's"})
	if got != "echo 'it'\\''s'" {
		t.Errorf("ShellQuote single-quote = %q", got)
	}
}
