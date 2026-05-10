package commfeedback

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedNow() time.Time {
	loc := time.FixedZone("EEST", 3*60*60)
	return time.Date(2026, 5, 10, 12, 18, 30, 0, loc)
}

func newCfg(t *testing.T) (Config, string) {
	t.Helper()
	state := t.TempDir()
	inbox := filepath.Join(state, "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	cfg := Config{
		StateDir: state,
		Inbox:    inbox,
		Now:      fixedNow,
	}
	return cfg.Defaults(), state
}

type ledgerRow struct {
	TS            string `json:"ts"`
	Sentiment     string `json:"sentiment"`
	PromptTail    string `json:"prompt_tail"`
	AssistantTail string `json:"assistant_tail"`
}

func readRows(t *testing.T, path string) []ledgerRow {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var rows []ledgerRow
	for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if line == "" {
			continue
		}
		var r ledgerRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal row %q: %v", line, err)
		}
		rows = append(rows, r)
	}
	return rows
}

func TestRunGateSkipsWhenInboxEmpty(t *testing.T) {
	cfg := Config{StateDir: t.TempDir(), Inbox: ""}.Defaults()
	res := Run(cfg, []byte(`{"prompt":"hi +++"}`), nil)
	if !res.Skipped || res.Recorded {
		t.Fatalf("expected Skipped, got %+v", res)
	}
}

func TestRunGateSkipsWhenInboxNotUnderStateDir(t *testing.T) {
	t.Setenv("SKYBOT_INBOX", "")
	cfg := Config{
		StateDir: t.TempDir(),
		Inbox:    filepath.Join(t.TempDir(), "other"),
	}.Defaults()
	res := Run(cfg, []byte(`{"prompt":"hi +++"}`), nil)
	if !res.Skipped {
		t.Fatalf("expected Skipped, got %+v", res)
	}
}

func TestIsCoordinatorBoundaryRespectsSlash(t *testing.T) {
	cfg := Config{StateDir: "/state", Inbox: "/state-other"}
	if cfg.IsCoordinator() {
		t.Fatal("/state-other must not match /state as a prefix")
	}
	cfg = Config{StateDir: "/state", Inbox: "/state/in"}
	if !cfg.IsCoordinator() {
		t.Fatal("/state/in must match /state")
	}
	cfg = Config{StateDir: "/state/", Inbox: "/state"}
	if !cfg.IsCoordinator() {
		t.Fatal("trailing slash on StateDir must be tolerated")
	}
}

func TestRunSkipsOnEmptyPayload(t *testing.T) {
	cfg, _ := newCfg(t)
	if r := Run(cfg, nil, nil); !r.Skipped {
		t.Fatalf("expected Skipped, got %+v", r)
	}
}

func TestRunSkipsOnMalformedPayload(t *testing.T) {
	cfg, _ := newCfg(t)
	if r := Run(cfg, []byte(`not json`), nil); !r.Skipped {
		t.Fatalf("expected Skipped, got %+v", r)
	}
}

func TestRunSkipsOnEmptyPrompt(t *testing.T) {
	cfg, _ := newCfg(t)
	if r := Run(cfg, []byte(`{"prompt":""}`), nil); !r.Skipped {
		t.Fatalf("expected Skipped, got %+v", r)
	}
}

func TestRunSkipsWhenNoTriggerSuffix(t *testing.T) {
	cfg, state := newCfg(t)
	r := Run(cfg, []byte(`{"prompt":"keep going please"}`), nil)
	if !r.Skipped {
		t.Fatalf("expected Skipped, got %+v", r)
	}
	if _, err := os.Stat(filepath.Join(state, "comm-feedback.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected ledger to not exist, got err=%v", err)
	}
}

func TestRunDetectsLikedAndHard(t *testing.T) {
	cases := []struct {
		name      string
		prompt    string
		sentiment string
		body      string
	}{
		{"liked", "thanks for the fix +++", "liked", "thanks for the fix"},
		{"hard", "this broke prod ---", "hard", "this broke prod"},
		{"trailing_whitespace", "  great  +++   \n", "liked", "  great"},
		{"only_trigger", "+++", "liked", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, state := newCfg(t)
			payload, _ := json.Marshal(map[string]string{"prompt": tc.prompt})
			r := Run(cfg, payload, nil)
			if !r.Recorded || r.Sentiment != tc.sentiment {
				t.Fatalf("got %+v, want sentiment=%s recorded=true", r, tc.sentiment)
			}
			rows := readRows(t, filepath.Join(state, "comm-feedback.jsonl"))
			if len(rows) != 1 {
				t.Fatalf("expected 1 row, got %d", len(rows))
			}
			if rows[0].Sentiment != tc.sentiment {
				t.Fatalf("sentiment=%q want %q", rows[0].Sentiment, tc.sentiment)
			}
			if rows[0].PromptTail != tc.body {
				t.Fatalf("prompt_tail=%q want %q", rows[0].PromptTail, tc.body)
			}
			if rows[0].TS != "2026-05-10T12:18:30+03:00" {
				t.Fatalf("ts=%q", rows[0].TS)
			}
		})
	}
}

func TestRunIgnoresWrongTriggerSuffixes(t *testing.T) {
	cfg, _ := newCfg(t)
	for _, p := range []string{"x++", "x+--", "yes ++ ", "ok ?"} {
		payload, _ := json.Marshal(map[string]string{"prompt": p})
		if r := Run(cfg, payload, nil); !r.Skipped {
			t.Fatalf("prompt %q: expected Skipped, got %+v", p, r)
		}
	}
}

func TestRunTruncatesPromptBodyToMax(t *testing.T) {
	cfg, state := newCfg(t)
	long := strings.Repeat("a", 250)
	payload := []byte(`{"prompt":"` + long + ` +++"}`)
	r := Run(cfg, payload, nil)
	if !r.Recorded {
		t.Fatalf("expected Recorded, got %+v", r)
	}
	rows := readRows(t, filepath.Join(state, "comm-feedback.jsonl"))
	if got := len(rows[0].PromptTail); got != cfg.PromptTailMax {
		t.Fatalf("prompt_tail len=%d want %d", got, cfg.PromptTailMax)
	}
	if !strings.HasSuffix(long, rows[0].PromptTail) {
		t.Fatalf("prompt_tail not a suffix of input")
	}
}

func TestRunHonorsCustomPromptTailMax(t *testing.T) {
	cfg, state := newCfg(t)
	cfg.PromptTailMax = 5
	payload := []byte(`{"prompt":"abcdefghij +++"}`)
	if r := Run(cfg, payload, nil); !r.Recorded {
		t.Fatalf("got %+v", r)
	}
	rows := readRows(t, filepath.Join(state, "comm-feedback.jsonl"))
	if rows[0].PromptTail != "fghij" {
		t.Fatalf("prompt_tail=%q", rows[0].PromptTail)
	}
}

func TestRunExtractsAssistantTailFromTranscript(t *testing.T) {
	cfg, state := newCfg(t)
	transcript := []byte(strings.Join([]string{
		`{"role":"user","content":[{"type":"text","text":"hello"}]}`,
		`{"role":"assistant","content":[{"type":"tool_use","name":"Bash"}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"first reply"}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"final answer"},{"type":"text","text":" with more"}]}`,
		`{"role":"user","content":[{"type":"text","text":"thanks +++"}]}`,
	}, "\n") + "\n")
	payload := []byte(`{"prompt":"thanks +++"}`)
	if r := Run(cfg, payload, transcript); !r.Recorded {
		t.Fatalf("got %+v", r)
	}
	rows := readRows(t, filepath.Join(state, "comm-feedback.jsonl"))
	if rows[0].AssistantTail != "final answer with more" {
		t.Fatalf("assistant_tail=%q", rows[0].AssistantTail)
	}
}

func TestRunDecodesCommonEscapesInTranscript(t *testing.T) {
	cfg, state := newCfg(t)
	transcript := []byte(`{"role":"assistant","content":[{"type":"text","text":"line1\nline2\twith \"quote\" and\\slash"}]}` + "\n")
	payload := []byte(`{"prompt":"k +++"}`)
	if r := Run(cfg, payload, transcript); !r.Recorded {
		t.Fatalf("got %+v", r)
	}
	rows := readRows(t, filepath.Join(state, "comm-feedback.jsonl"))
	want := "line1\nline2\twith \"quote\" and\\slash"
	if rows[0].AssistantTail != want {
		t.Fatalf("assistant_tail=%q want %q", rows[0].AssistantTail, want)
	}
}

func TestRunTrimsAssistantTailToMax(t *testing.T) {
	cfg, state := newCfg(t)
	cfg.AssistantTailMax = 10
	transcript := []byte(`{"role":"assistant","content":[{"type":"text","text":"abcdefghijklmnop"}]}` + "\n")
	payload := []byte(`{"prompt":"k +++"}`)
	if r := Run(cfg, payload, transcript); !r.Recorded {
		t.Fatalf("got %+v", r)
	}
	rows := readRows(t, filepath.Join(state, "comm-feedback.jsonl"))
	if rows[0].AssistantTail != "ghijklmnop" {
		t.Fatalf("assistant_tail=%q", rows[0].AssistantTail)
	}
}

func TestRunMissingTranscriptRecordsEmptyAssistantTail(t *testing.T) {
	cfg, state := newCfg(t)
	payload := []byte(`{"prompt":"hi +++","transcript_path":"/nonexistent"}`)
	if r := Run(cfg, payload, nil); !r.Recorded {
		t.Fatalf("got %+v", r)
	}
	rows := readRows(t, filepath.Join(state, "comm-feedback.jsonl"))
	if rows[0].AssistantTail != "" {
		t.Fatalf("assistant_tail=%q", rows[0].AssistantTail)
	}
}

func TestRunWritesReadyMarkerOnceAtThreshold(t *testing.T) {
	cfg, state := newCfg(t)
	cfg.Threshold = 3
	for i := 0; i < 4; i++ {
		if r := Run(cfg, []byte(`{"prompt":"ok +++"}`), nil); !r.Recorded {
			t.Fatalf("call %d: %+v", i, r)
		}
	}
	readyPath := filepath.Join(state, "comm-feedback.ready")
	body, err := os.ReadFile(readyPath)
	if err != nil {
		t.Fatalf("ready marker missing: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, `"count":3`) {
		t.Fatalf("ready marker should reflect threshold-crossing count, got %q", got)
	}
	if !strings.Contains(got, `"ts":"2026-05-10T12:18:30+03:00"`) {
		t.Fatalf("ready marker missing ts: %q", got)
	}
}

func TestRunDoesNotWriteReadyBelowThreshold(t *testing.T) {
	cfg, state := newCfg(t)
	cfg.Threshold = 5
	for i := 0; i < 3; i++ {
		if r := Run(cfg, []byte(`{"prompt":"ok +++"}`), nil); !r.Recorded {
			t.Fatalf("call %d: %+v", i, r)
		}
	}
	if _, err := os.Stat(filepath.Join(state, "comm-feedback.ready")); !os.IsNotExist(err) {
		t.Fatalf("ready marker should not exist, err=%v", err)
	}
}

func TestRunReadyMarkerNotRewritten(t *testing.T) {
	cfg, state := newCfg(t)
	cfg.Threshold = 1
	if r := Run(cfg, []byte(`{"prompt":"first +++"}`), nil); !r.Recorded {
		t.Fatalf("first: %+v", r)
	}
	readyPath := filepath.Join(state, "comm-feedback.ready")
	first, err := os.ReadFile(readyPath)
	if err != nil {
		t.Fatalf("ready marker missing: %v", err)
	}
	if r := Run(cfg, []byte(`{"prompt":"second +++"}`), nil); !r.Recorded {
		t.Fatalf("second: %+v", r)
	}
	second, err := os.ReadFile(readyPath)
	if err != nil {
		t.Fatalf("ready marker missing after second run: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("ready marker rewritten: %q -> %q", first, second)
	}
}

func TestRunWarnsWhenStateDirCannotBeCreated(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	cfg := Config{
		StateDir: filepath.Join(blocker, "child"),
		Inbox:    filepath.Join(blocker, "child"),
		Now:      fixedNow,
	}.Defaults()
	r := Run(cfg, []byte(`{"prompt":"x +++"}`), nil)
	if r.Warning == "" {
		t.Fatalf("expected Warning, got %+v", r)
	}
	if r.Recorded {
		t.Fatalf("expected !Recorded on persistence failure, got %+v", r)
	}
}

func TestDefaultsHonorsEnv(t *testing.T) {
	t.Setenv("SKYHELM_STATE_DIR", "/tmp/skytest")
	t.Setenv("SKYHELM_COMM_FEEDBACK_FILE", "/tmp/skytest/feedback.jsonl")
	t.Setenv("SKYHELM_COMM_FEEDBACK_READY", "/tmp/skytest/feedback.ready")
	t.Setenv("SKYHELM_COMM_FEEDBACK_THRESHOLD", "42")
	t.Setenv("SKYBOT_INBOX", "/tmp/skytest/in")
	c := Config{}.Defaults()
	if c.StateDir != "/tmp/skytest" {
		t.Errorf("StateDir = %q", c.StateDir)
	}
	if c.LedgerFile != "/tmp/skytest/feedback.jsonl" {
		t.Errorf("LedgerFile = %q", c.LedgerFile)
	}
	if c.ReadyFile != "/tmp/skytest/feedback.ready" {
		t.Errorf("ReadyFile = %q", c.ReadyFile)
	}
	if c.Threshold != 42 {
		t.Errorf("Threshold = %d", c.Threshold)
	}
	if c.Inbox != "/tmp/skytest/in" {
		t.Errorf("Inbox = %q", c.Inbox)
	}
}

func TestDefaultsFallbacks(t *testing.T) {
	t.Setenv("SKYHELM_STATE_DIR", "")
	t.Setenv("SKYHELM_COMM_FEEDBACK_FILE", "")
	t.Setenv("SKYHELM_COMM_FEEDBACK_READY", "")
	t.Setenv("SKYHELM_COMM_FEEDBACK_THRESHOLD", "")
	t.Setenv("HOME", "/tmp/skyhome")
	c := Config{}.Defaults()
	if c.StateDir != "/tmp/skyhome/.local/state/skyhelm" {
		t.Errorf("StateDir = %q", c.StateDir)
	}
	if c.LedgerFile != "/tmp/skyhome/.local/state/skyhelm/comm-feedback.jsonl" {
		t.Errorf("LedgerFile = %q", c.LedgerFile)
	}
	if c.ReadyFile != "/tmp/skyhome/.local/state/skyhelm/comm-feedback.ready" {
		t.Errorf("ReadyFile = %q", c.ReadyFile)
	}
	if c.Threshold != DefaultThreshold {
		t.Errorf("Threshold = %d", c.Threshold)
	}
}
