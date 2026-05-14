package fleet

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeReapTmux is a stub tmux runner for reap_test.
type fakeReapTmux struct {
	sessions    map[string]bool
	listOut     string
	killed      []string
	hasFunc     func(string) bool
	killErr     map[string]error
	listSessErr error
}

func (f *fakeReapTmux) listSessions() (string, error) {
	if f.listSessErr != nil {
		return "", f.listSessErr
	}
	return f.listOut, nil
}

func (f *fakeReapTmux) hasSession(name string) bool {
	if f.hasFunc != nil {
		return f.hasFunc(name)
	}
	return f.sessions[name]
}

func (f *fakeReapTmux) killSession(name string) error {
	if f.killErr[name] != nil {
		return f.killErr[name]
	}
	f.killed = append(f.killed, name)
	delete(f.sessions, name)
	return nil
}

// fakeGit drives reap's git calls in-memory. Lookups are by argv joined
// with spaces.
type fakeGit struct {
	responses map[string]gitResp
	calls     []string
}

type gitResp struct {
	out string
	err error
}

func (g *fakeGit) run(dir string, args ...string) (string, error) {
	key := strings.Join(args, " ")
	g.calls = append(g.calls, key)
	resp, ok := g.responses[key]
	if !ok {
		return "", fmt.Errorf("fakeGit: no response for %q", key)
	}
	return resp.out, resp.err
}

func writeReapTaskFile(t *testing.T, root, slug, status string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nslug: " + slug + "\nstatus: " + status + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(root, "tasks", slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newReapSetup(t *testing.T) (root, projectsFile string) {
	t.Helper()
	tmp := t.TempDir()
	root = filepath.Join(tmp, "p", "project")
	if err := os.MkdirAll(filepath.Join(root, ".worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	projectsFile = filepath.Join(tmp, "projects")
	if err := os.WriteFile(projectsFile, []byte(root+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, projectsFile
}

func mkWorktreeDir(t *testing.T, root, slug string) string {
	t.Helper()
	wt := filepath.Join(root, ".worktrees", slug)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	return wt
}

func TestReapDoneTaskRemovesWorktreeAndBranch(t *testing.T) {
	root, projectsFile := newReapSetup(t)
	wt := mkWorktreeDir(t, root, "done-slug")
	writeReapTaskFile(t, root, "done-slug", "done")

	git := &fakeGit{responses: map[string]gitResp{
		"rev-parse --verify --quiet wt/done-slug":             {out: "abc\n"},
		"branch -d wt/done-slug":                              {out: "Deleted branch wt/done-slug.\n"},
		"worktree remove --force " + wt:                       {out: ""},
		"worktree prune":                                      {out: ""},
		"rev-parse --verify --quiet refs/remotes/origin/main": {err: errors.New("no origin/main")},
	}}
	tmux := &fakeReapTmux{
		sessions: map[string]bool{},
		listOut:  "",
	}

	e := reapEnv{
		projectsFile:  projectsFile,
		currentRoot:   func() (string, error) { return root, nil },
		gitRunner:     git.run,
		tmuxRunner:    tmux,
		listWorktrees: func(_ string) ([]string, error) { return []string{wt}, nil },
	}

	var stdout, stderr bytes.Buffer
	rc, err := runReap(false, &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runReap: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "removed worktree") {
		t.Errorf("expected removed worktree, stdout=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "deleted branch wt/done-slug") {
		t.Errorf("expected branch delete, stdout=%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "removed=1 branches=1") {
		t.Errorf("summary mismatch: %s", stdout.String())
	}
}

func TestReapActiveTaskIsNoOp(t *testing.T) {
	root, projectsFile := newReapSetup(t)
	wt := mkWorktreeDir(t, root, "active-slug")
	writeReapTaskFile(t, root, "active-slug", "active")

	git := &fakeGit{responses: map[string]gitResp{
		"worktree prune": {out: ""},
	}}
	tmux := &fakeReapTmux{sessions: map[string]bool{}, listOut: ""}

	e := reapEnv{
		projectsFile:  projectsFile,
		currentRoot:   func() (string, error) { return root, nil },
		gitRunner:     git.run,
		tmuxRunner:    tmux,
		listWorktrees: func(_ string) ([]string, error) { return []string{wt}, nil },
	}

	var stdout, stderr bytes.Buffer
	rc, err := runReap(false, &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runReap: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(stdout.String(), "nothing to do") {
		t.Errorf("expected no-op, got %s", stdout.String())
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("active worktree dir unexpectedly missing: %v", err)
	}
}

func TestReapBlockedKillsSessionKeepsWorktree(t *testing.T) {
	root, projectsFile := newReapSetup(t)
	wt := mkWorktreeDir(t, root, "blocked-slug")
	session := "🐝 project/blocked-slug [opus]"
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nslug: blocked-slug\nstatus: blocked\nsession: " + session + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(root, "tasks", "blocked-slug.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	git := &fakeGit{responses: map[string]gitResp{
		"worktree prune": {out: ""},
	}}
	tmux := &fakeReapTmux{
		sessions: map[string]bool{session: true},
		listOut:  "",
	}

	e := reapEnv{
		projectsFile:  projectsFile,
		currentRoot:   func() (string, error) { return root, nil },
		gitRunner:     git.run,
		tmuxRunner:    tmux,
		listWorktrees: func(_ string) ([]string, error) { return []string{wt}, nil },
	}

	var stdout, stderr bytes.Buffer
	rc, err := runReap(false, &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runReap: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if len(tmux.killed) != 1 || tmux.killed[0] != session {
		t.Fatalf("expected blocked session killed, got %v", tmux.killed)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("blocked worktree must be preserved: %v", err)
	}
	if !strings.Contains(stdout.String(), "killed=1 removed=0 branches=0") {
		t.Errorf("summary mismatch: %s", stdout.String())
	}
}

func TestReapOrphanFileMissingWithUnlandedCommitsPreserved(t *testing.T) {
	root, projectsFile := newReapSetup(t)
	wt := mkWorktreeDir(t, root, "orphan-with-work")
	// No tasks/<slug>.md on main.

	git := &fakeGit{responses: map[string]gitResp{
		"rev-parse --verify --quiet wt/orphan-with-work":      {out: "abc\n"},
		"merge-base --is-ancestor wt/orphan-with-work HEAD":   {err: errors.New("not ancestor")},
		"rev-parse --verify --quiet refs/remotes/origin/main": {err: errors.New("no origin/main")},
		"worktree prune": {out: ""},
	}}
	tmux := &fakeReapTmux{sessions: map[string]bool{}, listOut: ""}

	e := reapEnv{
		projectsFile:  projectsFile,
		currentRoot:   func() (string, error) { return root, nil },
		gitRunner:     git.run,
		tmuxRunner:    tmux,
		listWorktrees: func(_ string) ([]string, error) { return []string{wt}, nil },
	}

	var stdout, stderr bytes.Buffer
	rc, err := runReap(false, &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runReap: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("expected worktree preserved (unlanded commits sentinel), got: %v", err)
	}
	if !strings.Contains(stderr.String(), "preserving worktree+session+branch") {
		t.Errorf("expected sentinel warning, got %s", stderr.String())
	}
}

func TestReapPass2KillsOrphanTmuxSession(t *testing.T) {
	root, projectsFile := newReapSetup(t)
	// No worktrees on disk. Tmux holds a session for a slug whose
	// .worktrees/<slug> is gone.
	orphan := "🐝 project/orphan [opus]"
	tmux := &fakeReapTmux{
		sessions: map[string]bool{orphan: true},
		listOut:  orphan + "\n",
	}
	git := &fakeGit{responses: map[string]gitResp{
		"worktree prune": {out: ""},
	}}

	e := reapEnv{
		projectsFile:  projectsFile,
		currentRoot:   func() (string, error) { return root, nil },
		gitRunner:     git.run,
		tmuxRunner:    tmux,
		listWorktrees: func(_ string) ([]string, error) { return nil, nil },
	}

	var stdout, stderr bytes.Buffer
	rc, err := runReap(false, &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runReap: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, stderr.String())
	}
	if len(tmux.killed) != 1 || tmux.killed[0] != orphan {
		t.Fatalf("expected orphan session killed, got %v", tmux.killed)
	}
	if !strings.Contains(stdout.String(), "killed orphan session "+orphan) {
		t.Errorf("expected orphan log, got %s", stdout.String())
	}
}

func TestReapForcePublishedClosesActiveTask(t *testing.T) {
	root, projectsFile := newReapSetup(t)
	wt := mkWorktreeDir(t, root, "active-pub")
	writeReapTaskFile(t, root, "active-pub", "active")

	publishedBody := "---\nslug: active-pub\nstatus: done\n---\n"

	git := &fakeGit{responses: map[string]gitResp{
		"rev-parse --verify --quiet refs/remotes/origin/main":             {out: "ok\n"},
		"merge-base --is-ancestor wt/active-pub refs/remotes/origin/main": {out: ""},
		"show refs/remotes/origin/main:tasks/active-pub.md":               {out: publishedBody},
		"rev-parse --verify --quiet wt/active-pub":                        {out: "abc\n"},
		"branch -d wt/active-pub":                                         {out: "Deleted.\n"},
		"worktree remove --force " + wt:                                   {out: ""},
		"worktree prune":                                                  {out: ""},
	}}
	tmux := &fakeReapTmux{sessions: map[string]bool{}, listOut: ""}

	e := reapEnv{
		projectsFile:  projectsFile,
		currentRoot:   func() (string, error) { return root, nil },
		gitRunner:     git.run,
		tmuxRunner:    tmux,
		listWorktrees: func(_ string) ([]string, error) { return []string{wt}, nil },
	}

	var stdout, stderr bytes.Buffer
	rc, err := runReap(true, &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runReap: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "removed worktree") {
		t.Errorf("expected force-published cleanup, got %s", stdout.String())
	}
}

func TestReapForceDeletesBranchContainedInOriginMain(t *testing.T) {
	root, projectsFile := newReapSetup(t)
	wt := mkWorktreeDir(t, root, "merged-slug")
	writeReapTaskFile(t, root, "merged-slug", "done")

	git := &fakeGit{responses: map[string]gitResp{
		"rev-parse --verify --quiet wt/merged-slug":                        {out: "abc\n"},
		"branch -d wt/merged-slug":                                         {err: errors.New("not fully merged into HEAD")},
		"rev-parse --verify --quiet refs/remotes/origin/main":              {out: "ok\n"},
		"merge-base --is-ancestor wt/merged-slug refs/remotes/origin/main": {out: ""},
		"branch -D wt/merged-slug":                                         {out: "Deleted (force).\n"},
		"worktree remove --force " + wt:                                    {out: ""},
		"worktree prune":                                                   {out: ""},
	}}
	tmux := &fakeReapTmux{sessions: map[string]bool{}, listOut: ""}

	e := reapEnv{
		projectsFile:  projectsFile,
		currentRoot:   func() (string, error) { return root, nil },
		gitRunner:     git.run,
		tmuxRunner:    tmux,
		listWorktrees: func(_ string) ([]string, error) { return []string{wt}, nil },
	}

	var stdout, stderr bytes.Buffer
	rc, err := runReap(false, &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runReap: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(stdout.String(), "contained in origin/main") {
		t.Errorf("expected force-delete via origin/main path, got %s", stdout.String())
	}
}

func TestReapUnmergedBranchIsKept(t *testing.T) {
	root, projectsFile := newReapSetup(t)
	wt := mkWorktreeDir(t, root, "unmerged-slug")
	writeReapTaskFile(t, root, "unmerged-slug", "done")

	git := &fakeGit{responses: map[string]gitResp{
		"rev-parse --verify --quiet wt/unmerged-slug":         {out: "abc\n"},
		"branch -d wt/unmerged-slug":                          {err: errors.New("not fully merged")},
		"rev-parse --verify --quiet refs/remotes/origin/main": {err: errors.New("no origin/main")},
		"worktree remove --force " + wt:                       {out: ""},
		"worktree prune":                                      {out: ""},
	}}
	tmux := &fakeReapTmux{sessions: map[string]bool{}, listOut: ""}

	e := reapEnv{
		projectsFile:  projectsFile,
		currentRoot:   func() (string, error) { return root, nil },
		gitRunner:     git.run,
		tmuxRunner:    tmux,
		listWorktrees: func(_ string) ([]string, error) { return []string{wt}, nil },
	}

	var stdout, stderr bytes.Buffer
	rc, err := runReap(false, &stdout, &stderr, e)
	if err != nil {
		t.Fatalf("runReap: %v", err)
	}
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(stderr.String(), "not merged into main; keeping") {
		t.Errorf("expected branch-kept warning, got %s", stderr.String())
	}
	if strings.Contains(stdout.String(), "branches=1") {
		t.Errorf("expected zero branch deletes, got %s", stdout.String())
	}
}
