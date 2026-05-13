package lints

import (
	"bufio"
	"bytes"
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
// source of truth; rendered files are derived. To opt a consumer in,
// add one or more "# target: <repo-relative-path>" lines anywhere in
// <ConsumersDir>/<name>.txt (composer skips comment lines so the
// directive is inert during rendering). Consumers without a target
// directive are skipped.
type ClaudeDrift struct {
	ConsumersDir string
	RulesDir     string
	RenderCmd    string
}

func (ClaudeDrift) Name() string { return "claude-drift" }

func (l ClaudeDrift) Run(root string) ([]Issue, error) {
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
