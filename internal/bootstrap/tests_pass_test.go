package bootstrap

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectTestsPassNoMarkers(t *testing.T) {
	root := t.TempDir()
	_, err := detectTestsPass(root)
	if err == nil || !strings.Contains(err.Error(), "no recognised test recipe") {
		t.Fatalf("err=%v; want 'no recognised test recipe'", err)
	}
}

func TestDetectTestsPassJustCheck(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not on PATH; skipping")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "justfile"), []byte("check:\n\ttrue\n"))
	notes, err := detectTestsPass(root)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !strings.Contains(notes, "just check") {
		t.Errorf("notes=%q; want mention of `just check`", notes)
	}
}

func TestDetectTestsPassJustCheckFails(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not on PATH; skipping")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "justfile"), []byte("check:\n\tfalse\n"))
	_, err := detectTestsPass(root)
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("err=%v; want failure", err)
	}
}

func TestDetectTestsPassPrefersJustOverGo(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not on PATH; skipping")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "justfile"), []byte("check:\n\ttrue\n"))
	writeFile(t, filepath.Join(root, "go.mod"), []byte("module x\n"))
	notes, err := detectTestsPass(root)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !strings.Contains(notes, "just check") {
		t.Errorf("notes=%q; expected just check to win over go test", notes)
	}
}

func TestDetectTestsPassFallsThroughJustWithoutCheck(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH; skipping")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "justfile"), []byte("foo:\n\ttrue\n"))
	writeFile(t, filepath.Join(root, "go.mod"), []byte("module x\n\ngo 1.22\n"))
	writeFile(t, filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"))
	notes, err := detectTestsPass(root)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !strings.Contains(notes, "go test") {
		t.Errorf("notes=%q; want go test fallback", notes)
	}
}

func TestDetectTestsPassRubyRspecPreferred(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Gemfile"), []byte("source 'https://rubygems.org'\n"))
	writeFile(t, filepath.Join(root, ".rspec"), []byte("--require rails_helper\n"))
	writeFile(t, filepath.Join(root, "Rakefile"), []byte("# rails app\n"))
	_, err := detectTestsPass(root)
	if err == nil {
		t.Fatalf("err=nil; want recipe-needs-bundle error or success")
	}
	if !strings.Contains(err.Error(), "bundle") {
		t.Fatalf("err=%v; want mention of bundle (rspec recipe should be picked)", err)
	}
	if !strings.Contains(err.Error(), "rspec") {
		t.Fatalf("err=%v; want mention of rspec; rake should be passed over when .rspec is present", err)
	}
}

func TestDetectTestsPassRubyRakeFallback(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Gemfile"), []byte("source 'https://rubygems.org'\n"))
	writeFile(t, filepath.Join(root, "Rakefile"), []byte("task :test\n"))
	_, err := detectTestsPass(root)
	if err == nil {
		t.Fatalf("err=nil; want recipe-needs-bundle error")
	}
	if !strings.Contains(err.Error(), "bundle") {
		t.Fatalf("err=%v; want mention of bundle", err)
	}
	if !strings.Contains(err.Error(), "rake") {
		t.Fatalf("err=%v; want mention of rake test", err)
	}
}

func TestDetectTestsPassRubyGemfileAloneNotEnough(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Gemfile"), []byte("source 'https://rubygems.org'\n"))
	_, err := detectTestsPass(root)
	if err == nil || !strings.Contains(err.Error(), "no recognised test recipe") {
		t.Fatalf("err=%v; bare Gemfile should not match (no .rspec, no Rakefile)", err)
	}
}
