package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/versality/spore/internal/align"
)

// ReadmeFollowed is the persisted shape of the operator's confirmation
// that the project README's "to use, do X" instructions have been
// walked through. The skill writes this file after a low-confidence
// LLM check; the detector only validates structure.
type ReadmeFollowed struct {
	ReadmePath  string             `json:"readme_path"`
	Items       []ReadmeFollowItem `json:"items"`
	CompletedAt string             `json:"completed_at,omitempty"`
}

// ReadmeFollowItem is one "to use, do X" instruction the agent
// extracted from the README plus the operator's verdict.
type ReadmeFollowItem struct {
	Step    string `json:"step"`
	Status  string `json:"status"`
	Comment string `json:"comment,omitempty"`
}

const (
	readmeStatusOK   = "ok"
	readmeStatusSkip = "skip"
	readmeStatusFail = "fail"
)

func detectReadmeFollowed(root string) (string, error) {
	if root == "" {
		return "", errors.New("readme-followed: empty root")
	}
	if _, err := readmeAt(root); err != nil {
		return "", err
	}
	paths, err := align.Resolve(root)
	if err != nil {
		return "", err
	}
	jsonPath := filepath.Join(paths.StateDir, "readme-followed.json")
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no readme-followed.json under %s; invoke /spore-bootstrap so the agent walks the README with the operator (or write the file directly)", paths.StateDir)
		}
		return "", err
	}
	var rf ReadmeFollowed
	if err := json.Unmarshal(b, &rf); err != nil {
		return "", fmt.Errorf("parse %s: %w", jsonPath, err)
	}
	if rf.ReadmePath == "" {
		return "", errors.New("readme-followed.json: readme_path is empty")
	}
	if len(rf.Items) == 0 {
		return "", errors.New("readme-followed.json: items is empty; record the to-use steps the agent extracted")
	}
	failures := 0
	for i, it := range rf.Items {
		switch it.Status {
		case readmeStatusOK, readmeStatusSkip:
		case readmeStatusFail:
			failures++
		default:
			return "", fmt.Errorf("readme-followed.json: items[%d].status=%q; want one of ok/skip/fail", i, it.Status)
		}
	}
	if failures > 0 {
		return "", fmt.Errorf("readme-followed.json: %d/%d items marked fail; resolve them and re-run", failures, len(rf.Items))
	}
	return fmt.Sprintf("%d items walked from %s", len(rf.Items), rf.ReadmePath), nil
}

// readmeAt returns the path to a README in root, trying common
// casings. Reports a blocker error if none exists.
func readmeAt(root string) (string, error) {
	for _, name := range []string{"README.md", "README", "readme.md", "Readme.md", "README.rst"} {
		p := filepath.Join(root, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no README at project root; the readme-followed stage has nothing to walk")
}
