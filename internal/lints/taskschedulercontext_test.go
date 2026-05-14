package lints

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func taskMD(status, slug, scheduler, schedulerKey, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("slug: " + slug + "\n")
	b.WriteString("title: t\n")
	b.WriteString("status: " + status + "\n")
	if scheduler != "" {
		b.WriteString("scheduler: " + scheduler + "\n")
	}
	if schedulerKey != "" {
		b.WriteString("scheduler_key: " + schedulerKey + "\n")
	}
	b.WriteString("---\n")
	b.WriteString(body)
	return b.String()
}

func TestTaskSchedulerContext(t *testing.T) {
	fixedNow := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		files map[string]string
		lint  TaskSchedulerContext
		want  []string // "path:msg-substring"
	}{
		{
			name: "parked with scheduler_key and valid trigger word -> ok",
			files: map[string]string{
				"tasks/a.md": taskMD("parked", "a", "parked until codex frees", "codex", ""),
			},
		},
		{
			name: "parked with scheduler_key but no scheduler -> reported",
			files: map[string]string{
				"tasks/a.md": taskMD("parked", "a", "", "codex", ""),
			},
			want: []string{"requires frontmatter scheduler"},
		},
		{
			name: "parked with scheduler_key + value lacking trigger word -> reported",
			files: map[string]string{
				"tasks/a.md": taskMD("parked", "a", "see notes", "codex", ""),
			},
			want: []string{"must name a trigger"},
		},
		{
			name: "parked with later-only body + no scheduler -> reported",
			files: map[string]string{
				"tasks/a.md": taskMD("parked", "a", "", "", "this is a later-only task\n"),
			},
			want: []string{"requires frontmatter scheduler"},
		},
		{
			name: "blocked + later-only + no scheduler -> reported",
			files: map[string]string{
				"tasks/a.md": taskMD("blocked", "a", "", "", "later-only stuff\n"),
			},
			want: []string{"tasks/a.md:later-only blocked task"},
		},
		{
			name: "parked + stale date -> reported",
			files: map[string]string{
				"tasks/a.md": taskMD("parked", "a", "resume after 2026-01-01", "codex", ""),
			},
			want: []string{"tasks/a.md:scheduler date 2026-01-01 is stale"},
		},
		{
			name: "parked without scheduler_key and no later-only -> auto-promotable skip",
			files: map[string]string{
				"tasks/a.md": taskMD("parked", "a", "", "", ""),
			},
		},
		{
			name: "bug 56a0f44c: midline '#' preserved",
			files: map[string]string{
				"tasks/a.md": taskMD("parked", "a", "codex A/B test run #2 (high effort); promote now", "codex", ""),
			},
		},
		{
			name: "bug 56a0f44c: leading '#' treated as empty -> reported",
			files: map[string]string{
				"tasks/a.md": taskMD("parked", "a", "# resume when selected", "codex", ""),
			},
			want: []string{"requires frontmatter scheduler"},
		},
		{
			name: "OwnSlug scoping: unrelated parked task ignored",
			lint: TaskSchedulerContext{OwnSlug: "mine"},
			files: map[string]string{
				"tasks/other.md": taskMD("parked", "other", "", "codex", ""),
				"tasks/mine.md":  taskMD("parked", "mine", "until codex frees", "codex", ""),
			},
		},
		{
			name: "trigger phrase heading counts as later-only",
			files: map[string]string{
				"tasks/a.md": taskMD("blocked", "a", "", "", "## Trigger phrase\n\nsome body\n"),
			},
			want: []string{"tasks/a.md:later-only blocked task"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestRepo(t, tt.files)
			lint := tt.lint
			lint.Now = fixedNow
			issues, err := lint.Run(root)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := make([]string, 0, len(issues))
			for _, i := range issues {
				got = append(got, i.Path+":"+i.Message)
			}
			sort.Strings(got)
			if len(got) != len(tt.want) {
				t.Fatalf("issues: got %v want %v", got, tt.want)
			}
			for i, want := range tt.want {
				if !strings.Contains(got[i], want) {
					t.Fatalf("issue[%d]=%q does not contain %q", i, got[i], want)
				}
			}
		})
	}
}

func TestTaskSchedulerContext_LineNumber(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"tasks/a.md": taskMD("parked", "a", "see notes", "codex", ""),
	})
	lint := TaskSchedulerContext{Now: time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)}
	issues, err := lint.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Line == 0 {
		t.Fatalf("expected non-zero line, got %d", issues[0].Line)
	}
}
