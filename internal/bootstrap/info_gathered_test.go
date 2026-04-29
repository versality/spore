package bootstrap

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectInfoGathered(t *testing.T) {
	cases := []struct {
		name     string
		ig       *InfoGathered
		wantErr  string
		wantNote string
	}{
		{
			name:    "missing sentinel",
			ig:      nil,
			wantErr: "info-gathered.json",
		},
		{
			name: "valid with no tools",
			ig: &InfoGathered{
				Tickets:   InfoSurface{Tool: "none"},
				Knowledge: InfoSurface{Tool: "none"},
			},
			wantNote: "tickets=none knowledge=none",
		},
		{
			name: "linear + notion with creds refs",
			ig: &InfoGathered{
				Tickets:   InfoSurface{Tool: "linear", CredsRef: "spore.creds.linear"},
				Knowledge: InfoSurface{Tool: "notion", CredsRef: "spore.creds.notion"},
			},
			wantNote: "tickets=linear knowledge=notion",
		},
		{
			name: "linear without creds_ref",
			ig: &InfoGathered{
				Tickets:   InfoSurface{Tool: "linear"},
				Knowledge: InfoSurface{Tool: "none"},
			},
			wantErr: "tickets.tool is set but tickets.creds_ref",
		},
		{
			name: "knowledge tool unknown",
			ig: &InfoGathered{
				Tickets:   InfoSurface{Tool: "none"},
				Knowledge: InfoSurface{Tool: "evernote"},
			},
			wantErr: "knowledge.tool",
		},
		{
			name: "ticket tool unknown",
			ig: &InfoGathered{
				Tickets:   InfoSurface{Tool: "trello"},
				Knowledge: InfoSurface{Tool: "none"},
			},
			wantErr: "tickets.tool",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, stateDir := fixture(t)
			if tc.ig != nil {
				writeJSON(t, filepath.Join(stateDir, "info-gathered.json"), tc.ig)
			}
			notes, err := detectInfoGathered(root)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v; want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if notes != tc.wantNote {
				t.Errorf("notes=%q; want %q", notes, tc.wantNote)
			}
		})
	}
}
