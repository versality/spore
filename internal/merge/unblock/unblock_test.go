package unblock

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestDecide(t *testing.T) {
	cases := []struct {
		name   string
		s      State
		want   Action
		reason string
	}{
		{
			name: "exists, tracked, matches HEAD -> noop",
			s: State{
				FileExists: true, Tracked: true, HasBranch: true, WTBranch: "wt/foo",
				WTHash: "H", HeadHash: "H", BranchHash: "B",
			},
			want:   ActionNoop,
			reason: "matches HEAD",
		},
		{
			name: "exists, tracked, matches branch -> checkout",
			s: State{
				FileExists: true, Tracked: true, HasBranch: true, WTBranch: "wt/foo",
				WTHash: "B", HeadHash: "H", BranchHash: "B",
			},
			want:   ActionCheckout,
			reason: "matches wt/foo",
		},
		{
			name: "exists, tracked, matches neither -> refuse",
			s: State{
				FileExists: true, Tracked: true, HasBranch: true, WTBranch: "wt/foo",
				WTHash: "X", HeadHash: "H", BranchHash: "B",
			},
			want: ActionRefuse,
		},
		{
			name: "exists, untracked, matches branch -> rm",
			s: State{
				FileExists: true, Tracked: false, HasBranch: true, WTBranch: "wt/foo",
				WTHash: "B", BranchHash: "B",
			},
			want:   ActionRemove,
			reason: "untracked file matches wt/foo",
		},
		{
			name: "exists, untracked, no branch hash -> refuse",
			s: State{
				FileExists: true, Tracked: false, HasBranch: false, WTBranch: "wt/foo",
				WTHash: "X",
			},
			want: ActionRefuse,
		},
		{
			name: "missing, tracked -> checkout",
			s: State{
				FileExists: false, Tracked: true, WTBranch: "wt/foo",
			},
			want:   ActionCheckout,
			reason: "missing from working tree",
		},
		{
			name: "missing, untracked -> noop",
			s: State{
				FileExists: false, Tracked: false, WTBranch: "wt/foo",
			},
			want:   ActionNoop,
			reason: "not present and not tracked",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := Decide(c.s)
			if got != c.want {
				t.Errorf("got %s, want %s (reason=%q)", got, c.want, reason)
			}
			if c.reason != "" && !strings.Contains(reason, c.reason) {
				t.Errorf("reason = %q, want substring %q", reason, c.reason)
			}
		})
	}
}

// fakeRepo records every call so we can assert ordering.
type fakeRepo struct {
	state    State
	stateErr error
	checkout []string
	rm       []string
	tells    []string
	tellErr  error
	root     string
}

func (f *fakeRepo) MainRoot() string { return f.root }
func (f *fakeRepo) State(slug string) (State, error) {
	return f.state, f.stateErr
}
func (f *fakeRepo) Checkout(file string) error { f.checkout = append(f.checkout, file); return nil }
func (f *fakeRepo) Remove(file string) error   { f.rm = append(f.rm, file); return nil }
func (f *fakeRepo) Tell(slug, body string) error {
	f.tells = append(f.tells, slug+":"+body)
	return f.tellErr
}

func TestRun_CheckoutPath(t *testing.T) {
	r := &fakeRepo{
		root: "/repo",
		state: State{
			FileExists: true, Tracked: true, HasBranch: true, WTBranch: "wt/foo",
			WTHash: "B", HeadHash: "H", BranchHash: "B",
		},
	}
	var buf bytes.Buffer
	code, err := Run(r, "foo", &buf)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if len(r.checkout) != 1 || r.checkout[0] != "tasks/foo.md" {
		t.Errorf("checkout = %v", r.checkout)
	}
	if len(r.rm) != 0 {
		t.Errorf("rm should be empty: %v", r.rm)
	}
	if len(r.tells) != 1 || !strings.Contains(r.tells[0], "drift cleared") {
		t.Errorf("tells = %v", r.tells)
	}
}

func TestRun_RemovePath(t *testing.T) {
	r := &fakeRepo{
		root: "/repo",
		state: State{
			FileExists: true, Tracked: false, HasBranch: true, WTBranch: "wt/foo",
			WTHash: "B", BranchHash: "B",
		},
	}
	var buf bytes.Buffer
	code, err := Run(r, "foo", &buf)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if len(r.rm) != 1 || r.rm[0] != "tasks/foo.md" {
		t.Errorf("rm = %v", r.rm)
	}
}

func TestRun_RefusalReturns2_NoSideEffects(t *testing.T) {
	r := &fakeRepo{
		root: "/repo",
		state: State{
			FileExists: true, Tracked: true, HasBranch: true, WTBranch: "wt/foo",
			WTHash: "X", HeadHash: "H", BranchHash: "B",
		},
	}
	var buf bytes.Buffer
	code, err := Run(r, "foo", &buf)
	if err != nil || code != 2 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if len(r.checkout) != 0 || len(r.rm) != 0 || len(r.tells) != 0 {
		t.Errorf("refusal must not touch repo: checkout=%v rm=%v tells=%v",
			r.checkout, r.rm, r.tells)
	}
	out := buf.String()
	if !strings.Contains(out, "refusing") || !strings.Contains(out, "wt/foo") {
		t.Errorf("refusal message off: %s", out)
	}
}

func TestRun_TellFailureNonFatal(t *testing.T) {
	r := &fakeRepo{
		root: "/repo",
		state: State{
			FileExists: true, Tracked: true, HasBranch: true, WTBranch: "wt/foo",
			WTHash: "H", HeadHash: "H", BranchHash: "B",
		},
		tellErr: errors.New("wt missing"),
	}
	var buf bytes.Buffer
	code, err := Run(r, "foo", &buf)
	if err != nil || code != 0 {
		t.Fatalf("tell failure must be non-fatal: code=%d err=%v", code, err)
	}
	if !strings.Contains(buf.String(), "wt task tell failed") {
		t.Errorf("expected non-fatal warning: %s", buf.String())
	}
}

func TestRun_EmptySlugErrors(t *testing.T) {
	r := &fakeRepo{root: "/repo"}
	var buf bytes.Buffer
	code, err := Run(r, "", &buf)
	if err == nil {
		t.Errorf("expected error on empty slug")
	}
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
}
