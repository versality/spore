package bashnetpositive

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo holds a tiny git fixture: a base commit on main, then optional
// follow-up commits on a feature branch the test diffs against main.
type repo struct {
	t    *testing.T
	root string
}

func newRepo(t *testing.T, base map[string]string) *repo {
	t.Helper()
	r := &repo{t: t, root: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "commit.gpgsign", "false")
	r.write(base)
	r.git("add", "-A")
	r.commit("base")
	r.git("checkout", "-q", "-b", "feature")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.root}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (r *repo) write(files map[string]string) {
	r.t.Helper()
	for p, body := range files {
		full := filepath.Join(r.root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			r.t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			r.t.Fatalf("write %s: %v", full, err)
		}
	}
}

func (r *repo) commit(msg string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
}

// commitMessage stages anything pending and commits with a multi-line
// message (so trailers like allow-bash-net-positive: <reason> end up
// in the commit body).
func (r *repo) commitMessage(body string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", body)
}

func nLines(prefix string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(prefix)
		b.WriteString("\n")
	}
	return b.String()
}

func glueDoc(paths ...string) string {
	var b strings.Builder
	b.WriteString("# harness inventory\n\n")
	b.WriteString("Some preamble.\n\n")
	b.WriteString("## keep-here-glue\n\n")
	for _, p := range paths {
		b.WriteString("- ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	b.WriteString("\n## other-section\n\n- harness/notlisted.sh\n")
	return b.String()
}

// 1: no .sh changes -> pass.
func TestRun_NoShChanges_Pass(t *testing.T) {
	r := newRepo(t, map[string]string{
		"harness/keep.sh":           "echo hi\n",
		"docs/harness-inventory.md": glueDoc("harness/keep.sh"),
	})
	r.write(map[string]string{"README.md": "doc change\n"})
	r.commit("docs only")

	res, err := Run(r.root, "main")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Verdict != Pass {
		t.Fatalf("verdict: got %q want %q (res=%+v)", res.Verdict, Pass, res)
	}
	if res.NetBashLoc != 0 {
		t.Fatalf("netBashLoc: got %d want 0", res.NetBashLoc)
	}
}

// 2: +30 LOC to harness/foo.sh -> refuse (net positive).
func TestRun_NetPositive_Refuse(t *testing.T) {
	r := newRepo(t, map[string]string{
		"harness/foo.sh":            "echo seed\n",
		"docs/harness-inventory.md": glueDoc("harness/foo.sh"),
	})
	r.write(map[string]string{"harness/foo.sh": "echo seed\n" + nLines("echo x", 30)})
	r.commit("grow foo.sh")

	res, err := Run(r.root, "main")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Verdict != Refuse {
		t.Fatalf("verdict: got %q want %q (res=%+v)", res.Verdict, Refuse, res)
	}
	if res.NetBashLoc != 30 {
		t.Fatalf("netBashLoc: got %d want 30", res.NetBashLoc)
	}
}

// 3: new harness/foo.sh not in glue list -> refuse.
func TestRun_NewBashFile_NotGlued_Refuse(t *testing.T) {
	r := newRepo(t, map[string]string{
		"harness/keep.sh":           "echo hi\n",
		"docs/harness-inventory.md": glueDoc("harness/keep.sh"),
	})
	// Net negative LOC by removing 5 lines from keep.sh and adding a
	// brand-new 1-line foo.sh (+1 -5 = -4) so the new-file check is
	// the only signal driving the refuse.
	r.write(map[string]string{
		"harness/keep.sh": "",
	})
	r.commitMessage("trim keep.sh")
	r.write(map[string]string{
		"harness/foo.sh": "echo new\n",
	})
	// Pad keep.sh back so net is negative for harness/.
	r.write(map[string]string{
		"harness/keep.sh": "",
	})
	r.commit("add foo.sh")

	res, err := Run(r.root, "main")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Verdict != Refuse {
		t.Fatalf("verdict: got %q want %q (res=%+v)", res.Verdict, Refuse, res)
	}
	if len(res.RefusedNewFiles) != 1 || res.RefusedNewFiles[0] != "harness/foo.sh" {
		t.Fatalf("refusedNewFiles: got %v want [harness/foo.sh]", res.RefusedNewFiles)
	}
}

// 4: new harness/foo.sh listed under keep-here-glue -> pass (net negative).
func TestRun_NewBashFile_Glued_Pass(t *testing.T) {
	r := newRepo(t, map[string]string{
		"harness/keep.sh":           nLines("echo seed", 10),
		"docs/harness-inventory.md": glueDoc("harness/keep.sh", "harness/foo.sh"),
	})
	// Add new file but keep net LOC <= 0 by removing more from keep.sh.
	r.write(map[string]string{
		"harness/keep.sh": nLines("echo seed", 5),
		"harness/foo.sh":  "echo glue\n",
	})
	r.commit("add glue foo.sh, trim keep.sh")

	res, err := Run(r.root, "main")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Verdict != Pass {
		t.Fatalf("verdict: got %q want %q (res=%+v)", res.Verdict, Pass, res)
	}
	if len(res.AllowedNewFiles) != 1 || res.AllowedNewFiles[0] != "harness/foo.sh" {
		t.Fatalf("allowedNewFiles: got %v want [harness/foo.sh]", res.AllowedNewFiles)
	}
}

// 5: allow-bash-net-positive: <reason> in commit body -> override-applied.
func TestRun_OverrideApplied(t *testing.T) {
	r := newRepo(t, map[string]string{
		"harness/foo.sh":            "echo seed\n",
		"docs/harness-inventory.md": glueDoc("harness/foo.sh"),
	})
	r.write(map[string]string{"harness/foo.sh": "echo seed\n" + nLines("echo x", 30)})
	r.commitMessage("grow foo.sh\n\nallow-bash-net-positive: one-shot vendoring of upstream\n")

	res, err := Run(r.root, "main")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Verdict != OverrideApplied {
		t.Fatalf("verdict: got %q want %q (res=%+v)", res.Verdict, OverrideApplied, res)
	}
	if !strings.Contains(res.Override, "vendoring") {
		t.Fatalf("override reason: got %q", res.Override)
	}
}

// 6: missing inventory -> graceful: pass for net-zero/negative,
// refuse for net-positive, both with a "no inventory" warning.
func TestRun_MissingInventory(t *testing.T) {
	t.Run("net-zero passes with warning", func(t *testing.T) {
		r := newRepo(t, map[string]string{
			"harness/foo.sh": "echo seed\n",
		})
		r.write(map[string]string{"README.md": "doc change\n"})
		r.commit("docs only, no inventory")

		res, err := Run(r.root, "main")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.Verdict != Pass {
			t.Fatalf("verdict: got %q want %q (res=%+v)", res.Verdict, Pass, res)
		}
		if !res.NoInventory {
			t.Fatalf("expected NoInventory=true")
		}
		if !hasWarn(res.Warnings, "harness-inventory.md not found") {
			t.Fatalf("expected no-inventory warning, got %v", res.Warnings)
		}
	})
	t.Run("net-positive refuses with warning", func(t *testing.T) {
		r := newRepo(t, map[string]string{
			"harness/foo.sh": "echo seed\n",
		})
		r.write(map[string]string{"harness/foo.sh": "echo seed\n" + nLines("echo x", 30)})
		r.commit("grow foo.sh, no inventory")

		res, err := Run(r.root, "main")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.Verdict != Refuse {
			t.Fatalf("verdict: got %q want %q (res=%+v)", res.Verdict, Refuse, res)
		}
		if !res.NoInventory {
			t.Fatalf("expected NoInventory=true")
		}
		if !hasWarn(res.Warnings, "harness-inventory.md not found") {
			t.Fatalf("expected no-inventory warning, got %v", res.Warnings)
		}
	})
}

// New harness file with missing inventory must NOT refuse on the
// new-file check alone: the LOC check is the only signal in that
// fallback path. Net-zero (one-line new file balanced by removal
// elsewhere) should still pass, with a warning.
func TestRun_MissingInventory_NewFileNetZero_Pass(t *testing.T) {
	r := newRepo(t, map[string]string{
		"harness/keep.sh": nLines("echo seed", 5),
	})
	r.write(map[string]string{
		"harness/keep.sh": "",
		"harness/new.sh":  "echo only\n",
	})
	r.commit("rotate scripts")

	res, err := Run(r.root, "main")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Verdict != Pass {
		t.Fatalf("verdict: got %q want %q (res=%+v)", res.Verdict, Pass, res)
	}
}

// matchOverride is unit-tested at line granularity since the real
// path traverses git log.
func TestMatchOverride(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"allow-bash-net-positive: one-shot", "one-shot"},
		{"  allow-bash-net-positive:  spaces ", "spaces"},
		{"Allow-Bash-Net-Positive: case", "case"},
		{"allow-bash-net-positive:", ""},
		{"# allow-bash-net-positive: leading hash", ""},
		{"allow-bash-net-positivesomething: no", ""},
		{"unrelated: line", ""},
	}
	for _, tc := range cases {
		got, ok := matchOverride(tc.line)
		if tc.want == "" && ok {
			t.Errorf("matchOverride(%q) = (%q, true), want no match", tc.line, got)
		}
		if tc.want != "" && (!ok || got != tc.want) {
			t.Errorf("matchOverride(%q) = (%q, %v), want (%q, true)", tc.line, got, ok, tc.want)
		}
	}
}

func TestExtractPath(t *testing.T) {
	cases := map[string]string{
		"harness/foo.sh":         "harness/foo.sh",
		"- harness/foo.sh":       "harness/foo.sh",
		"* `harness/foo.sh`":     "harness/foo.sh",
		"+ harness/foo.sh notes": "harness/foo.sh",
		"harness/foo.py":         "",
		"docs/foo.sh":            "",
		"":                       "",
	}
	for in, want := range cases {
		if got := extractPath(in); got != want {
			t.Errorf("extractPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func hasWarn(warns []string, sub string) bool {
	for _, w := range warns {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
