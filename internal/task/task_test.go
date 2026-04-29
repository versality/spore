package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello world", "hello-world"},
		{"Hello, World!", "hello-world"},
		{"  spaces  everywhere  ", "spaces-everywhere"},
		{"---leading-and-trailing---", "leading-and-trailing"},
		{"café résumé", "caf-r-sum"},
		{"snake_case_input", "snake-case-input"},
		{"alpha123beta", "alpha123beta"},
		{"", ""},
		{"!!!", ""},
		{strings.Repeat("a", 60), strings.Repeat("a", 50)},
		{strings.Repeat("a", 49) + "-bbbb", strings.Repeat("a", 49)},
	}
	for _, c := range cases {
		got := Slugify(c.in)
		if got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAllocate(t *testing.T) {
	dir := t.TempDir()
	got, err := Allocate(dir, "demo")
	if err != nil {
		t.Fatalf("first Allocate: %v", err)
	}
	if got != "demo" {
		t.Errorf("first slug = %q, want demo", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Allocate(dir, "demo")
	if err != nil {
		t.Fatalf("second Allocate: %v", err)
	}
	if got != "demo-2" {
		t.Errorf("second slug = %q, want demo-2", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "demo-2.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Allocate(dir, "demo")
	if err != nil {
		t.Fatalf("third Allocate: %v", err)
	}
	if got != "demo-3" {
		t.Errorf("third slug = %q, want demo-3", got)
	}
}

func TestAllocateExhausted(t *testing.T) {
	dir := t.TempDir()
	for n := 1; n <= 99; n++ {
		name := "x"
		if n > 1 {
			name = "x-" + itoa(n)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Allocate(dir, "x")
	if err == nil {
		t.Fatal("expected exhaustion error, got nil")
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("zeta.md", "---\nstatus: active\nslug: zeta\ntitle: Zeta\n---\n")
	write("alpha.md", "---\nstatus: draft\nslug: alpha\ntitle: Alpha\n---\n")
	write("mid.md", "---\nstatus: done\nslug: mid\ntitle: Mid\n---\n")
	write("README.md", "no frontmatter here\n")
	write("notes.txt", "ignored: not markdown\n")

	metas, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 3 {
		t.Fatalf("expected 3 metas, got %d (%v)", len(metas), metas)
	}
	wantOrder := []string{"alpha", "mid", "zeta"}
	for i, want := range wantOrder {
		if metas[i].Slug != want {
			t.Errorf("metas[%d].Slug = %q, want %q", i, metas[i].Slug, want)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
