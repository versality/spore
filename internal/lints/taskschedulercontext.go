package lints

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/versality/spore/internal/task/frontmatter"
)

// TaskSchedulerContext requires parked tasks carrying scheduler-gating
// state to spell out their trigger or resume condition in the
// `scheduler:` frontmatter line. The bash predecessor lives at
// nix-config harness/check-task-scheduler-context.sh; this is the Go
// port. Bug 56a0f44c fixed a comment-stripping regression: a midline
// `#` (e.g. "codex A/B test #2; promote now") used to be truncated
// as a YAML comment, while a leading `#` should be treated as an
// empty value. Both behaviors are pinned by tests.
type TaskSchedulerContext struct {
	TasksDir string
	OwnSlug  string
	Now      time.Time
}

func (TaskSchedulerContext) Name() string { return "task-scheduler-context" }

var (
	schedulerTriggerWord = regexp.MustCompile(`(?i)(^|[[:space:]])(until|when|after|needs|depends|trigger|promote|resume)([[:space:]]|$)`)
	schedulerDate        = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
	schedulerKeyLine     = regexp.MustCompile(`(?m)^scheduler[[:space:]]*:`)
	laterOnlyMarker      = regexp.MustCompile(`(?i)(later-only|later only)`)
	triggerPhraseHeading = regexp.MustCompile(`(?im)^##[ \t]+trigger phrase[ \t]*$`)
)

func (l TaskSchedulerContext) Run(root string) ([]Issue, error) {
	dir := l.TasksDir
	if dir == "" {
		dir = "tasks"
	}
	abs := filepath.Join(root, dir)
	entries, err := os.ReadDir(abs)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	ownSlug := l.OwnSlug
	if ownSlug == "" {
		ownSlug = deriveOwnSlug(root)
	}
	now := l.Now
	if now.IsZero() {
		now = time.Now()
	}
	today := now.Format("2006-01-02")

	var issues []Issue
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(abs, e.Name())
		rel := filepath.ToSlash(filepath.Join(dir, e.Name()))
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		m, body, err := frontmatter.Parse(raw)
		if err != nil {
			continue
		}
		if m.Status != "parked" && m.Status != "blocked" {
			continue
		}
		if ownSlug != "" {
			slug := m.Slug
			if slug == "" {
				slug = strings.TrimSuffix(e.Name(), ".md")
			}
			if slug != ownSlug {
				continue
			}
		}

		schedulerVal, hasSched := m.Extra["scheduler"]
		schedulerVal = strings.TrimSpace(schedulerVal)
		if strings.HasPrefix(schedulerVal, "#") {
			// Leading '#' is a comment-only value; treat as empty.
			// Mirrors nix-config bug 56a0f44c post-fix semantics.
			schedulerVal = ""
		}
		schedulerKey := strings.TrimSpace(m.Extra["scheduler_key"])
		hasLaterOnly := bodyHasLaterOnly(body)

		if m.Status == "blocked" {
			if hasLaterOnly && !hasSched {
				issues = append(issues, Issue{
					Path:    rel,
					Message: "later-only blocked task requires scheduler: <operator trigger/dependency>",
				})
			}
			continue
		}

		// parked: skip when auto-promotable (no scheduler_key, no later-only).
		if schedulerKey == "" && !hasLaterOnly {
			continue
		}

		line := schedulerLineNumber(raw)
		if !hasSched || schedulerVal == "" {
			issues = append(issues, Issue{
				Path:    rel,
				Line:    line,
				Message: "status=parked requires frontmatter scheduler: <coordinator-owned trigger/resume condition>",
			})
			continue
		}
		if !schedulerTriggerWord.MatchString(schedulerVal) {
			issues = append(issues, Issue{
				Path:    rel,
				Line:    line,
				Message: "scheduler for status=parked must name a trigger, dependency, or resume condition",
			})
		}
		if mm := schedulerDate.FindStringSubmatch(schedulerVal); mm != nil && mm[1] < today {
			issues = append(issues, Issue{
				Path:    rel,
				Line:    line,
				Message: fmt.Sprintf("scheduler date %s is stale; update the resume/parking condition", mm[1]),
			})
		}
	}
	return issues, nil
}

func bodyHasLaterOnly(body []byte) bool {
	if laterOnlyMarker.Match(body) {
		return true
	}
	return triggerPhraseHeading.Match(body)
}

func schedulerLineNumber(raw []byte) int {
	lines := bytes.Split(raw, []byte("\n"))
	fences := 0
	for i, ln := range lines {
		s := strings.TrimRight(string(ln), " \t\r")
		if s == "---" {
			fences++
			if fences >= 2 {
				return 0
			}
			continue
		}
		if fences == 1 && schedulerKeyLine.MatchString(s) {
			return i + 1
		}
	}
	return 0
}

func deriveOwnSlug(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	cmd := exec.Command("git", "-c", "safe.directory="+abs, "-C", root, "symbolic-ref", "--short", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	br := strings.TrimSpace(out.String())
	if !strings.HasPrefix(br, "wt/") {
		return ""
	}
	return strings.TrimPrefix(br, "wt/")
}
