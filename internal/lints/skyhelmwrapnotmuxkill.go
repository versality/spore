package lints

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SkyhelmWrapNoTmuxKill enforces the skyhelm context-wrap invariant:
// wrap guidance must not tell skyhelm to kill its tmux session, must
// name the self-only restart command, and the skyhelm systemd unit
// must use KillMode=process. A previous regression had wrap docs tell
// skyhelm to `tmux kill-session`, which reaped in-flight rower
// sessions hanging off the same socket; this lint pins the fix.
//
// Port of nix-config harness/check-skyhelm-context-wrap-no-tmux-kill.sh.
// Project-policy-shaped: assumes the nix-config layout
// (nix/packages/wt, configs, docs, nix/features/skyhelm.nix) and runs
// only via `spore lint skyhelm-wrap-no-tmux-kill`.
type SkyhelmWrapNoTmuxKill struct {
	// ScanDirs are the wrap-code roots scanned for forbidden patterns
	// and the required self-restart mention. Repo-relative, forward
	// slashes. Default: nix/packages/wt, configs, docs.
	ScanDirs []string
	// ServiceFile holds the skyhelm systemd unit checked for
	// KillMode=process. Default: nix/features/skyhelm.nix.
	ServiceFile string
}

func (SkyhelmWrapNoTmuxKill) Name() string { return "skyhelm-wrap-no-tmux-kill" }

var (
	skyhelmForbiddenKill   = regexp.MustCompile(`tmux kill-session -t skyhelm|Wrap.*kill-session`)
	skyhelmSelfRestart     = []byte("wt task skyhelm self-restart")
	skyhelmKillModeProcess = []byte(`KillMode = "process";`)
)

func (l SkyhelmWrapNoTmuxKill) Run(root string) ([]Issue, error) {
	dirs := l.ScanDirs
	if len(dirs) == 0 {
		dirs = []string{"nix/packages/wt", "configs", "docs"}
	}
	service := l.ServiceFile
	if service == "" {
		service = "nix/features/skyhelm.nix"
	}

	markers := l.markers()
	if !anyExists(root, markers) {
		return nil, nil
	}

	files, err := listFiles(root, nil)
	if err != nil {
		return nil, err
	}

	var issues []Issue
	selfRestartSeen := false
	scanned := false
	for _, rel := range files {
		if !pathUnderAny(rel, dirs) {
			continue
		}
		scanned = true
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for i, raw := range strings.Split(string(body), "\n") {
			if isCommentLine(raw) {
				continue
			}
			if skyhelmForbiddenKill.MatchString(raw) {
				issues = append(issues, Issue{
					Path:    rel,
					Line:    i + 1,
					Message: "context-wrap guidance must not tell skyhelm to kill its tmux session",
				})
			}
		}
		if !selfRestartSeen && containsLine(body, skyhelmSelfRestart) {
			selfRestartSeen = true
		}
	}

	if scanned && !selfRestartSeen {
		issues = append(issues, Issue{
			Path:    strings.Join(dirs, ","),
			Message: "context-wrap guidance must name the self-only skyhelm restart command (wt task skyhelm self-restart)",
		})
	}

	if body, err := os.ReadFile(filepath.Join(root, service)); err == nil {
		if !containsLine(body, skyhelmKillModeProcess) {
			issues = append(issues, Issue{
				Path:    service,
				Message: `skyhelm.service must use KillMode = "process"; so restart cleanup cannot reap rower tmux sessions`,
			})
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return issues, nil
}

// markers names the nix-config-specific paths whose presence signals
// the lint should run. Outside that layout the lint stays silent so
// `spore lint skyhelm-wrap-no-tmux-kill` is safe to invoke from any
// repo (e.g. spore's own tree).
func (l SkyhelmWrapNoTmuxKill) markers() []string {
	out := []string{l.ServiceFile}
	if out[0] == "" {
		out[0] = "nix/features/skyhelm.nix"
	}
	for _, d := range l.ScanDirs {
		if d == "nix/packages/wt" || d == "nix/features" {
			out = append(out, d)
		}
	}
	if len(l.ScanDirs) == 0 {
		out = append(out, "nix/packages/wt")
	}
	return out
}

func anyExists(root string, paths []string) bool {
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			return true
		}
	}
	return false
}

func pathUnderAny(rel string, dirs []string) bool {
	rel = filepath.ToSlash(rel)
	for _, d := range dirs {
		d = strings.TrimSuffix(filepath.ToSlash(d), "/")
		if d == "" {
			continue
		}
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

func isCommentLine(raw string) bool {
	s := strings.TrimLeft(raw, " \t")
	if s == "" {
		return false
	}
	return s[0] == '#' || strings.HasPrefix(s, "//")
}

func containsLine(body, needle []byte) bool {
	for _, ln := range strings.Split(string(body), "\n") {
		if isCommentLine(ln) {
			continue
		}
		if strings.Contains(ln, string(needle)) {
			return true
		}
	}
	return false
}
