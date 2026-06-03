package lints

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/versality/spore/internal/evidence"
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
	var issues []Issue
	err := forEachTask(root, l.TasksDir, func(rel string, raw []byte) error {
		m, body, err := frontmatter.Parse(raw)
		if err != nil {
			// Not a task file (e.g. README.md). Skip silently.
			return nil
		}
		rawReq, hasReq := m.Extra["evidence_required"]
		if !hasReq || strings.TrimSpace(rawReq) == "" {
			return nil
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
			return nil
		}
		verdict, diags := evidence.Verify(meta, string(body))
		if !evidence.Blocks(verdict) {
			return nil
		}
		if len(diags) == 0 {
			issues = append(issues, Issue{
				Path:    rel,
				Message: fmt.Sprintf("[%s]", verdict),
			})
			return nil
		}
		for _, d := range diags {
			issues = append(issues, Issue{
				Path:    rel,
				Message: fmt.Sprintf("[%s] %s", verdict, d),
			})
		}
		return nil
	})
	return issues, err
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
