package lints

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/evidence"
	"github.com/versality/spore/internal/task/frontmatter"
)

// TaskEvidence enforces the evidence contract on tasks/<slug>.md files.
//
// Cases:
//
//   - tasks without `evidence_required:` are pre-contract; skipped.
//   - tasks with declared kinds but at least one outside evidence.Kinds
//     are reported regardless of status (lint-time configuration error).
//   - status=done tasks with a contract whose verdict blocks
//     (suspect-hallucination, bogus-evidence, unknown) are reported.
//
// Run never returns an error from a normal find. The lint surfaces
// findings via Issue. The CLI decides whether to exit non-zero based
// on the soak window plus SPORE_EVIDENCE_WARN_ONLY.
type TaskEvidence struct {
	TasksDir string
}

func (TaskEvidence) Name() string { return "task-evidence" }

func (l TaskEvidence) Run(root string) ([]Issue, error) {
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
			// Not a task file (e.g. README.md). Skip silently.
			continue
		}
		rawReq, hasReq := m.Extra["evidence_required"]
		if !hasReq || strings.TrimSpace(rawReq) == "" {
			continue
		}
		meta := map[string]any{"evidence_required": rawReq}
		required := evidence.Required(meta)

		for _, k := range required {
			if !evidence.IsKind(k) {
				issues = append(issues, Issue{
					Path:    rel,
					Message: fmt.Sprintf("evidence_required: unknown kind %q (allowed: %s)", k, strings.Join(evidence.Kinds, ", ")),
				})
			}
		}
		if m.Status != "done" {
			continue
		}
		verdict, diags := evidence.Verify(meta, string(body))
		if !evidence.Blocks(verdict) {
			continue
		}
		if len(diags) == 0 {
			issues = append(issues, Issue{
				Path:    rel,
				Message: fmt.Sprintf("[%s]", verdict),
			})
			continue
		}
		for _, d := range diags {
			issues = append(issues, Issue{
				Path:    rel,
				Message: fmt.Sprintf("[%s] %s", verdict, d),
			})
		}
	}
	return issues, nil
}

// EvidenceWarnOnly reports whether the task-evidence lint should be
// reduced to warn-only (issues printed, exit code suppressed). True
// during the soak window or when SPORE_EVIDENCE_WARN_ONLY=1 is set.
func EvidenceWarnOnly() bool {
	if os.Getenv("SPORE_EVIDENCE_WARN_ONLY") == "1" {
		return true
	}
	return evidence.InSoakWindow(time.Now())
}
