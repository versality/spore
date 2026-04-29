package bootstrap

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectReadmeFollowed(t *testing.T) {
	cases := []struct {
		name     string
		readme   string
		rf       *ReadmeFollowed
		wantErr  string
		wantNote string
	}{
		{
			name:    "no README at all",
			wantErr: "no README at project root",
		},
		{
			name:    "README but no sentinel",
			readme:  "# x\n",
			wantErr: "no readme-followed.json",
		},
		{
			name:   "valid sentinel, all ok",
			readme: "# x\n",
			rf: &ReadmeFollowed{
				ReadmePath: "README.md",
				Items: []ReadmeFollowItem{
					{Step: "run install.sh", Status: readmeStatusOK},
					{Step: "set FOO env", Status: readmeStatusSkip, Comment: "n/a"},
				},
			},
			wantNote: "2 items walked",
		},
		{
			name:   "sentinel with fail items",
			readme: "# x\n",
			rf: &ReadmeFollowed{
				ReadmePath: "README.md",
				Items: []ReadmeFollowItem{
					{Step: "run install.sh", Status: readmeStatusFail},
				},
			},
			wantErr: "1/1 items marked fail",
		},
		{
			name:   "empty items array",
			readme: "# x\n",
			rf: &ReadmeFollowed{
				ReadmePath: "README.md",
				Items:      nil,
			},
			wantErr: "items is empty",
		},
		{
			name:   "unknown status",
			readme: "# x\n",
			rf: &ReadmeFollowed{
				ReadmePath: "README.md",
				Items:      []ReadmeFollowItem{{Step: "x", Status: "maybe"}},
			},
			wantErr: "items[0].status",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, stateDir := fixture(t)
			if tc.readme != "" {
				writeFile(t, filepath.Join(root, "README.md"), []byte(tc.readme))
			}
			if tc.rf != nil {
				writeJSON(t, filepath.Join(stateDir, "readme-followed.json"), tc.rf)
			}
			notes, err := detectReadmeFollowed(root)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v; want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if !strings.Contains(notes, tc.wantNote) {
				t.Errorf("notes=%q; want substring %q", notes, tc.wantNote)
			}
		})
	}
}
