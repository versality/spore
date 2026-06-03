package sporetoml

import (
	"fmt"
	"reflect"
	"testing"
)

func TestStripComment(t *testing.T) {
	cases := map[string]string{
		`key = "value # not a comment" # comment`: `key = "value # not a comment" `,
		`key = bare # tail`:                       `key = bare `,
		`# whole line`:                            ``,
		`'single # quoted' # tail`:                `'single # quoted' `,
		`no comment here`:                         `no comment here`,
		`"unterminated # stays`:                   `"unterminated # stays`,
	}
	for in, want := range cases {
		if got := StripComment(in); got != want {
			t.Errorf("StripComment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripQuotes(t *testing.T) {
	cases := map[string]string{
		`"x"`:   `x`,
		`'x'`:   `x`,
		`x`:     `x`,
		`""`:    ``,
		`"`:     `"`,
		`"x'`:   `"x'`,
		`a"b"c`: `a"b"c`,
	}
	for in, want := range cases {
		if got := StripQuotes(in); got != want {
			t.Errorf("StripQuotes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`"a", "b", "c"`, []string{"a", "b", "c"}},
		{`a, b ,c`, []string{"a", "b", "c"}},
		{`"x, y", z`, []string{"x, y", "z"}},
		{`'p', "q"`, []string{"p", "q"}},
		{``, nil},
		{`  ,  `, nil},
	}
	for _, c := range cases {
		if got := SplitList(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitList(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestScanSectionsMultiSection(t *testing.T) {
	content := `# leading comment
[fleet]
max_workers = 3  # inline comment

[fleet.workers.ratio]
claude = 67
codex = 33

[coordinator]
driver = "claude"
brief = 'path # with hash'
`
	type kv struct {
		section, key, val string
	}
	var got []kv
	err := ScanSections(content, func(l Line) error {
		key, raw, ok := SplitKeyValue(l.Text)
		if !ok {
			return fmt.Errorf("line %d: not a kv: %q", l.LineNum, l.Text)
		}
		got = append(got, kv{l.Section, key, StripQuotes(raw)})
		return nil
	})
	if err != nil {
		t.Fatalf("ScanSections: %v", err)
	}
	want := []kv{
		{"fleet", "max_workers", "3"},
		{"fleet.workers.ratio", "claude", "67"},
		{"fleet.workers.ratio", "codex", "33"},
		{"coordinator", "driver", "claude"},
		{"coordinator", "brief", "path # with hash"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanSections entries = %#v, want %#v", got, want)
	}
}

func TestScanSectionsPropagatesError(t *testing.T) {
	sentinel := fmt.Errorf("boom")
	err := ScanSections("[s]\nk = v\n", func(Line) error { return sentinel })
	if err != sentinel {
		t.Fatalf("want sentinel error, got %v", err)
	}
}

func TestSplitKeyValue(t *testing.T) {
	cases := []struct {
		line string
		k, v string
		ok   bool
	}{
		{`a = b`, "a", "b", true},
		{`a=b`, "a", "b", true},
		{`a = "b = c"`, "a", `"b = c"`, true},
		{`noequals`, "", "", false},
		{`= rhs`, "", "", false},
	}
	for _, c := range cases {
		k, v, ok := SplitKeyValue(c.line)
		if k != c.k || v != c.v || ok != c.ok {
			t.Errorf("SplitKeyValue(%q) = (%q,%q,%v), want (%q,%q,%v)", c.line, k, v, ok, c.k, c.v, c.ok)
		}
	}
}
