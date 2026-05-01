package frontmatter

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestWriteGolden(t *testing.T) {
	m := Meta{
		Status:  "draft",
		Slug:    "hello-world",
		Title:   "hello world",
		Created: "2026-04-28T10:00:00Z",
		Project: "spore",
	}
	body := []byte("\nbody line one\n")
	got := Write(m, body)
	want := "---\n" +
		"status: draft\n" +
		"slug: hello-world\n" +
		"title: hello world\n" +
		"created: 2026-04-28T10:00:00Z\n" +
		"project: spore\n" +
		"---\n" +
		"\nbody line one\n"
	if string(got) != want {
		t.Fatalf("Write golden mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		m    Meta
		body []byte
	}{
		{
			name: "core fields only",
			m: Meta{
				Status:  "active",
				Slug:    "x",
				Title:   "x",
				Created: "2026-01-01T00:00:00Z",
				Project: "spore",
			},
			body: []byte("\nbody\n"),
		},
		{
			name: "with host and agent",
			m: Meta{
				Status:  "draft",
				Slug:    "y",
				Title:   "y",
				Created: "2026-01-02T00:00:00Z",
				Project: "spore",
				Host:    "localhost",
				Agent:   "claude",
			},
			body: []byte("\nlonger\nbody\n"),
		},
		{
			name: "empty body",
			m: Meta{
				Status: "done",
				Slug:   "z",
				Title:  "z",
			},
			body: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := Write(c.m, c.body)
			parsed, body, err := Parse(out)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(parsed, c.m) {
				t.Errorf("Meta round-trip\n want: %#v\n got:  %#v", c.m, parsed)
			}
			if !bytes.Equal(body, c.body) {
				t.Errorf("body round-trip\n want: %q\n got:  %q", c.body, body)
			}
			again := Write(parsed, body)
			if !bytes.Equal(again, out) {
				t.Errorf("re-Write differs from first Write:\n%s\n---\n%s", out, again)
			}
		})
	}
}

func TestParseQuotedValue(t *testing.T) {
	in := []byte("---\nstatus: draft\ntitle: \"hello: world\"\n---\n")
	m, _, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Title != "hello: world" {
		t.Errorf("Title = %q, want %q", m.Title, "hello: world")
	}
}

func TestParseFirstClassFieldRoundTrip(t *testing.T) {
	in := []byte("---\nstatus: draft\nslug: x\nhost: tower\nagent: claude\n---\nbody\n")
	m, body, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Agent != "claude" || m.Host != "tower" {
		t.Errorf("Agent=%q Host=%q", m.Agent, m.Host)
	}
	out := Write(m, body)
	want := "---\nstatus: draft\nslug: x\nhost: tower\nagent: claude\n---\nbody\n"
	if string(out) != want {
		t.Errorf("Write mismatch\nwant:\n%s\ngot:\n%s", want, out)
	}
}

func TestParseUnknownFieldRoundTrip(t *testing.T) {
	in := []byte("---\nstatus: draft\nslug: x\ncustom_key: hello\n---\nbody\n")
	m, body, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Extra["custom_key"] != "hello" {
		t.Errorf("Extra = %#v", m.Extra)
	}
	out := Write(m, body)
	want := "---\nstatus: draft\nslug: x\ncustom_key: hello\n---\nbody\n"
	if string(out) != want {
		t.Errorf("Write mismatch\nwant:\n%s\ngot:\n%s", want, out)
	}
}

func TestNeedsBlockList(t *testing.T) {
	in := []byte("---\nstatus: draft\nslug: x\nneeds:\n  - foo\n  - bar\n---\nbody\n")
	m, body, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Needs) != 2 || m.Needs[0] != "foo" || m.Needs[1] != "bar" {
		t.Errorf("Needs = %v", m.Needs)
	}
	out := Write(m, body)
	if string(out) != string(in) {
		t.Errorf("round-trip mismatch\nwant:\n%s\ngot:\n%s", in, out)
	}
}

func TestNeedsEmpty(t *testing.T) {
	m := Meta{Status: "draft", Slug: "x"}
	out := Write(m, nil)
	if strings.Contains(string(out), "needs") {
		t.Errorf("empty Needs should not appear in output: %s", out)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantSub string
	}{
		{"empty", "", "missing opening"},
		{"no opening fence", "status: draft\n---\n", "missing opening"},
		{"no closing fence", "---\nstatus: draft\nslug: x\n", "missing closing"},
		{"malformed line", "---\nstatus draft\n---\n", "malformed line"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := Parse([]byte(c.in))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error %q does not contain %q", err, c.wantSub)
			}
		})
	}
}
