package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/versality/spore/internal/align"
)

// InfoGathered is the persisted shape of the operator's answers about
// the adopted project's existing project-management and knowledge
// surfaces. Sensitive values (tokens, OAuth refresh) live in the
// creds-broker; only the reference key is recorded here.
type InfoGathered struct {
	Tickets     InfoSurface `json:"tickets"`
	Knowledge   InfoSurface `json:"knowledge"`
	CompletedAt string      `json:"completed_at,omitempty"`
}

// InfoSurface captures one tool family: which tool the project uses,
// where its access lives in the creds-broker, and whether spore should
// substitute its own driver. ToolNone means the operator confirmed
// the family is absent.
type InfoSurface struct {
	Tool     string `json:"tool"`
	CredsRef string `json:"creds_ref,omitempty"`
	Decision string `json:"decision,omitempty"`
}

// validTicketTools lists the project-management surfaces spore knows
// how to read once creds are wired. ToolNone records "no ticket tool;
// use spore tasks".
var validTicketTools = map[string]bool{
	"jira":          true,
	"linear":        true,
	"github-issues": true,
	"none":          true,
}

// validKnowledgeTools lists the knowledge / wiki surfaces spore knows.
// ToolNone records "no wiki; use docs/todo + spore docs/list.md".
var validKnowledgeTools = map[string]bool{
	"notion":      true,
	"confluence":  true,
	"obsidian":    true,
	"google-docs": true,
	"docs-tree":   true,
	"none":        true,
}

func detectInfoGathered(root string) (string, error) {
	paths, err := align.Resolve(root)
	if err != nil {
		return "", err
	}
	jsonPath := paths.StateDir + "/info-gathered.json"
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no info-gathered.json under %s; invoke /spore-bootstrap so the agent walks the operator through it (or write the file directly)", paths.StateDir)
		}
		return "", err
	}
	var ig InfoGathered
	if err := json.Unmarshal(b, &ig); err != nil {
		return "", fmt.Errorf("parse %s: %w", jsonPath, err)
	}
	if !validTicketTools[ig.Tickets.Tool] {
		return "", fmt.Errorf("tickets.tool=%q; want one of jira/linear/github-issues/none", ig.Tickets.Tool)
	}
	if !validKnowledgeTools[ig.Knowledge.Tool] {
		return "", fmt.Errorf("knowledge.tool=%q; want one of notion/confluence/obsidian/google-docs/docs-tree/none", ig.Knowledge.Tool)
	}
	if ig.Tickets.Tool != "none" && ig.Tickets.CredsRef == "" {
		return "", errors.New("tickets.tool is set but tickets.creds_ref is empty; record the broker reference, not the secret")
	}
	if ig.Knowledge.Tool != "none" && ig.Knowledge.CredsRef == "" {
		return "", errors.New("knowledge.tool is set but knowledge.creds_ref is empty; record the broker reference, not the secret")
	}
	return fmt.Sprintf("tickets=%s knowledge=%s", ig.Tickets.Tool, ig.Knowledge.Tool), nil
}
