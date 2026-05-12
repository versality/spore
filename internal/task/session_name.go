package task

import (
	"path/filepath"
	"strings"

	"github.com/versality/spore/codexpolicy"
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
		return LegacySessionName(projectRoot, slug)
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

func LegacySessionName(projectRoot, slug string) string {
	project := projectNameOrBase(projectRoot)
	return sessionPath("spore", project, slug)
}

func sessionPath(wrap, project, slug string) string {
	if wrap == project {
		return wrap + "/" + slug
	}
	return wrap + "/" + project + "/" + slug
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
		effort, err := codexpolicy.EffortForTask(m.Extra["effort"], m.Extra["complexity"])
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

func slugFromSessionName(name, project string) (string, bool) {
	for _, prefix := range legacySessionPrefixes(project) {
		if strings.HasPrefix(name, prefix) {
			return cleanSessionSlug(strings.TrimPrefix(name, prefix))
		}
	}
	needle := project + "/"
	start := 0
	for {
		i := strings.Index(name[start:], needle)
		if i < 0 {
			return "", false
		}
		i += start
		if i == 0 || name[i-1] == ' ' {
			return cleanSessionSlug(name[i+len(needle):])
		}
		start = i + len(needle)
	}
}

func legacySessionPrefixes(project string) []string {
	prefixes := []string{"spore/" + project + "/"}
	if project == "spore" {
		prefixes = append(prefixes, "spore/")
	}
	return prefixes
}

func cleanSessionSlug(rest string) (string, bool) {
	if rest == "" {
		return "", false
	}
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}
