package lifecyclehooks

const (
	DriverClaude = "claude"
	DriverCodex  = "codex"
)

type Hook struct {
	Driver  string
	Event   string
	Command string
	Timeout int
	Kinds   []string
	Docs    []string
}

func Registry() []Hook {
	return []Hook{
		{
			Driver:  DriverCodex,
			Event:   "PreToolUse",
			Command: "spore hooks codex pre-tool-use",
			Timeout: 10,
			Docs:    []string{"https://developers.openai.com/codex/hooks"},
		},
		{
			Driver:  DriverCodex,
			Event:   "Stop",
			Command: "spore coordinator token-monitor",
			Timeout: 10,
			Kinds:   []string{"coordinator"},
			Docs:    []string{"https://developers.openai.com/codex/hooks"},
		},
		{
			Driver:  DriverCodex,
			Event:   "Stop",
			Command: "spore worker token-monitor",
			Timeout: 10,
			Kinds:   []string{"worker"},
			Docs:    []string{"https://developers.openai.com/codex/hooks"},
		},
		{
			Driver:  DriverCodex,
			Event:   "Stop",
			Command: "spore fleet replenish-hook",
			Timeout: 30,
			Kinds:   []string{"coordinator"},
			Docs:    []string{"https://developers.openai.com/codex/hooks"},
		},
		{
			Driver:  DriverCodex,
			Event:   "Stop",
			Command: "spore hooks plan-ready-mechanical",
			Timeout: 10,
			Kinds:   []string{"worker"},
			Docs:    []string{"https://developers.openai.com/codex/hooks"},
		},
		{
			Driver:  DriverCodex,
			Event:   "Stop",
			Command: "spore hooks watch-inbox",
			Timeout: 604800,
			Kinds:   []string{"coordinator", "worker"},
			Docs:    []string{"https://developers.openai.com/codex/hooks"},
		},
		{
			Driver:  DriverClaude,
			Event:   "Stop",
			Command: "spore coordinator token-monitor",
			Timeout: 10,
			Kinds:   []string{"coordinator"},
			Docs:    []string{"https://code.claude.com/docs/en/hooks"},
		},
		{
			Driver:  DriverClaude,
			Event:   "Stop",
			Command: "spore worker token-monitor",
			Timeout: 10,
			Kinds:   []string{"worker"},
			Docs:    []string{"https://code.claude.com/docs/en/hooks"},
		},
		{
			Driver:  DriverClaude,
			Event:   "Stop",
			Command: "spore fleet replenish-hook",
			Timeout: 30,
			Kinds:   []string{"coordinator"},
			Docs:    []string{"https://code.claude.com/docs/en/hooks"},
		},
		{
			Driver:  DriverClaude,
			Event:   "Stop",
			Command: "spore hooks wtmerge-mechanical",
			Timeout: 10,
			Kinds:   []string{"worker"},
			Docs:    []string{"https://code.claude.com/docs/en/hooks"},
		},
		{
			Driver:  DriverClaude,
			Event:   "Stop",
			Command: "spore hooks plan-ready-mechanical",
			Timeout: 10,
			Kinds:   []string{"worker"},
			Docs:    []string{"https://code.claude.com/docs/en/hooks"},
		},
		{
			Driver:  DriverClaude,
			Event:   "Stop",
			Command: "spore hooks worker-continue",
			Timeout: 10,
			Kinds:   []string{"worker"},
			Docs:    []string{"https://code.claude.com/docs/en/hooks"},
		},
		{
			Driver:  DriverClaude,
			Event:   "Stop",
			Command: "spore hooks push-pending",
			Timeout: 10,
			Kinds:   []string{"worker"},
			Docs:    []string{"https://code.claude.com/docs/en/hooks"},
		},
		{
			Driver:  DriverClaude,
			Event:   "Stop",
			Command: "spore hooks pr-finish",
			Timeout: 20,
			Kinds:   []string{"worker"},
			Docs:    []string{"https://code.claude.com/docs/en/hooks"},
		},
		{
			Driver:  DriverClaude,
			Event:   "Stop",
			Command: "spore hooks watch-inbox",
			Timeout: 604800,
			Kinds:   []string{"coordinator", "worker"},
			Docs:    []string{"https://code.claude.com/docs/en/hooks"},
		},
	}
}

func ForDriver(driver string) []Hook {
	var out []Hook
	for _, hook := range Registry() {
		if hook.Driver == driver {
			out = append(out, hook)
		}
	}
	return out
}
