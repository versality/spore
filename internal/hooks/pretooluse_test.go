package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func mkBashReq(t *testing.T, cmd string) Request {
	t.Helper()
	in, err := json.Marshal(BashInput{Command: cmd})
	if err != nil {
		t.Fatal(err)
	}
	return Request{
		HookEventName: "PreToolUse",
		ToolName:      "Bash",
		ToolInput:     in,
	}
}

func TestPreToolUse_DefaultForbidden(t *testing.T) {
	cases := []struct {
		name      string
		cmd       string
		wantDeny  bool
		reasonHas string
	}{
		{"plain-ls", "ls -la", false, ""},
		{"sudo-anything", "sudo apt update", true, "sudo"},
		{"sudo-mid-pipe", "ls | sudo tee /etc/x", false, ""},
		{"rm-rf-root", "rm -rf /", true, "rm -rf"},
		{"rm-rf-subdir", "rm -rf /tmp/foo", false, ""},
		{"git-push-force", "git push --force origin main", true, "force"},
		{"git-push-f-flag", "git push -f origin main", true, "force"},
		{"git-push-plain", "git push origin main", false, ""},
	}
	def := DefaultForbidden()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := PreToolUse(mkBashReq(t, tc.cmd), def)
			if !tc.wantDeny {
				if resp.HookSpecificOutput != nil && resp.HookSpecificOutput.PermissionDecision == Deny {
					t.Fatalf("expected allow, got deny: %s", resp.HookSpecificOutput.PermissionDecisionReason)
				}
				return
			}
			if resp.HookSpecificOutput == nil || resp.HookSpecificOutput.PermissionDecision != Deny {
				t.Fatalf("expected deny response, got %+v", resp)
			}
			if !strings.Contains(resp.HookSpecificOutput.PermissionDecisionReason, tc.reasonHas) {
				t.Fatalf("reason %q does not contain %q", resp.HookSpecificOutput.PermissionDecisionReason, tc.reasonHas)
			}
		})
	}
}

func TestPreToolUse_NonBashAllowed(t *testing.T) {
	req := Request{
		HookEventName: "PreToolUse",
		ToolName:      "Read",
		ToolInput:     json.RawMessage(`{"file_path":"/etc/passwd"}`),
	}
	resp := PreToolUse(req, DefaultForbidden())
	if resp.HookSpecificOutput != nil {
		t.Fatalf("expected no decision for non-Bash tool, got %+v", resp)
	}
}

func TestPreToolUse_MalformedInput(t *testing.T) {
	req := Request{
		HookEventName: "PreToolUse",
		ToolName:      "Bash",
		ToolInput:     json.RawMessage(`not-json`),
	}
	resp := PreToolUse(req, DefaultForbidden())
	if resp.HookSpecificOutput != nil {
		t.Fatalf("expected no decision on malformed input, got %+v", resp)
	}
}
