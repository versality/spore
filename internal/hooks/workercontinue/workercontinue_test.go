package workercontinue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheck(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		setup      func(t *testing.T, env *testEnv)
		wantFire   bool
		wantReason string
	}{
		{
			name:       "no slug noop",
			setup:      func(t *testing.T, env *testEnv) { env.cfg.Slug = "" },
			wantReason: "noop-context",
		},
		{
			name:       "missing task file",
			setup:      func(t *testing.T, env *testEnv) {},
			wantReason: "missing-task",
		},
		{
			name: "status draft noop",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "draft", "")
			},
			wantReason: "not-active",
		},
		{
			name: "status blocked noop",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "blocked", "")
			},
			wantReason: "not-active",
		},
		{
			name: "awaiting operator suppresses",
			setup: func(t *testing.T, env *testEnv) {
				writeTaskWithExtra(t, env, "active", map[string]string{"worker-state": "awaiting-operator"}, "")
			},
			wantReason: "awaiting-operator",
		},
		{
			name: "fleet disabled noop",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "active", "")
				env.cfg.FleetEnabled = func() (bool, error) { return false, nil }
			},
			wantReason: "fleet-disabled",
		},
		{
			name: "inbox unread suppresses",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "active", "")
				writeInbox(t, env, "1234-tell.json", `{"body":"hi"}`)
			},
			wantReason: "inbox-unread",
		},
		{
			name: "inbox under read does not suppress",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "active", "")
				inbox := filepath.Join(env.cfg.WtStateDir, env.cfg.Slug, "inbox", "read")
				mkdir(t, inbox)
				writeFile(t, filepath.Join(inbox, "1234-tell.json"), `{"body":"hi"}`)
			},
			wantFire:   true,
			wantReason: "ok-fire",
		},
		{
			name: "plan pending ack suppresses",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "active", "\n## Plan\n\nbody\n")
				writeCoordInbox(t, env, "ready.json", `{"body":"plan ready: `+env.cfg.Slug+`"}`)
			},
			wantReason: "plan-pending-ack",
		},
		{
			name: "plan ack present does not suppress",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "active", "\n## Plan\n\nbody\n")
				writeCoordInbox(t, env, "ready.json", `{"body":"plan ready: `+env.cfg.Slug+`"}`)
				writeCoordInbox(t, env, "ack.json", `{"body":"plan ack: `+env.cfg.Slug+` proceed"}`)
			},
			wantFire:   true,
			wantReason: "ok-fire",
		},
		{
			name: "no plan section ignores coord inbox",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "active", "")
				writeCoordInbox(t, env, "ready.json", `{"body":"plan ready: `+env.cfg.Slug+`"}`)
			},
			wantFire:   true,
			wantReason: "ok-fire",
		},
		{
			name: "token wrap fresh suppresses",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "active", "")
				snap := filepath.Join(env.cfg.TokenStateDir, env.cfg.Slug+".json")
				mkdir(t, filepath.Dir(snap))
				writeFile(t, snap, `{}`)
				touch(t, snap, now.Add(-1*time.Minute))
			},
			wantReason: "token-wrap-fired",
		},
		{
			name: "token wrap stale does not suppress",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "active", "")
				snap := filepath.Join(env.cfg.TokenStateDir, env.cfg.Slug+".json")
				mkdir(t, filepath.Dir(snap))
				writeFile(t, snap, `{}`)
				touch(t, snap, now.Add(-1*time.Hour))
			},
			wantFire:   true,
			wantReason: "ok-fire",
		},
		{
			name: "fingerprint match suppresses",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "active", "")
				taskFile := filepath.Join(env.cfg.Worktree, "tasks", env.cfg.Slug+".md")
				fp := fingerprint(taskFile, env.cfg.Head)
				_ = writeLedger(env.cfg.ledgerPath(), fp, now.Add(-30*time.Second))
			},
			wantReason: "already-nudged",
		},
		{
			name: "fingerprint mismatch refires (HEAD moved)",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "active", "")
				taskFile := filepath.Join(env.cfg.Worktree, "tasks", env.cfg.Slug+".md")
				oldHead := env.cfg.Head
				env.cfg.Head = func() string { return "deadbeef" }
				fp := fingerprint(taskFile, env.cfg.Head)
				_ = writeLedger(env.cfg.ledgerPath(), fp, now.Add(-30*time.Second))
				env.cfg.Head = oldHead
			},
			wantFire:   true,
			wantReason: "ok-fire",
		},
		{
			name: "clean active fires",
			setup: func(t *testing.T, env *testEnv) {
				writeTask(t, env, "active", "")
			},
			wantFire:   true,
			wantReason: "ok-fire",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, now)
			tc.setup(t, env)
			res, err := Check(env.cfg)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if res.ShouldFire != tc.wantFire {
				t.Fatalf("ShouldFire = %v, want %v (reason=%s)", res.ShouldFire, tc.wantFire, res.Reason)
			}
			if res.Reason != tc.wantReason {
				t.Fatalf("Reason = %q, want %q", res.Reason, tc.wantReason)
			}
			if tc.wantFire && res.Message == "" {
				t.Fatalf("ShouldFire=true but empty Message")
			}
		})
	}
}

func TestRunPersistsLedger(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	env := newTestEnv(t, now)
	writeTask(t, env, "active", "")

	res, err := Run(env.cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.ShouldFire {
		t.Fatalf("first Run ShouldFire = false, want true (reason=%s)", res.Reason)
	}

	b, err := os.ReadFile(env.cfg.ledgerPath())
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	var entry ledgerEntry
	if err := json.Unmarshal(b, &entry); err != nil {
		t.Fatalf("parse ledger: %v", err)
	}
	if entry.Fingerprint == "" {
		t.Fatalf("ledger missing fingerprint")
	}
	if !entry.FiredAt.Equal(now) {
		t.Fatalf("ledger FiredAt = %v, want %v", entry.FiredAt, now)
	}

	res2, err := Run(env.cfg)
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if res2.ShouldFire {
		t.Fatalf("second Run ShouldFire = true, want suppressed (reason=%s)", res2.Reason)
	}
	if res2.Reason != "already-nudged" {
		t.Fatalf("Reason = %q, want already-nudged", res2.Reason)
	}
}

func TestRunRefiresAfterFrontmatterEdit(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	env := newTestEnv(t, now)
	writeTask(t, env, "active", "")

	if _, err := Run(env.cfg); err != nil {
		t.Fatalf("Run 1: %v", err)
	}

	taskFile := filepath.Join(env.cfg.Worktree, "tasks", env.cfg.Slug+".md")
	touch(t, taskFile, now.Add(1*time.Hour))

	res, err := Run(env.cfg)
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if !res.ShouldFire {
		t.Fatalf("ShouldFire after frontmatter touch = false (reason=%s)", res.Reason)
	}
}

type testEnv struct {
	cfg Config
}

func newTestEnv(t *testing.T, now time.Time) *testEnv {
	t.Helper()
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	wtState := filepath.Join(dir, "wt-state")
	ledger := filepath.Join(dir, "ledger")
	tokenDir := filepath.Join(dir, "token")
	mkdir(t, filepath.Join(worktree, "tasks"))
	mkdir(t, wtState)
	mkdir(t, ledger)
	mkdir(t, tokenDir)
	return &testEnv{
		cfg: Config{
			Slug:          "test-slug",
			Worktree:      worktree,
			Project:       "spore",
			WtStateDir:    wtState,
			LedgerDir:     ledger,
			TokenStateDir: tokenDir,
			FleetEnabled:  func() (bool, error) { return true, nil },
			Head:          func() string { return "abc123" },
			Now:           func() time.Time { return now },
		},
	}
}

func writeTask(t *testing.T, env *testEnv, status, body string) {
	t.Helper()
	content := "---\nslug: " + env.cfg.Slug + "\ntitle: test\nstatus: " + status + "\n---\n" + body
	taskFile := filepath.Join(env.cfg.Worktree, "tasks", env.cfg.Slug+".md")
	writeFile(t, taskFile, content)
}

func writeTaskWithExtra(t *testing.T, env *testEnv, status string, extra map[string]string, body string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("---\nslug: ")
	b.WriteString(env.cfg.Slug)
	b.WriteString("\ntitle: test\nstatus: ")
	b.WriteString(status)
	b.WriteByte('\n')
	for k, v := range extra {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	b.WriteString(body)
	taskFile := filepath.Join(env.cfg.Worktree, "tasks", env.cfg.Slug+".md")
	writeFile(t, taskFile, b.String())
}

func writeInbox(t *testing.T, env *testEnv, name, body string) {
	t.Helper()
	dir := filepath.Join(env.cfg.WtStateDir, env.cfg.Slug, "inbox")
	mkdir(t, dir)
	writeFile(t, filepath.Join(dir, name), body)
}

func writeCoordInbox(t *testing.T, env *testEnv, name, body string) {
	t.Helper()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", filepath.Join(env.cfg.Worktree, "..", "coord"))
	dir := filepath.Join(env.cfg.coordinatorInbox())
	mkdir(t, dir)
	writeFile(t, filepath.Join(dir, name), body)
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func touch(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}
