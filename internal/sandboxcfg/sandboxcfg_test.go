package sandboxcfg

import (
	"reflect"
	"testing"
)

func TestParseSandboxSection(t *testing.T) {
	in := `
# top comment
[matter.unrelated]
key = "ignored"

[sandbox]
allow_hosts = ["api.anthropic.com", "linear.app"]
rw = ["/home/sky/.config/nvim"]
ro = []
`
	got, err := LoadFromString(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := Config{
		AllowHosts: []string{"api.anthropic.com", "linear.app"},
		RW:         []string{"/home/sky/.config/nvim"},
		RO:         nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUnknownKeyIsError(t *testing.T) {
	in := `[sandbox]
allow_lan = ["10.0.0.0/8"]
`
	if _, err := LoadFromString(in); err == nil {
		t.Fatal("expected unknown-key error")
	}
}

func TestMissingSandboxSectionIsEmpty(t *testing.T) {
	in := `[matter.linear]
enabled = true
`
	got, err := LoadFromString(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got.AllowHosts)+len(got.RW)+len(got.RO) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestMergeDedupesAndPreservesOrder(t *testing.T) {
	a := Config{AllowHosts: []string{"a", "b"}, RW: []string{"/x"}}
	b := Config{AllowHosts: []string{"b", "c"}, RW: []string{"/x", "/y"}}
	got := Merge(a, b)
	if !reflect.DeepEqual(got.AllowHosts, []string{"a", "b", "c"}) {
		t.Fatalf("allow_hosts: %v", got.AllowHosts)
	}
	if !reflect.DeepEqual(got.RW, []string{"/x", "/y"}) {
		t.Fatalf("rw: %v", got.RW)
	}
}

func TestQuotedHashIsNotComment(t *testing.T) {
	in := `[sandbox]
allow_hosts = ["foo.example#bar"]
`
	got, err := LoadFromString(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.AllowHosts[0] != "foo.example#bar" {
		t.Fatalf("unexpected: %q", got.AllowHosts[0])
	}
}
