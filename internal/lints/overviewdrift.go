package lints

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// OverviewDrift fails when Target's on-disk content diverges from the
// stdout of RenderCmd. The pieces are the source of truth; Target is
// derived. Defaults match nix-config: Target=OVERVIEW.md, RenderCmd=
// [bash, harness/render-overview.sh], FixHint="just overview-render".
type OverviewDrift struct {
	Target    string
	RenderCmd []string
	FixHint   string
}

func (OverviewDrift) Name() string { return "overview-drift" }

func (l OverviewDrift) Run(root string) ([]Issue, error) {
	target := l.Target
	if target == "" {
		target = "OVERVIEW.md"
	}
	cmd := l.RenderCmd
	if len(cmd) == 0 {
		cmd = []string{"bash", "harness/render-overview.sh"}
	}
	hint := l.FixHint
	if hint == "" {
		hint = "just overview-render"
	}

	abs := filepath.Join(root, target)
	have, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return []Issue{{
				Path:    target,
				Message: fmt.Sprintf("missing; run '%s' to regenerate, then commit", hint),
			}}, nil
		}
		return nil, err
	}

	c := exec.Command(cmd[0], cmd[1:]...)
	c.Dir = root
	var out, errBuf bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errBuf
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("render %v: %w (%s)", cmd, err, errBuf.String())
	}

	if !bytes.Equal(have, out.Bytes()) {
		return []Issue{{
			Path:    target,
			Message: fmt.Sprintf("drift vs render; run '%s' to regenerate, then commit", hint),
		}}, nil
	}
	return nil, nil
}
