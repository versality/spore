package operatoringress

import (
	"crypto/sha256"
	"encoding/hex"
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

func newCfg(t *testing.T, prompt string) (Config, string) {
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

func TestRunGateSkipsWhenInboxNotUnderStateDir(t *testing.T) {
	t.Setenv("SKYBOT_INBOX", "")
	cfg := Config{
		StateDir: t.TempDir(),
		Inbox:    filepath.Join(t.TempDir(), "other"),
	}.Defaults()
	res := Run(cfg, []byte(`{"prompt":"hi"}`))
	if !res.Skipped || res.Failed {
		t.Fatalf("expected Skipped, got %+v", res)
	}
}

func TestRunGateSkipsWhenInboxEmpty(t *testing.T) {
	cfg := Config{StateDir: t.TempDir(), Inbox: ""}.Defaults()
	if cfg.IsCoordinator() {
		t.Fatal("expected !IsCoordinator with empty inbox")
	}
	res := Run(cfg, []byte(`{"prompt":"hi"}`))
	if !res.Skipped {
		t.Fatalf("expected Skipped, got %+v", res)
	}
}

func TestRunFailsOnEmptyPayload(t *testing.T) {
	cfg, _ := newCfg(t, "")
	res := Run(cfg, nil)
	if !res.Failed || !strings.Contains(res.ErrorMsg, "missing hook payload") {
		t.Fatalf("unexpected: %+v", res)
	}
}

func TestRunFailsOnMissingPrompt(t *testing.T) {
	cfg, _ := newCfg(t, "")
	res := Run(cfg, []byte(`{"session_id":"x"}`))
	if !res.Failed || !strings.Contains(res.ErrorMsg, "missing prompt") {
		t.Fatalf("unexpected: %+v", res)
	}
}

func TestRunFailsOnMalformedJSON(t *testing.T) {
	cfg, _ := newCfg(t, "")
	res := Run(cfg, []byte(`not json`))
	if !res.Failed || !strings.Contains(res.ErrorMsg, "missing prompt") {
		t.Fatalf("unexpected: %+v", res)
	}
}

func TestRunPersistsRowAndHeader(t *testing.T) {
	cfg, state := newCfg(t, "")
	prompt := "summarize state.md"
	res := Run(cfg, []byte(`{"prompt":"`+prompt+`"}`))
	if res.Failed || res.Skipped {
		t.Fatalf("unexpected: %+v", res)
	}

	body, err := os.ReadFile(filepath.Join(state, "state.md"))
	if err != nil {
		t.Fatalf("read state.md: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "## Operator ingress ledger") {
		t.Fatalf("missing header in:\n%s", got)
	}
	sum := sha256.Sum256([]byte(prompt))
	hash := hex.EncodeToString(sum[:])
	if !strings.Contains(got, "sha256="+hash) {
		t.Fatalf("missing hash in:\n%s", got)
	}
	if !strings.Contains(got, `excerpt=summarize state.md`) {
		t.Fatalf("missing excerpt in:\n%s", got)
	}
	if !strings.Contains(got, `chars=18`) {
		t.Fatalf("expected chars=18, got:\n%s", got)
	}
	if !strings.Contains(got, `next="process before probes dispatch edits"`) {
		t.Fatalf("missing next= clause in:\n%s", got)
	}
	if !strings.HasPrefix(got, "\n## Operator ingress ledger\n- 2026-05-10T12:18:30+03:00 ") {
		t.Fatalf("unexpected prefix:\n%s", got)
	}
}

func TestRunSecondCallReusesHeader(t *testing.T) {
	cfg, state := newCfg(t, "")
	if r := Run(cfg, []byte(`{"prompt":"first"}`)); r.Failed {
		t.Fatalf("first run: %+v", r)
	}
	if r := Run(cfg, []byte(`{"prompt":"second"}`)); r.Failed {
		t.Fatalf("second run: %+v", r)
	}
	body, _ := os.ReadFile(filepath.Join(state, "state.md"))
	if n := strings.Count(string(body), "## Operator ingress ledger"); n != 1 {
		t.Fatalf("expected exactly 1 header, got %d:\n%s", n, body)
	}
	if n := strings.Count(string(body), "operator-prompt status=pending"); n != 2 {
		t.Fatalf("expected 2 rows, got %d:\n%s", n, body)
	}
}

func TestRunRedactsSensitivePromptToMarker(t *testing.T) {
	cfg, state := newCfg(t, "")
	res := Run(cfg, []byte(`{"prompt":"my password is hunter2"}`))
	if res.Failed {
		t.Fatalf("unexpected: %+v", res)
	}
	body, _ := os.ReadFile(filepath.Join(state, "state.md"))
	if !strings.Contains(string(body), "excerpt=[redacted-sensitive-prompt]") {
		t.Fatalf("expected sensitive marker in:\n%s", body)
	}
}

func TestBuildExcerptCases(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   string
	}{
		{"plain", "hello world", "hello world"},
		{"sensitive_password", "PASSWORD: hunter2", "[redacted-sensitive-prompt]"},
		{"sensitive_apikey", "let me set api_key = abc", "[redacted-sensitive-prompt]"},
		{"sensitive_skkey", "use sk-1234567890ABCDEF1234", "[redacted-sensitive-prompt]"},
		{"empty", "\n  \n\t\n", "[empty-prompt]"},
		{"long", strings.Repeat("a", 200), "[redacted-long-line]"},
		{"crlf_collapses", "first\rline", "first line"},
		{"tabs_to_spaces", "first\tword", "first word"},
		{"multiline_takes_first_nonblank", "\n\nfoo bar\nbaz", "foo bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildExcerpt(tc.prompt, DefaultMaxExcerpt)
			if got != tc.want {
				t.Errorf("buildExcerpt(%q) = %q want %q", tc.prompt, got, tc.want)
			}
		})
	}
}

func TestRunRespectsMaxExcerptOverride(t *testing.T) {
	cfg, state := newCfg(t, "")
	cfg.MaxExcerpt = 5
	res := Run(cfg, []byte(`{"prompt":"abcdefghij"}`))
	if res.Failed {
		t.Fatalf("unexpected: %+v", res)
	}
	body, _ := os.ReadFile(filepath.Join(state, "state.md"))
	if !strings.Contains(string(body), "excerpt=[redacted-long-line]") {
		t.Fatalf("expected long-line marker:\n%s", body)
	}
}

func TestRunPreservesExistingStateFileBody(t *testing.T) {
	cfg, state := newCfg(t, "")
	stateFile := filepath.Join(state, "state.md")
	preamble := "# state\n\n## Some other section\n- thing\n"
	if err := os.WriteFile(stateFile, []byte(preamble), 0o600); err != nil {
		t.Fatalf("seed state.md: %v", err)
	}
	if r := Run(cfg, []byte(`{"prompt":"hi"}`)); r.Failed {
		t.Fatalf("run: %+v", r)
	}
	body, _ := os.ReadFile(stateFile)
	if !strings.HasPrefix(string(body), preamble) {
		t.Fatalf("preamble truncated:\n%s", body)
	}
	if !strings.Contains(string(body), "## Operator ingress ledger") {
		t.Fatalf("header missing:\n%s", body)
	}
}

func TestRunReusesPreexistingHeader(t *testing.T) {
	cfg, state := newCfg(t, "")
	stateFile := filepath.Join(state, "state.md")
	preamble := "# state\n\n## Operator ingress ledger\n- old row\n"
	if err := os.WriteFile(stateFile, []byte(preamble), 0o600); err != nil {
		t.Fatalf("seed state.md: %v", err)
	}
	if r := Run(cfg, []byte(`{"prompt":"hi"}`)); r.Failed {
		t.Fatalf("run: %+v", r)
	}
	body, _ := os.ReadFile(stateFile)
	if n := strings.Count(string(body), "## Operator ingress ledger"); n != 1 {
		t.Fatalf("header duplicated, got %d:\n%s", n, body)
	}
}

func TestDefaultsHonorsEnv(t *testing.T) {
	t.Setenv("SKYHELM_STATE_DIR", "/tmp/skytest")
	t.Setenv("SKYHELM_STATE_FILE", "/tmp/skytest/custom.md")
	t.Setenv("SKYBOT_INBOX", "/tmp/skytest/in")
	t.Setenv("SKYHELM_OPERATOR_INGRESS_MAX_EXCERPT", "42")
	c := Config{}.Defaults()
	if c.StateDir != "/tmp/skytest" {
		t.Errorf("StateDir = %q", c.StateDir)
	}
	if c.StateFile != "/tmp/skytest/custom.md" {
		t.Errorf("StateFile = %q", c.StateFile)
	}
	if c.Inbox != "/tmp/skytest/in" {
		t.Errorf("Inbox = %q", c.Inbox)
	}
	if c.MaxExcerpt != 42 {
		t.Errorf("MaxExcerpt = %d", c.MaxExcerpt)
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
