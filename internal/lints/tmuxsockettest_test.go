package lints

import (
	"strings"
	"testing"
)

func TestTmuxSocketTest_FlagsMissingL(t *testing.T) {
	body := `package x

import "os/exec"

func TestX(t *testing.T) {
	_ = exec.Command("tmux", "kill-session", "-t", "name").Run()
}
`
	issues := scanTmuxSocketTest("x_test.go", []byte(body))
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d: %v", len(issues), issues)
	}
	if issues[0].Line != 6 {
		t.Errorf("line: got %d want 6", issues[0].Line)
	}
	if !strings.Contains(issues[0].Message, "-L") {
		t.Errorf("message %q must mention -L", issues[0].Message)
	}
}

func TestTmuxSocketTest_AcceptsL(t *testing.T) {
	body := `package x

import "os/exec"

const sock = "default"

func TestX(t *testing.T) {
	_ = exec.Command("tmux", "-L", sock, "kill-session", "-t", "name").Run()
	_ = exec.Command("tmux", "-L", "spore-test", "new-session").Run()
}
`
	issues := scanTmuxSocketTest("x_test.go", []byte(body))
	if len(issues) != 0 {
		t.Fatalf("expected 0 issues, got %v", issues)
	}
}

func TestTmuxSocketTest_IgnoresNonTmux(t *testing.T) {
	body := `package x

import "os/exec"

func TestX(t *testing.T) {
	_ = exec.Command("git", "status").Run()
	_ = exec.Command("ls").Run()
}
`
	issues := scanTmuxSocketTest("x_test.go", []byte(body))
	if len(issues) != 0 {
		t.Fatalf("non-tmux calls must not flag, got %v", issues)
	}
}

func TestTmuxSocketTest_FlagsMultipleCallsIndependently(t *testing.T) {
	body := `package x

import "os/exec"

func TestX(t *testing.T) {
	_ = exec.Command("tmux", "-L", "s", "has-session").Run()
	_ = exec.Command("tmux", "kill-session").Run()
	_ = exec.Command("tmux", "-L", "s", "kill-session").Run()
	_ = exec.Command("tmux", "list-sessions").Run()
}
`
	issues := scanTmuxSocketTest("x_test.go", []byte(body))
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues (lines 7, 9), got %v", issues)
	}
	if issues[0].Line != 7 || issues[1].Line != 9 {
		t.Errorf("lines: got %d,%d want 7,9", issues[0].Line, issues[1].Line)
	}
}

func TestTmuxSocketTest_RunOnlyTouchesTestFiles(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"prod.go":       "package x\nimport \"os/exec\"\nfunc f() { _ = exec.Command(\"tmux\", \"kill-session\").Run() }\n",
		"clean_test.go": "package x\nimport \"os/exec\"\nfunc TestY() { _ = exec.Command(\"tmux\", \"-L\", \"s\", \"kill-session\").Run() }\n",
		"dirty_test.go": "package x\nimport \"os/exec\"\nfunc TestZ() { _ = exec.Command(\"tmux\", \"kill-session\").Run() }\n",
	})
	issues, err := TmuxSocketTest{}.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue (dirty_test.go only), got %v", issues)
	}
	if issues[0].Path != "dirty_test.go" {
		t.Errorf("path: got %q want dirty_test.go", issues[0].Path)
	}
}

func TestTmuxSocketTest_BacktickStringLiteral(t *testing.T) {
	body := "package x\n\nimport \"os/exec\"\n\nfunc TestX(t *testing.T) {\n\t_ = exec.Command(`tmux`, `kill-session`).Run()\n}\n"
	issues := scanTmuxSocketTest("x_test.go", []byte(body))
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue for backtick `tmux` without -L, got %v", issues)
	}
}
