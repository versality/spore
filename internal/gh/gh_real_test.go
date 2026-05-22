package gh

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// shimGh writes a fake `gh` shell script at <dir>/gh that prints
// scriptBody (via printf when stdout is asked for) and exits with
// exitCode. Argv is appended to <dir>/calls so tests can assert what
// gh was invoked with. The caller is expected to t.Setenv PATH so dir
// wins.
func shimGh(t *testing.T, body string, exitCode int) (binDir, callsFile string) {
	t.Helper()
	binDir = t.TempDir()
	callsFile = filepath.Join(binDir, "calls")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"" + callsFile + "\"\n" +
		"cat <<'GHEOF'\n" +
		body + "\n" +
		"GHEOF\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write gh shim: %v", err)
	}
	return binDir, callsFile
}

// shimGhStderr writes a fake gh that prints body to stderr and exits
// with exitCode. Used for the error / "no pull requests found" path.
func shimGhStderr(t *testing.T, body string, exitCode int) string {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\n" +
		"cat >&2 <<'GHEOF'\n" +
		body + "\n" +
		"GHEOF\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write gh shim: %v", err)
	}
	return binDir
}

func TestRealViewPRSuccess(t *testing.T) {
	binDir, calls := shimGh(t, `{"number":7,"state":"OPEN","mergeable":"MERGEABLE","statusCheckRollup":[{"name":"CI","conclusion":"SUCCESS","status":"COMPLETED"}]}`, 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pr, found, err := Real{}.ViewPR(t.TempDir(), "wt/foo")
	if err != nil {
		t.Fatalf("ViewPR: %v", err)
	}
	if !found {
		t.Fatal("found = false")
	}
	if pr.Number != 7 || pr.State != "OPEN" || pr.Mergeable != "MERGEABLE" || len(pr.Checks) != 1 {
		t.Fatalf("pr wrong: %+v", pr)
	}
	got, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("read calls: %v", err)
	}
	gs := string(got)
	for _, want := range []string{"pr", "view", "wt/foo", "--json", "number,state,mergeable,statusCheckRollup"} {
		if !strings.Contains(gs, want) {
			t.Errorf("calls missing %q in %q", want, gs)
		}
	}
}

func TestRealViewPRNotFound(t *testing.T) {
	binDir := shimGhStderr(t, "no pull requests found for branch wt/foo", 1)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pr, found, err := Real{}.ViewPR(t.TempDir(), "wt/foo")
	if err != nil {
		t.Fatalf("ViewPR: %v", err)
	}
	if found {
		t.Fatalf("found = true on missing PR (pr=%+v)", pr)
	}
}

func TestRealViewPRPropagatesUnknownError(t *testing.T) {
	binDir := shimGhStderr(t, "ssh: connect to host github.com failed", 1)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, _, err := (Real{}).ViewPR(t.TempDir(), "wt/foo"); err == nil {
		t.Fatal("want error for non-not-found failure")
	}
}

func TestRealCreatePRReturnsNumber(t *testing.T) {
	binDir, calls := shimGh(t, "https://github.com/owner/repo/pull/42", 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	n, err := Real{}.CreatePR(t.TempDir(), "wt/foo", "main", "title", "body")
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if n != 42 {
		t.Fatalf("n = %d, want 42", n)
	}
	got, _ := os.ReadFile(calls)
	gs := string(got)
	for _, want := range []string{"pr", "create", "--head", "wt/foo", "--base", "main", "--title", "title", "--body", "body"} {
		if !strings.Contains(gs, want) {
			t.Errorf("calls missing %q in %q", want, gs)
		}
	}
	if strings.Contains(gs, "--fill") {
		t.Errorf("--fill leaked when title+body were supplied: %s", gs)
	}
}

func TestRealCreatePRFillWhenEmpty(t *testing.T) {
	binDir, calls := shimGh(t, "https://github.com/owner/repo/pull/9", 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := (Real{}).CreatePR(t.TempDir(), "wt/foo", "main", "", ""); err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	got, _ := os.ReadFile(calls)
	if !strings.Contains(string(got), "--fill") {
		t.Fatalf("--fill missing when title/body empty: %s", got)
	}
}

func TestRealMergePRSquash(t *testing.T) {
	binDir, calls := shimGh(t, "Merged via squash.", 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := (Real{}).MergePR(t.TempDir(), 42, "squash", true); err != nil {
		t.Fatalf("MergePR: %v", err)
	}
	got, _ := os.ReadFile(calls)
	gs := string(got)
	for _, want := range []string{"pr", "merge", "42", "--squash", "--delete-branch"} {
		if !strings.Contains(gs, want) {
			t.Errorf("calls missing %q in %q", want, gs)
		}
	}
}

func TestRealMergePRRejectsUnknownStrategy(t *testing.T) {
	if err := (Real{}).MergePR(t.TempDir(), 1, "ffwd", false); err == nil {
		t.Fatal("want error on bogus strategy")
	}
}

func TestRealMergePRIdempotentOnAlreadyMerged(t *testing.T) {
	// gh prints "Pull request is already merged" to stdout and exits
	// non-zero; MergePR swallows that.
	binDir := t.TempDir()
	script := "#!/bin/sh\necho 'Pull request is already merged into main'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := (Real{}).MergePR(t.TempDir(), 1, "squash", false); err != nil {
		t.Fatalf("MergePR should swallow already-merged: %v", err)
	}
}

func TestRealListRunsForCommit(t *testing.T) {
	body := `[{"databaseId":1,"name":"CI","status":"completed","conclusion":"success","url":"https://example/run/1","headSha":"abc"},{"databaseId":2,"name":"Stray","status":"completed","conclusion":"failure","url":"https://example/run/2","headSha":"def"}]`
	binDir, calls := shimGh(t, body, 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	runs, err := Real{}.ListRunsForCommit(t.TempDir(), "main", "abc")
	if err != nil {
		t.Fatalf("ListRunsForCommit: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1 (stray sha filtered)", len(runs))
	}
	if runs[0].Status != "COMPLETED" || runs[0].Conclusion != "SUCCESS" {
		t.Errorf("normalisation wrong: %+v", runs[0])
	}
	got, _ := os.ReadFile(calls)
	gs := string(got)
	for _, want := range []string{"run", "list", "--branch", "main", "--commit", "abc"} {
		if !strings.Contains(gs, want) {
			t.Errorf("calls missing %q in %q", want, gs)
		}
	}
}
