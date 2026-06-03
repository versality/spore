package task

import (
	"path/filepath"
	"testing"

	"github.com/versality/spore/internal/task/frontmatter"
)

func TestProjectEmoji(t *testing.T) {
	if got := projectEmoji("spore"); got != "\U0001F41D" {
		t.Errorf("spore emoji = %q, want bee", got)
	}
	if got := projectEmoji("marketer"); got != "\U0001F41D" {
		t.Errorf("marketer emoji = %q, want bee", got)
	}
	if got := projectEmoji("nix-config"); got != "\U0001F994" {
		t.Errorf("nix-config emoji = %q, want hedgehog", got)
	}
	first := projectEmoji("alpha")
	second := projectEmoji("alpha")
	if first != second {
		t.Errorf("projectEmoji must be deterministic: %q vs %q", first, second)
	}
	if projectEmoji("alpha") == projectEmoji("beta") {
		t.Errorf("projectEmoji fallback collapsed to a single icon for distinct names")
	}
}

func TestWtSessionNameTagOptional(t *testing.T) {
	if got := wtSessionName("spore", "demo", ""); got != "\U0001F41D spore/demo" {
		t.Errorf("without tag = %q", got)
	}
	if got := wtSessionName("spore", "demo", "opus_high"); got != "\U0001F41D spore/demo [opus_high]" {
		t.Errorf("with tag = %q", got)
	}
}

func TestTmuxSessionNameUsesTierTag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spore")
	m := frontmatter.Meta{Agent: "codex", Extra: map[string]string{"effort": "high"}}
	got, err := tmuxSessionName(dir, "demo", m)
	if err != nil {
		t.Fatalf("tmuxSessionName: %v", err)
	}
	want := "\U0001F41D spore/demo [codex-high]"
	if got != want {
		t.Errorf("session = %q, want %q", got, want)
	}
}

func TestParseSessionAcceptsWtShapes(t *testing.T) {
	type want struct {
		slug string
		kind string
		tag  string
	}
	cases := []struct {
		name    string
		project string
		want    want
		ok      bool
	}{
		// Worker, wt-emoji shape.
		{"\U0001F41D spore/demo [opus_high]", "spore", want{"demo", SessionKindWorker, "opus_high"}, true},
		{"\U0001F41D spore/demo", "spore", want{"demo", SessionKindWorker, ""}, true},
		{"\U0001F428 demo/foo-bar [codex-high]", "demo", want{"foo-bar", SessionKindWorker, "codex-high"}, true},
		// Coordinator, bare and emoji-prefixed.
		{"demo/coordinator", "demo", want{"", SessionKindCoordinator, ""}, true},
		{"\U0001F41D spore/coordinator", "spore", want{"", SessionKindCoordinator, ""}, true},
		// Misses.
		{"unrelated", "spore", want{}, false},
		{"spore/demo/extra/slug", "demo", want{}, false},
		// Slug-with-suffix must not match a different slug.
		{"\U0001F41D spore/demo-extra", "spore", want{"demo-extra", SessionKindWorker, ""}, true},
	}
	for _, c := range cases {
		p, ok := ParseSession(c.name, c.project)
		if ok != c.ok {
			t.Errorf("ParseSession(%q,%q) ok = %v; want %v", c.name, c.project, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if p.Slug != c.want.slug || p.Kind != c.want.kind || p.Tag != c.want.tag {
			t.Errorf("ParseSession(%q,%q) = %+v; want slug=%q kind=%s tag=%q",
				c.name, c.project, p, c.want.slug, c.want.kind, c.want.tag)
		}
	}
}

func TestParseSessionClassifiesEmojiCoordinator(t *testing.T) {
	p, ok := ParseSession(CoordinatorSession("spore"), "spore")
	if !ok {
		t.Fatalf("ParseSession(%q) not recognized", CoordinatorSession("spore"))
	}
	if p.Kind != SessionKindCoordinator || p.Slug != "" {
		t.Errorf("ParseSession(%q) = %+v; want Kind=Coordinator slug empty", CoordinatorSession("spore"), p)
	}
}

func TestMatchSlugRejectsSuffixCollision(t *testing.T) {
	if MatchSlug("\U0001F41D spore/foo-bar", "spore", "foo") {
		t.Errorf("MatchSlug must not treat slug %q as a prefix of %q", "foo", "foo-bar")
	}
	if !MatchSlug("\U0001F41D spore/foo-bar", "spore", "foo-bar") {
		t.Errorf("MatchSlug missed exact-slug match")
	}
	if MatchSlug("\U0001F428 demo/coordinator", "demo", "coordinator") {
		t.Errorf("MatchSlug must not match coordinator sessions as worker slug=coordinator")
	}
}
