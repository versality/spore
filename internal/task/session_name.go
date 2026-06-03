package task

import (
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/agentpolicy/codex"
	"github.com/versality/spore/internal/sessionkind"
	"github.com/versality/spore/internal/task/frontmatter"
)

func tmuxSessionName(projectRoot, slug string, m frontmatter.Meta) (string, error) {
	tag, err := tierTag(m)
	if err != nil {
		return "", err
	}
	project := projectNameOrBase(projectRoot)
	return wtSessionName(project, slug, tag), nil
}

// TaskTmuxSession returns the canonical tmux session name for slug
// in the given project: m.Session when the task file records one,
// else the computed wt-style name.
func TaskTmuxSession(tasksDir, projectRoot, slug string) string {
	return taskTmuxSession(tasksDir, projectRoot, slug)
}

func taskTmuxSession(tasksDir, projectRoot, slug string) string {
	if m, err := readTaskMeta(tasksDir, slug); err == nil {
		if m.Session != "" {
			return m.Session
		}
		if session, err := tmuxSessionName(projectRoot, slug, m); err == nil {
			return session
		}
	}
	session, err := tmuxSessionName(projectRoot, slug, frontmatter.Meta{})
	if err != nil {
		return wtSessionName(projectNameOrBase(projectRoot), slug, "")
	}
	return session
}

func projectNameOrBase(projectRoot string) string {
	name, err := ProjectName(projectRoot)
	if err != nil || name == "" {
		return filepath.Base(projectRoot)
	}
	return name
}

func projectEmoji(name string) string {
	switch name {
	case "spore", "marketer":
		return "\U0001F41D"
	}
	icons := []string{
		"\U0001F98A", "\U0001F43C", "\U0001F428", "\U0001F981",
		"\U0001F42F", "\U0001F438", "\U0001F435", "\U0001F419",
		"\U0001F980", "\U0001F41D", "\U0001F98B", "\U0001F989",
		"\U0001F99A", "\U0001F427", "\U0001F9A6", "\U0001F422",
		"\U0001F42C", "\U0001F985", "\U0001F994", "\U0001F987",
		"\U0001F992", "\U0001F418", "\U0001F98F", "\U0001F993",
		"\U0001F998", "\U0001F42B", "\U0001F408", "\U0001F415",
		"\U0001F40E", "\U0001F404", "\U0001F416", "\U0001F411",
	}
	sum := 0
	for _, r := range name {
		sum += int(r)
	}
	return icons[sum%len(icons)]
}

func wtSessionName(project, slug, tag string) string {
	name := projectEmoji(project) + " " + project + "/" + slug
	if tag != "" {
		name += " [" + tag + "]"
	}
	return name
}

// CoordinatorSession returns the tmux session name for the singleton
// coordinator in the wt-emoji shape: "<emoji> <project>/coordinator",
// with no tier tag. ParseSession classifies this as Kind=Coordinator.
func CoordinatorSession(projectRoot string) string {
	return wtSessionName(projectNameOrBase(projectRoot), "coordinator", "")
}

func tierTag(m frontmatter.Meta) (string, error) {
	agent := m.Agent
	if agent == "" {
		agent = "claude"
	}
	if agent == "claude-code" {
		agent = "claude"
	}
	if agent == "codex" {
		effort, err := codex.EffortForTask(m.Extra["effort"], m.Extra["complexity"])
		if err != nil {
			return "", err
		}
		return "codex-" + effort, nil
	}
	tag := modelTier(m.Extra["model"], agent)
	if agent == "claude" {
		if effort := m.Extra["effort"]; effort != "" {
			tag += "_" + effort
		}
	}
	return tag, nil
}

func modelTier(model, agent string) string {
	switch {
	case strings.Contains(model, "opus"):
		return "opus"
	case strings.Contains(model, "sonnet"):
		return "sonnet"
	case strings.Contains(model, "haiku"):
		return "haiku"
	case strings.HasPrefix(model, "ollama/"):
		return "ollama"
	case strings.HasPrefix(model, "codex"):
		return "codex"
	}
	switch agent {
	case "claude", "":
		return "opus"
	case "opencode":
		return "ollama"
	case "aider", "shell":
		return agent
	default:
		return agent
	}
}

// ParsedSession is the canonical decomposition of a tmux session
// name. Name carries the raw input (including any tier-tag suffix).
// Kind is one of sessionkind.Worker, sessionkind.Coordinator, or ""
// (other).
type ParsedSession struct {
	Name    string
	Project string
	Slug    string
	Tag     string
	Kind    string
}

// ParseSession decomposes a tmux session name relative to project.
// Project is required: the wt-emoji shape needs project to find the
// "<project>/<slug>" anchor.
//
// Accepted shapes (all project-scoped):
//
//	"<rune> <project>/<slug>"           worker
//	"<rune> <project>/<slug> [tag]"     worker, tagged
//	"<project>/coordinator"             coordinator (no emoji prefix)
//	"<rune> <project>/coordinator"      coordinator
//
// Returns (zero, false) for anything else.
func ParseSession(name, project string) (ParsedSession, bool) {
	raw := name
	tag := ""
	if i := strings.LastIndex(name, " ["); i >= 0 && strings.HasSuffix(name, "]") {
		tag = name[i+2 : len(name)-1]
		name = name[:i]
	}
	mk := func(slug, kind string) (ParsedSession, bool) {
		return ParsedSession{
			Name:    raw,
			Project: project,
			Slug:    slug,
			Tag:     tag,
			Kind:    kind,
		}, true
	}

	if name == project+"/"+sessionkind.Coordinator {
		return mk("", sessionkind.Coordinator)
	}

	needle := " " + project + "/"
	i := strings.Index(name, needle)
	if i < 0 {
		return ParsedSession{}, false
	}
	tail := name[i+len(needle):]
	if tail == "" || strings.Contains(tail, "/") {
		return ParsedSession{}, false
	}
	if tail == sessionkind.Coordinator {
		return mk("", sessionkind.Coordinator)
	}
	return mk(tail, sessionkind.Worker)
}

// MatchSlug reports whether name is a worker session for (project,
// slug). Coordinator sessions never match.
func MatchSlug(name, project, slug string) bool {
	p, ok := ParseSession(name, project)
	return ok && p.Kind == sessionkind.Worker && p.Slug == slug
}
