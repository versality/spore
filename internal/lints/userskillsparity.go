package lints

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// UserSkillsParity asserts that for each Host the userSkills set
// matches the actual home.file / xdg.configFile entries under each
// configured surface (claude, codex, opencode...). Defaults match
// the nix-config layout.
//
// Run shells out to `nix eval --json` against the project flake at
// root; nix must be on PATH. With no Hosts configured, the lint is
// a no-op.
type UserSkillsParity struct {
	Hosts    []string
	Surfaces []SkillSurface
}

// SkillSurface describes one skill mount-point on a host: a label
// (e.g. "claude"), the file set the home-manager module installs
// the skill into ("home" for home.file, "xdg" for xdg.configFile),
// and the attribute prefix under that file set (e.g.
// ".claude/skills"). Skills under userSkills must surface at
// <Prefix>/<skill> in the matching file set.
type SkillSurface struct {
	Label   string
	FileSet string
	Prefix  string
}

func (UserSkillsParity) Name() string { return "user-skills-parity" }

// DefaultSkillSurfaces is the surface set nix-config wires.
var DefaultSkillSurfaces = []SkillSurface{
	{Label: "claude", FileSet: "home", Prefix: ".claude/skills"},
	{Label: "codex", FileSet: "home", Prefix: ".codex/skills"},
	{Label: "opencode", FileSet: "xdg", Prefix: "opencode/skills"},
}

func (l UserSkillsParity) Run(root string) ([]Issue, error) {
	if len(l.Hosts) == 0 {
		return nil, nil
	}
	surfaces := l.Surfaces
	if len(surfaces) == 0 {
		surfaces = DefaultSkillSurfaces
	}

	const homeApply = `files: builtins.listToAttrs (builtins.map (name: { inherit name; value = toString files.${name}.source; }) (builtins.filter (name: (builtins.match "\\.claude/skills/[^/]+" name) != null || (builtins.match "\\.codex/skills/[^/]+" name) != null) (builtins.attrNames files)))`
	const xdgApply = `files: builtins.listToAttrs (builtins.map (name: { inherit name; value = toString files.${name}.source; }) (builtins.filter (name: (builtins.match "opencode/skills/[^/]+" name) != null) (builtins.attrNames files)))`
	const userApply = `skills: builtins.mapAttrs (_: skill: toString skill.source) skills`

	var issues []Issue
	for _, host := range l.Hosts {
		userSkills, err := nixEvalJSON(root, host, "userSkills", userApply)
		if err != nil {
			return nil, fmt.Errorf("nix eval userSkills on %s: %w", host, err)
		}
		homeFiles, err := nixEvalJSON(root, host, "home-manager.users.sky.home.file", homeApply)
		if err != nil {
			return nil, fmt.Errorf("nix eval home.file on %s: %w", host, err)
		}
		xdgFiles, err := nixEvalJSON(root, host, "home-manager.users.sky.xdg.configFile", xdgApply)
		if err != nil {
			return nil, fmt.Errorf("nix eval xdg.configFile on %s: %w", host, err)
		}

		skillNames := sortedStringKeys(userSkills)
		for _, skill := range skillNames {
			expected := userSkills[skill]
			for _, s := range surfaces {
				files := homeFiles
				if s.FileSet == "xdg" {
					files = xdgFiles
				}
				target := s.Prefix + "/" + skill
				actual, ok := files[target]
				switch {
				case !ok:
					issues = append(issues, Issue{
						Path:    host + ":" + target,
						Message: fmt.Sprintf("missing %s skill target", s.Label),
					})
				case actual != expected:
					issues = append(issues, Issue{
						Path:    host + ":" + target,
						Message: fmt.Sprintf("%s target points at %s, expected %s", s.Label, actual, expected),
					})
				}
			}
		}

		for _, files := range []map[string]string{homeFiles, xdgFiles} {
			for _, target := range sortedStringKeys(files) {
				skill := target
				if i := strings.LastIndexByte(target, '/'); i >= 0 {
					skill = target[i+1:]
				}
				if _, ok := userSkills[skill]; !ok {
					issues = append(issues, Issue{
						Path:    host + ":" + target,
						Message: "skill target is not declared in userSkills",
					})
				}
			}
		}
	}
	return issues, nil
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func nixEvalJSON(root, host, attr, apply string) (map[string]string, error) {
	cmd := exec.Command("nix", "eval", "--json",
		fmt.Sprintf(".#nixosConfigurations.%s.config.%s", host, attr),
		"--apply", apply)
	cmd.Dir = root
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	var m map[string]string
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	return m, nil
}
