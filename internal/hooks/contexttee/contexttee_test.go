package contexttee

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTranscript(t *testing.T, dir string, total int) string {
	t.Helper()
	path := filepath.Join(dir, "session.jsonl")
	body := `{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":` +
		itoa(total) + `}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestRun_Coordinator(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	tpath := writeTranscript(t, dir, 75000)
	cfg := Config{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
		Tier:                "max",
		Now:                 func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
	}
	payload := `{"session_id":"sess-c","transcript_path":"` + tpath + `"}`
	res, err := Run(cfg, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Fatalf("expected coordinator emit")
	}
	if res.JSON.Role != "coordinator" {
		t.Errorf("role = %q", res.JSON.Role)
	}
	if res.JSON.Slug != "coordinator" {
		t.Errorf("slug = %q", res.JSON.Slug)
	}
	if res.JSON.Ctx != 75000 {
		t.Errorf("ctx = %d", res.JSON.Ctx)
	}
	if res.JSON.CapSoft != DefaultCoordSoftCap || res.JSON.CapHard != DefaultCoordHardCap {
		t.Errorf("caps = %d/%d", res.JSON.CapSoft, res.JSON.CapHard)
	}
	wantPct := (75000 * 100) / DefaultCoordHardCap
	if res.JSON.Pct != wantPct {
		t.Errorf("pct = %d, want %d", res.JSON.Pct, wantPct)
	}
	body, _ := os.ReadFile(filepath.Join(stateDir, "token.json"))
	var got TokenJSON
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Role != "coordinator" || got.Ctx != 75000 {
		t.Errorf("disk: %+v", got)
	}
}

func TestRun_WorkerSubMax(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	workerDir := filepath.Join(dir, "worker-token")
	inbox := filepath.Join(dir, "wt", "feature-x", "inbox")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	tpath := writeTranscript(t, dir, 100000)
	cfg := Config{
		Inbox:               inbox,
		CoordinatorStateDir: stateDir,
		WorkerTokenDir:      workerDir,
		Tier:                "pro",
	}
	payload := `{"session_id":"sess-w","transcript_path":"` + tpath + `"}`
	res, err := Run(cfg, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Fatalf("expected worker emit")
	}
	if res.JSON.Role != "worker" || res.JSON.Slug != "feature-x" {
		t.Errorf("role/slug = %q/%q", res.JSON.Role, res.JSON.Slug)
	}
	if res.JSON.CapSoft != DefaultWorkerWrapSub || res.JSON.CapHard != DefaultWorkerWrapSub {
		t.Errorf("caps = %d/%d, want %d/%d", res.JSON.CapSoft, res.JSON.CapHard, DefaultWorkerWrapSub, DefaultWorkerWrapSub)
	}
	if _, err := os.Stat(filepath.Join(workerDir, "feature-x.json")); err != nil {
		t.Errorf("missing tee file: %v", err)
	}
}

func TestRun_WorkerMaxTier(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "wt", "feat", "inbox")
	os.MkdirAll(inbox, 0o700)
	tpath := writeTranscript(t, dir, 50)
	cfg := Config{
		Inbox:               inbox,
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		WorkerTokenDir:      filepath.Join(dir, "wt-tok"),
		Tier:                "max",
	}
	res, _ := Run(cfg, strings.NewReader(`{"transcript_path":"`+tpath+`"}`))
	if res.JSON.CapHard != DefaultWorkerWrapMax {
		t.Errorf("max-tier cap_hard = %d, want %d", res.JSON.CapHard, DefaultWorkerWrapMax)
	}
}

func TestRun_WorkerWrapOverride(t *testing.T) {
	dir := t.TempDir()
	inbox := filepath.Join(dir, "wt", "feat", "inbox")
	os.MkdirAll(inbox, 0o700)
	tpath := writeTranscript(t, dir, 50)
	cfg := Config{
		Inbox:               inbox,
		CoordinatorStateDir: filepath.Join(dir, "coord"),
		WorkerTokenDir:      filepath.Join(dir, "wt-tok"),
		Tier:                "max",
		WorkerWrapOverride:  90000,
	}
	res, _ := Run(cfg, strings.NewReader(`{"transcript_path":"`+tpath+`"}`))
	if res.JSON.CapHard != 90000 {
		t.Errorf("override cap_hard = %d", res.JSON.CapHard)
	}
}

func TestRun_NoInbox_Skip(t *testing.T) {
	res, err := Run(Config{}, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped {
		t.Fatalf("expected skip with no inbox")
	}
}

func TestRun_NoTranscript_Skip(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "coord")
	cfg := Config{
		Inbox:               stateDir,
		CoordinatorStateDir: stateDir,
	}
	res, _ := Run(cfg, strings.NewReader(`{"session_id":"x"}`))
	if !res.Skipped {
		t.Fatalf("expected skip when no transcript locatable")
	}
}
