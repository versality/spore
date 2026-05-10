// Package unblock encapsulates the "ff-merge drift unblock" pattern:
// when the main worktree's tasks/<slug>.md drifts from the rower's
// wt-branch view, probe whether the file is tracked on main, pick
// `git checkout` (tracked) vs `rm` (untracked), then ping the rower
// to retry. Refuses when the working-tree content matches neither
// HEAD nor the wt-branch (= genuine local work, not drift).
package unblock

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Action is the chosen mitigation.
type Action string

const (
	ActionNoop     Action = "noop"
	ActionCheckout Action = "checkout"
	ActionRemove   Action = "rm"
	ActionRefuse   Action = "refuse"
)

// State is the pre-decision repo view for the candidate file.
type State struct {
	FileExists bool
	Tracked    bool
	HasBranch  bool
	WTHash     string // hash-object of working tree, "" if absent
	HeadHash   string // HEAD:tasks/<slug>.md, "" if not tracked / missing
	BranchHash string // wt/<slug>:tasks/<slug>.md, "" if absent
	WTBranch   string // branch name (e.g. wt/foo) for the refuse message
}

// Decide is the pure verdict. Returns the chosen Action plus a one-
// line reason. ActionRefuse means the caller must abort (exit 2).
func Decide(s State) (Action, string) {
	if s.FileExists {
		if s.Tracked {
			if s.WTHash == s.HeadHash {
				return ActionNoop, "working tree already matches HEAD; no drift to clear"
			}
			if s.BranchHash != "" && s.WTHash == s.BranchHash {
				return ActionCheckout, fmt.Sprintf("working tree matches %s; safe to checkout HEAD's version", s.WTBranch)
			}
		} else {
			if s.BranchHash != "" && s.WTHash == s.BranchHash {
				return ActionRemove, fmt.Sprintf("untracked file matches %s; safe to remove", s.WTBranch)
			}
		}
	} else {
		if s.Tracked {
			return ActionCheckout, "file missing from working tree; restoring from HEAD"
		}
		return ActionNoop, "file not present and not tracked; nothing to clear"
	}
	return ActionRefuse, ""
}

// Repo is the side-effecting interface. Implementations may shell
// out (LocalRepo) or fake the calls (tests).
type Repo interface {
	State(slug string) (State, error)
	Checkout(file string) error
	Remove(file string) error
	Tell(slug, body string) error
	MainRoot() string
}

// Run executes one unblock pass for slug. Output is written to w.
// Returns 0 on success, 2 on refusal, 1 on hard error.
func Run(r Repo, slug string, w io.Writer) (int, error) {
	if slug == "" {
		return 1, fmt.Errorf("usage: spore merge unblock <slug>")
	}
	st, err := r.State(slug)
	if err != nil {
		return 1, err
	}
	action, reason := Decide(st)
	file := filepath.Join("tasks", slug+".md")

	if action == ActionRefuse {
		fmt.Fprintf(w, "[wt-merge-unblock] refusing: %s has uncommitted content that matches neither HEAD nor %s.\n", file, st.WTBranch)
		fmt.Fprintln(w, "This looks like genuine local work, not drift. Inspect manually:")
		fmt.Fprintf(w, "  git -C %s diff -- %s\n", r.MainRoot(), file)
		fmt.Fprintf(w, "  git -C %s diff %s -- %s\n", r.MainRoot(), st.WTBranch, file)
		return 2, nil
	}

	switch action {
	case ActionCheckout:
		if err := r.Checkout(file); err != nil {
			return 1, err
		}
	case ActionRemove:
		if err := r.Remove(file); err != nil {
			return 1, err
		}
	case ActionNoop:
		// nothing to do
	}

	if err := r.Tell(slug, "drift cleared, retry wt merge"); err != nil {
		fmt.Fprintln(w, "[wt-merge-unblock] wt task tell failed (non-fatal)")
	}
	fmt.Fprintf(w, "[wt-merge-unblock] %s: %s (%s); pinged rower\n", slug, action, reason)
	return 0, nil
}

// LocalRepo drives the unblock against a real repo at Root.
type LocalRepo struct {
	Root string
}

func (l LocalRepo) MainRoot() string { return l.Root }

func (l LocalRepo) State(slug string) (State, error) {
	file := filepath.Join("tasks", slug+".md")
	branch := "wt/" + slug
	st := State{WTBranch: branch}

	if _, err := os.Lstat(filepath.Join(l.Root, file)); err == nil {
		st.FileExists = true
	}

	if l.git("ls-files", "--error-unmatch", "--", file) == nil {
		st.Tracked = true
	}
	if l.git("rev-parse", "--verify", "--quiet", branch) == nil {
		st.HasBranch = true
	}
	if st.FileExists {
		if out, err := l.gitOut("hash-object", "--", file); err == nil {
			st.WTHash = strings.TrimSpace(out)
		}
	}
	if st.Tracked {
		if out, err := l.gitOut("rev-parse", "HEAD:"+file); err == nil {
			st.HeadHash = strings.TrimSpace(out)
		}
	}
	if st.HasBranch {
		if out, err := l.gitOut("rev-parse", branch+":"+file); err == nil {
			st.BranchHash = strings.TrimSpace(out)
		}
	}
	return st, nil
}

func (l LocalRepo) Checkout(file string) error {
	return l.git("checkout", "--", file)
}

func (l LocalRepo) Remove(file string) error {
	return os.Remove(filepath.Join(l.Root, file))
}

func (l LocalRepo) Tell(slug, body string) error {
	cmd := exec.Command("wt", "task", "tell", slug, body)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func (l LocalRepo) git(args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", l.Root}, args...)...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func (l LocalRepo) gitOut(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", l.Root}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}
