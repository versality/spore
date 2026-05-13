package consumerclaim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseClaim(t *testing.T) {
	cases := []struct {
		in   string
		want Claim
		err  bool
	}{
		{"nix-config:path:modules/skyhelm/totalsize.sh", Claim{Repo: "nix-config", Kind: KindPath, Value: "modules/skyhelm/totalsize.sh"}, false},
		{"nix-config:grep:skyhelm-rower-watch", Claim{Repo: "nix-config", Kind: KindGrep, Value: "skyhelm-rower-watch"}, false},
		{"  nix-config : grep : foo  ", Claim{Repo: "nix-config", Kind: KindGrep, Value: "foo"}, false},
		// values with colons survive (SplitN 3)
		{"nix-config:grep:http://example.com/foo", Claim{Repo: "nix-config", Kind: KindGrep, Value: "http://example.com/foo"}, false},
		{"nix-config:walk:foo", Claim{}, true},
		{"nix-config:path:", Claim{}, true},
		{":path:foo", Claim{}, true},
		{"nix-config:path", Claim{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseClaim(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseClaim: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveRepoPath(t *testing.T) {
	deps := Deps{
		LookupEnv: func(k string) (string, bool) {
			if k == "SPORE_CONSUMER_NIX_CONFIG" {
				return "/override/nix-config", true
			}
			return "", false
		},
		HomeDir: func() (string, error) { return "/home/sky", nil },
	}
	got, err := ResolveRepoPath("nix-config", deps)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/override/nix-config" {
		t.Errorf("override path lost: got %q", got)
	}

	// Fallback to ~/projects/<repo>.
	deps2 := Deps{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return "/home/sky", nil },
	}
	got, err = ResolveRepoPath("nix-config", deps2)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/sky/projects/nix-config" {
		t.Errorf("fallback path wrong: %q", got)
	}
}

func TestScanPathResolved(t *testing.T) {
	repo := t.TempDir()
	// Claim says modules/totalsize.sh must NOT exist; we did not create it.
	claims := []Claim{{Repo: "demo", Kind: KindPath, Value: "modules/totalsize.sh"}}
	res := Scan(claims, depsAt(repo))
	if len(res) != 1 || res[0].Status != StatusResolved {
		t.Fatalf("want resolved, got %+v", res)
	}
}

func TestScanPathUnresolved(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "modules", "totalsize.sh"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	claims := []Claim{{Repo: "demo", Kind: KindPath, Value: "modules/totalsize.sh"}}
	res := Scan(claims, depsAt(repo))
	if res[0].Status != StatusUnresolved {
		t.Fatalf("want unresolved, got %+v", res)
	}
	if !strings.HasSuffix(res[0].Detail, "modules/totalsize.sh") {
		t.Errorf("Detail = %q, want suffix modules/totalsize.sh", res[0].Detail)
	}
}

func TestScanGrep(t *testing.T) {
	repo := t.TempDir()
	deps := depsAt(repo)
	deps.Grep = func(r, pattern string) (string, error) {
		if r != repo {
			t.Fatalf("repo = %q", r)
		}
		if pattern == "still-here" {
			return "modules/foo.sh", nil
		}
		return "", nil
	}
	res := Scan([]Claim{
		{Repo: "demo", Kind: KindGrep, Value: "gone"},
		{Repo: "demo", Kind: KindGrep, Value: "still-here"},
	}, deps)
	if res[0].Status != StatusResolved {
		t.Errorf("res[0] want resolved, got %+v", res[0])
	}
	if res[1].Status != StatusUnresolved || res[1].Detail != "modules/foo.sh" {
		t.Errorf("res[1] = %+v", res[1])
	}
}

func TestScanSkippedNoCheckout(t *testing.T) {
	// HomeDir points to a temp dir, but no projects/<repo> is created.
	home := t.TempDir()
	deps := Deps{
		LookupEnv: func(string) (string, bool) { return "", false },
		HomeDir:   func() (string, error) { return home, nil },
		Stat:      os.Stat,
	}
	res := Scan([]Claim{{Repo: "ghost-repo", Kind: KindPath, Value: "x"}}, deps)
	if res[0].Status != StatusSkipped {
		t.Fatalf("want skipped, got %+v", res[0])
	}
	if !strings.Contains(res[0].Detail, "missing at") {
		t.Errorf("Detail = %q, want 'missing at' substring", res[0].Detail)
	}
}

func TestAnyUnresolved(t *testing.T) {
	if !AnyUnresolved([]Result{{Status: StatusUnresolved}}) {
		t.Errorf("unresolved should report unresolved")
	}
	if !AnyUnresolved([]Result{{Status: StatusSkipped}}) {
		t.Errorf("skipped should report unresolved (cannot prove clean)")
	}
	if AnyUnresolved([]Result{{Status: StatusResolved}, {Status: StatusResolved}}) {
		t.Errorf("all resolved should report clean")
	}
	if AnyUnresolved(nil) {
		t.Errorf("no claims should report clean")
	}
}

// depsAt returns a Deps that resolves any repo name to `repo` via a
// fake env var, so Scan checks files under the test's tempdir.
func depsAt(repo string) Deps {
	return Deps{
		LookupEnv: func(k string) (string, bool) {
			if strings.HasPrefix(k, "SPORE_CONSUMER_") {
				return repo, true
			}
			return "", false
		},
		HomeDir: func() (string, error) { return "/no/home", nil },
		Stat:    os.Stat,
		Grep: func(r, pattern string) (string, error) {
			return "", fmt.Errorf("grep not configured")
		},
	}
}
