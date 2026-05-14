package cutover

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/task/consumerclaim"
)

type fakeFS struct {
	writes map[string][]byte
	dirs   map[string]bool
	exists map[string]bool
}

func newFakeFS() *fakeFS {
	return &fakeFS{
		writes: map[string][]byte{},
		dirs:   map[string]bool{},
		exists: map[string]bool{},
	}
}

func (f *fakeFS) Write(path string, data []byte, _ fs.FileMode) error {
	f.writes[path] = append([]byte(nil), data...)
	f.exists[path] = true
	return nil
}

func (f *fakeFS) Stat(path string) (os.FileInfo, error) {
	if f.dirs[path] {
		return fakeDirInfo{name: filepath.Base(path)}, nil
	}
	if f.exists[path] {
		return fakeFileInfo{name: filepath.Base(path)}, nil
	}
	return nil, os.ErrNotExist
}

func (f *fakeFS) MkdirAll(path string, _ fs.FileMode) error {
	f.dirs[path] = true
	return nil
}

type fakeFileInfo struct{ name string }

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return 0o644 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() interface{}   { return nil }

type fakeDirInfo struct{ name string }

func (f fakeDirInfo) Name() string       { return f.name }
func (f fakeDirInfo) Size() int64        { return 0 }
func (f fakeDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o755 }
func (f fakeDirInfo) ModTime() time.Time { return time.Time{} }
func (f fakeDirInfo) IsDir() bool        { return true }
func (f fakeDirInfo) Sys() interface{}   { return nil }

func newDeps(fs *fakeFS, consumerRoot string) Deps {
	return Deps{
		Consumer: consumerclaim.Deps{
			LookupEnv: func(k string) (string, bool) {
				if k == "SPORE_CONSUMER_NIX_CONFIG" {
					return consumerRoot, true
				}
				return "", false
			},
			Stat: fs.Stat,
		},
		WriteFile: fs.Write,
		Stat:      fs.Stat,
		MkdirAll:  fs.MkdirAll,
		Now:       func() time.Time { return time.Date(2026, 5, 13, 10, 30, 0, 0, time.UTC) },
	}
}

func TestMintHappyPath(t *testing.T) {
	fs := newFakeFS()
	fs.dirs["/srv/nix-config"] = true

	r, err := Mint(Options{
		Consumer:   "nix-config",
		Feature:    "worker-finish-contract",
		SourceRepo: "spore",
		SourceSlug: "spore-worker-finish-contract",
		SourcePR:   42,
		Claim:      "nix-config:path:modules/foo.sh",
		Reason:     "covered by spore lint totalsize",
	}, newDeps(fs, "/srv/nix-config"))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if r.Skipped {
		t.Error("Skipped = true on first mint")
	}
	wantSlug := "consume-spore-worker-finish-contract"
	if r.Slug != wantSlug {
		t.Errorf("Slug = %q, want %q", r.Slug, wantSlug)
	}
	wantPath := "/srv/nix-config/tasks/" + wantSlug + ".md"
	if r.Path != wantPath {
		t.Errorf("Path = %q, want %q", r.Path, wantPath)
	}

	body := string(fs.writes[wantPath])
	for _, want := range []string{
		"status: draft",
		"slug: " + wantSlug,
		"project: nix-config",
		"priority: medium",
		"created: 2026-05-13T10:30:00Z",
		"source-repo: spore",
		"source-slug: spore-worker-finish-contract",
		"source-pr: 42",
		"claim: nix-config:path:modules/foo.sh",
		"reason: covered by spore lint totalsize",
		"PR: #42",
		"nix-config:path:modules/foo.sh",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n%s", want, body)
		}
	}
}

func TestMintIdempotent(t *testing.T) {
	fs := newFakeFS()
	fs.dirs["/srv/nix-config"] = true

	opts := Options{
		Consumer:   "nix-config",
		Feature:    "feat",
		SourceRepo: "spore",
		Claim:      "nix-config:grep:foo",
	}
	r1, err := Mint(opts, newDeps(fs, "/srv/nix-config"))
	if err != nil {
		t.Fatal(err)
	}
	if r1.Skipped {
		t.Error("first mint Skipped = true")
	}
	body1 := string(fs.writes[r1.Path])

	r2, err := Mint(opts, newDeps(fs, "/srv/nix-config"))
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Skipped {
		t.Error("second mint Skipped = false; want true (file exists)")
	}
	if r2.Slug != r1.Slug || r2.Path != r1.Path {
		t.Errorf("second mint diverged: r1=%+v r2=%+v", r1, r2)
	}
	if string(fs.writes[r1.Path]) != body1 {
		t.Error("idempotent mint rewrote the file")
	}
}

func TestMintMissingConsumer(t *testing.T) {
	fs := newFakeFS() // no dirs
	_, err := Mint(Options{Consumer: "nix-config", Feature: "feat"}, newDeps(fs, "/srv/nix-config"))
	if err == nil || !strings.Contains(err.Error(), "consumer checkout absent") {
		t.Fatalf("err = %v, want consumer-absent", err)
	}
}

func TestMintNoSourceRepo(t *testing.T) {
	fs := newFakeFS()
	fs.dirs["/srv/nix-config"] = true
	r, err := Mint(Options{Consumer: "nix-config", Feature: "feat"}, newDeps(fs, "/srv/nix-config"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Slug != "consume-feat" {
		t.Errorf("Slug = %q, want consume-feat", r.Slug)
	}
}

func TestMintGuards(t *testing.T) {
	fs := newFakeFS()
	if _, err := Mint(Options{Feature: "x"}, newDeps(fs, "/x")); err == nil || !strings.Contains(err.Error(), "consumer repo required") {
		t.Errorf("missing-consumer err = %v", err)
	}
	if _, err := Mint(Options{Consumer: "x"}, newDeps(fs, "/x")); err == nil || !strings.Contains(err.Error(), "feature required") {
		t.Errorf("missing-feature err = %v", err)
	}
}
