package hooks

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GitHook is one shell hook spore writes when `spore hooks install`
// runs. Name is the basename git invokes (commit-msg, pre-commit, …);
// Body is the shell script (rendered as-is, must start with the
// shebang line).
type GitHook struct {
	Name string
	Body string
}

// DefaultGitHooks returns the hook scripts spore installs by default.
// Each script shells out to `spore` so the policy lives in Go and the
// scripts stay one-line glue. Add to this list as new event handlers
// land; `spore hooks install` writes whatever Default returns.
func DefaultGitHooks() []GitHook {
	return []GitHook{
		{
			Name: "commit-msg",
			Body: "#!/usr/bin/env bash\nset -euo pipefail\nexec spore hooks commit-msg \"$1\"\n",
		},
	}
}

// Install writes hooks under <repoRoot>/.git/hooks-spore/ and points
// core.hooksPath at that directory. The directory name is fixed so
// repeat installs converge instead of accumulating siblings. Returns
// the absolute hooks-dir path on success.
func Install(repoRoot string, hooks []GitHook) (string, error) {
	if hooks == nil {
		hooks = DefaultGitHooks()
	}
	gitDir, err := resolveGitDir(repoRoot)
	if err != nil {
		return "", err
	}
	hooksDir := filepath.Join(gitDir, "hooks-spore")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", err
	}
	for _, h := range hooks {
		if err := writeHook(hooksDir, h); err != nil {
			return "", err
		}
	}
	if err := setCoreHooksPath(repoRoot, hooksDir); err != nil {
		return "", err
	}
	return hooksDir, nil
}

func writeHook(dir string, h GitHook) error {
	if h.Name == "" {
		return fmt.Errorf("hook name is empty")
	}
	if !strings.HasPrefix(h.Body, "#!") {
		return fmt.Errorf("hook %q body missing shebang", h.Name)
	}
	path := filepath.Join(dir, h.Name)
	if err := os.WriteFile(path, []byte(h.Body), 0o755); err != nil {
		return fmt.Errorf("write hook %s: %w", h.Name, err)
	}
	return nil
}

func resolveGitDir(repoRoot string) (string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--git-dir")
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git rev-parse --git-dir: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	dir := strings.TrimSpace(out.String())
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repoRoot, dir)
	}
	return dir, nil
}

func setCoreHooksPath(repoRoot, hooksDir string) error {
	cmd := exec.Command("git", "-C", repoRoot, "config", "core.hooksPath", hooksDir)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git config core.hooksPath: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

// CommitMsg is the hook implementation for git's commit-msg event. It
// reads msgPath, fails (returning a non-nil error) if the message
// contains an em-dash or en-dash. Mirrors the writing-style rule.
func CommitMsg(msgPath string) error {
	body, err := os.ReadFile(msgPath)
	if err != nil {
		return err
	}
	if bytes.ContainsAny(body, "\u2014\u2013") {
		return fmt.Errorf("commit message contains em-dash or en-dash; replace with a hyphen, colon, parentheses, or a new sentence")
	}
	return nil
}
