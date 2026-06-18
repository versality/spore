// Linear GraphQL client: the wire types and the Source methods that
// talk to the Linear API. Split out of linear.go to keep each file
// under the 800-line limit; the Sync/adopt/tasks-dir orchestration
// stays in linear.go, the HTTP/GraphQL surface lives here.
package linear

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// transitionIssue maps the issueUpdate mutation. Linear treats the
// id field on issueUpdate as the issue ID, which can be either the
// canonical UUID or the human identifier (e.g. "MAR-42"); we pass
// whichever the caller hands us.
func (s *Source) transitionIssue(issueID, stateID string) error {
	const mutation = `mutation IssueUpdate($id: String!, $stateId: String!) {
  issueUpdate(id: $id, input: {stateId: $stateId}) { success }
}`
	var resp struct {
		Data struct {
			IssueUpdate struct {
				Success bool `json:"success"`
			} `json:"issueUpdate"`
		} `json:"data"`
	}
	vars := map[string]any{"id": issueID, "stateId": stateID}
	if err := s.graphQL(mutation, vars, &resp); err != nil {
		return err
	}
	if !resp.Data.IssueUpdate.Success {
		return fmt.Errorf("issueUpdate returned success=false for %s", issueID)
	}
	return nil
}

func (s *Source) loadStateIDs() error {
	if s.stateIDs != nil {
		return nil
	}
	const q = `query States($team: String!) {
  workflowStates(filter: {team: {key: {eq: $team}}}) {
    nodes { id name }
  }
}`
	var resp struct {
		Data struct {
			WorkflowStates struct {
				Nodes []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"nodes"`
			} `json:"workflowStates"`
		} `json:"data"`
	}
	if err := s.graphQL(q, map[string]any{"team": s.cfg.Team}, &resp); err != nil {
		return err
	}
	out := map[string]string{}
	for _, n := range resp.Data.WorkflowStates.Nodes {
		out[n.Name] = n.ID
	}
	if len(out) == 0 {
		return fmt.Errorf("matter.linear: workflowStates returned no nodes for team %s", s.cfg.Team)
	}
	s.stateIDs = out
	return nil
}

type linearIssue struct {
	ID          string          `json:"id"`
	Identifier  string          `json:"identifier"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	URL         string          `json:"url"`
	SortOrder   float64         `json:"sortOrder"`
	Relations   linearRelations `json:"relations"`
}

type linearRelations struct {
	Nodes []linearRelation `json:"nodes"`
}

type linearRelation struct {
	Type         string             `json:"type"`
	RelatedIssue linearRelatedIssue `json:"relatedIssue"`
}

type linearRelatedIssue struct {
	ID    string                  `json:"id"`
	State linearRelatedIssueState `json:"state"`
}

type linearRelatedIssueState struct {
	Type string `json:"type"`
}

// isBlockedByOpenUpstream returns true when issue has at least one
// blocked_by relation pointing to an upstream issue whose state.type
// is not terminal. Linear's state.type enum tags terminal states
// "completed" and "canceled"; the workflow-state name is
// operator-customisable and not safe to key on.
func isBlockedByOpenUpstream(issue linearIssue) bool {
	for _, rel := range issue.Relations.Nodes {
		if rel.Type != "blocked_by" {
			continue
		}
		switch rel.RelatedIssue.State.Type {
		case "completed", "canceled":
			continue
		default:
			return true
		}
	}
	return false
}

// listIssuesByState returns the issues in stateID ordered by Linear's
// kanban sortOrder (ascending; lower values appear higher in the
// column). The GraphQL `issues` query has no manual-order knob - its
// `orderBy` only takes createdAt/updatedAt - so we fetch sortOrder and
// sort client-side. Operators expect "drag a ticket to the top of
// Ready, get it picked next"; relying on default ordering would honour
// createdAt instead.
func (s *Source) listIssuesByState(stateID string) ([]linearIssue, error) {
	q := `query StateIssues($stateId: ID!) {
  issues(filter: {state: {id: {eq: $stateId}}}) {
    nodes {
      id identifier title description url sortOrder
      relations {
        nodes {
          type
          relatedIssue { id state { type } }
        }
      }
    }
  }
}`
	vars := map[string]any{"stateId": stateID}
	if s.cfg.ClaimLabel != "" {
		q = `query StateIssues($stateId: ID!, $label: String!) {
  issues(filter: {state: {id: {eq: $stateId}}, labels: {some: {name: {eq: $label}}}}) {
    nodes {
      id identifier title description url sortOrder
      relations {
        nodes {
          type
          relatedIssue { id state { type } }
        }
      }
    }
  }
}`
		vars["label"] = s.cfg.ClaimLabel
	}
	var resp struct {
		Data struct {
			Issues struct {
				Nodes []linearIssue `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
	}
	if err := s.graphQL(q, vars, &resp); err != nil {
		return nil, err
	}
	nodes := resp.Data.Issues.Nodes
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].SortOrder != nodes[j].SortOrder {
			return nodes[i].SortOrder < nodes[j].SortOrder
		}
		return nodes[i].Identifier < nodes[j].Identifier
	})
	return nodes, nil
}

// graphQL sends one POST to the configured endpoint and decodes the
// response into out. A 200 with a non-empty `errors` array surfaces
// as an error so a partial GraphQL failure does not look successful.
func (s *Source) graphQL(query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", s.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", s.apiKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("linear: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var probe struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && len(probe.Errors) > 0 {
		msgs := make([]string, 0, len(probe.Errors))
		for _, e := range probe.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("linear graphql: %s", strings.Join(msgs, "; "))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
