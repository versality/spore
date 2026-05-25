package codextrust

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestInspectTrustedRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	body := "[projects." + strconv.Quote(root) + "]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Trusted {
		t.Fatalf("Trusted = false, want true: %+v", st)
	}
}

func TestInspectUntrustedRoot(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	body := "[projects." + strconv.Quote(root) + "]\ntrust_level = \"untrusted\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Trusted {
		t.Fatalf("Trusted = true, want false")
	}
}

func TestInspectMissingConfigIsUntrusted(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	st, err := Inspect(root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Trusted {
		t.Fatal("missing config should not be trusted")
	}
	if st.ConfigPath != filepath.Join(home, "config.toml") {
		t.Fatalf("ConfigPath = %q", st.ConfigPath)
	}
}
