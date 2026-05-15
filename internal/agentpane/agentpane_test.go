package agentpane

import (
	"strings"
	"testing"
)

func fixedCapture(out string) CaptureFunc {
	return func(string) (string, error) { return out, nil }
}

func claudeCapture(body, prompt string) string {
	separator := strings.Repeat("─", 60)
	mode := "  -- INSERT -- ⏵⏵ bypass permissions on (shift+tab to cycle)         8000 tokens"
	parts := []string{}
	if body != "" {
		parts = append(parts, body, "")
	}
	parts = append(parts, separator, prompt, separator, mode)
	return strings.Join(parts, "\n")
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name       string
		agent      string
		out        string
		wantState  string
		wantDetail string
	}{
		{
			name:      "claude empty prompt is idle",
			agent:     "claude",
			out:       claudeCapture("● Read(/x)\n  ⎿  Read 12 lines", "❯ "),
			wantState: "idle",
		},
		{
			name:      "claude cogitating is running",
			agent:     "claude",
			out:       claudeCapture("✶ Cogitating… (53s · ↓ 2.2k tokens · thought for 4s)", "❯ "),
			wantState: "running",
		},
		{
			name:      "claude esc to interrupt is running",
			agent:     "claude",
			out:       claudeCapture("✶ Bashing… (1s · ↓ 0 tokens · esc to interrupt)", "❯ "),
			wantState: "running",
		},
		{
			name:      "claude interrupted banner with mode line is idle",
			agent:     "claude",
			out:       claudeCapture("Interrupted · What should Claude do instead?", "❯ "),
			wantState: "idle",
		},
		{
			name:      "claude no mode line is running",
			agent:     "claude",
			out:       "starting up...\n",
			wantState: "running",
		},
		{
			name:      "claude stop-hook chain without mode line is idle",
			agent:     "claude",
			out:       "● Bash(go test ./...)\n  ⎿  ok\n\n• Running Stop hook: Checking fleet state\n\n• Running 5 Stop hooks\n",
			wantState: "idle",
		},
		{
			name:      "claude stop-hook chain with busy spinner stays running",
			agent:     "claude",
			out:       "• Running Stop hook: Checking fleet state\n✶ Cogitating… (3s · esc to interrupt)\n",
			wantState: "running",
		},
		{
			name:      "codex stop-hook chain without prompt is idle",
			agent:     "codex",
			out:       "• Ran true\n\n• Running Stop hook: Checking fleet state\n\n• Running 5 Stop hooks\n",
			wantState: "idle",
		},
		{
			name:      "codex working is running",
			agent:     "codex",
			out:       "• Working (1m 04s • esc to interrupt)\n\n› queued input\n",
			wantState: "running",
		},
		{
			name:      "codex prompt is idle",
			agent:     "codex",
			out:       "• Ran true\n\n› Improve documentation in @filename\n",
			wantState: "idle",
		},
		{
			name:      "codex stop-hook log line is idle",
			agent:     "codex",
			out:       "• Running Stop hook: Checking fleet state\n\n• Running 5 Stop hooks\n\n› Improve documentation in @filename\n",
			wantState: "idle",
		},
		{
			name:       "codex interrupted is dead",
			agent:      "codex",
			out:        "■ Conversation interrupted - tell the model what to do differently.\n\n› Explain this codebase",
			wantState:  "dead",
			wantDetail: "codex conversation interrupted",
		},
		{
			name:      "opencode thinking is running",
			agent:     "opencode",
			out:       "Thinking about the next step\n",
			wantState: "running",
		},
		{
			name:      "opencode prompt is idle",
			agent:     "opencode",
			out:       "│ > waiting for input\n",
			wantState: "idle",
		},
		{
			name:      "unknown agent defaults to running",
			agent:     "aider",
			out:       "anything\n",
			wantState: "running",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, detail := Classify(fixedCapture(tc.out), "target", tc.agent)
			if state != tc.wantState {
				t.Errorf("state = %q, want %q", state, tc.wantState)
			}
			if tc.wantDetail != "" && detail != tc.wantDetail {
				t.Errorf("detail = %q, want %q", detail, tc.wantDetail)
			}
		})
	}
}

func TestClassifyCaptureErrorReturnsRunning(t *testing.T) {
	state, _ := Classify(func(string) (string, error) { return "", errBoom{} }, "t", "claude")
	if state != "running" {
		t.Errorf("state = %q, want running", state)
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }
