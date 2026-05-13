package lints

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/align"
	"github.com/versality/spore/internal/composer"
)

// ClaudeDrift fails when a consumer's on-disk instruction target
// diverges from what the composer would render. The composer is the
// source of truth; rendered files are derived.
//
// Two adapter shapes:
//
//   - Default (file-based): enumerate <ConsumersDir>/<name>.txt; each
//     opts in by adding one or more "# target: <repo-relative-path>"
//     lines (composer skips comments so the directive is inert during
//     rendering). Use RenderCmd to swap the built-in composer.
//
//   - ConsumersCmd (composer-driven): shell out to a single command
//     that emits the full consumer set as JSON:
//     [{"name": "<id>", "target_path": "<repo-relative>",
//     "rendered_text": "<expected content>"}, ...]
//     Fits Nix-eval composers or anything that knows its consumers at
//     eval time. When set, ConsumersDir / RulesDir / RenderCmd are
//     ignored.
type ClaudeDrift struct {
	ConsumersDir string
	RulesDir     string
	RenderCmd    string
	ConsumersCmd string
}

type driftConsumer struct {
	Name         string `json:"name"`
	TargetPath   string `json:"target_path"`
	RenderedText string `json:"rendered_text"`
}

func (ClaudeDrift) Name() string { return "claude-drift" }

func (l ClaudeDrift) Run(root string) ([]Issue, error) {
	if strings.TrimSpace(l.ConsumersCmd) != "" {
		return l.runConsumersCmd(root)
	}
	consumersDir := l.ConsumersDir
	if consumersDir == "" {
		consumersDir = "rules/consumers"
	}
	rulesDir := l.RulesDir
	if rulesDir == "" {
		rulesDir = "rules"
	}
	absConsumers := filepath.Join(root, consumersDir)
	absRules := filepath.Join(root, rulesDir)

	entries, err := os.ReadDir(absConsumers)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	alignActive, err := align.Active(root)
	if err != nil {
		return nil, fmt.Errorf("claude-drift: read align state: %w", err)
	}
	opts := composer.Options{Predicates: map[string]bool{"align": alignActive}}

	var issues []Issue
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".txt")
		consumerPath := filepath.Join(absConsumers, e.Name())
		targets, err := readTargetDirectives(consumerPath)
		if err != nil {
			return nil, err
		}
		if len(targets) == 0 {
			continue
		}
		rendered, err := l.render(root, name, consumerPath, absRules, opts)
		if err != nil {
			return nil, fmt.Errorf("compose %s: %w", name, err)
		}
		for _, target := range targets {
			targetPath := filepath.Join(root, target)
			on, err := os.ReadFile(targetPath)
			if err != nil {
				if os.IsNotExist(err) {
					issues = append(issues, Issue{
						Path:    target,
						Message: fmt.Sprintf("missing render target for consumer %q", name),
					})
					continue
				}
				return nil, err
			}
			if string(on) != rendered {
				issues = append(issues, Issue{
					Path:    target,
					Message: fmt.Sprintf("drift vs composer (consumer %q); rerun render", name),
				})
			}
		}
	}
	return issues, nil
}

func (l ClaudeDrift) render(root, name, consumerPath, rulesDir string, opts composer.Options) (string, error) {
	if strings.TrimSpace(l.RenderCmd) == "" {
		return composer.Compose(rulesDir, consumerPath, opts)
	}
	cmd := exec.Command("sh", "-c", l.RenderCmd)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"SPORE_LINT_ROOT="+root,
		"SPORE_LINT_CONSUMER="+name,
		"SPORE_LINT_CONSUMER_FILE="+consumerPath,
		"SPORE_LINT_RULES_DIR="+rulesDir,
	)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, msg)
	}
	return out.String(), nil
}

func (l ClaudeDrift) runConsumersCmd(root string) ([]Issue, error) {
	cmd := exec.Command("sh", "-c", l.ConsumersCmd)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "SPORE_LINT_ROOT="+root)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			return nil, fmt.Errorf("consumers_cmd: %w", err)
		}
		return nil, fmt.Errorf("consumers_cmd: %w: %s", err, msg)
	}
	var consumers []driftConsumer
	if err := json.Unmarshal(out.Bytes(), &consumers); err != nil {
		return nil, fmt.Errorf("consumers_cmd: parse JSON: %w", err)
	}
	var issues []Issue
	for i, c := range consumers {
		if c.TargetPath == "" {
			return nil, fmt.Errorf("consumers_cmd: entry %d (%q) has empty target_path", i, c.Name)
		}
		targetPath := filepath.Join(root, c.TargetPath)
		on, err := os.ReadFile(targetPath)
		if err != nil {
			if os.IsNotExist(err) {
				issues = append(issues, Issue{
					Path:    c.TargetPath,
					Message: fmt.Sprintf("missing render target for consumer %q", c.Name),
				})
				continue
			}
			return nil, err
		}
		if string(on) != c.RenderedText {
			issues = append(issues, Issue{
				Path:    c.TargetPath,
				Message: fmt.Sprintf("drift vs composer (consumer %q); rerun render", c.Name),
			})
		}
	}
	return issues, nil
}

func readTargetDirectives(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var targets []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "#") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if v, ok := strings.CutPrefix(body, "target:"); ok {
			target := strings.TrimSpace(v)
			if target != "" {
				targets = append(targets, target)
			}
		}
	}
	return targets, scanner.Err()
}
