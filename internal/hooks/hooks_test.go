package hooks

import (
	"encoding/json"
	"testing"
)

func TestConstants(t *testing.T) {
	if Allow != "allow" {
		t.Errorf("Allow = %q, want allow", Allow)
	}
	if Deny != "deny" {
		t.Errorf("Deny = %q, want deny", Deny)
	}
	if Ask != "ask" {
		t.Errorf("Ask = %q, want ask", Ask)
	}
}

func TestRequestRoundtrip(t *testing.T) {
	req := Request{
		HookEventName:  "PreToolUse",
		SessionID:      "sess-123",
		TranscriptPath: "/tmp/t.jsonl",
		CWD:            "/work/project",
		ToolName:       "Bash",
		ToolInput:      json.RawMessage(`{"command":"ls"}`),
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var got Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.HookEventName != req.HookEventName {
		t.Errorf("HookEventName = %q, want %q", got.HookEventName, req.HookEventName)
	}
	if got.ToolName != req.ToolName {
		t.Errorf("ToolName = %q, want %q", got.ToolName, req.ToolName)
	}
	if string(got.ToolInput) != string(req.ToolInput) {
		t.Errorf("ToolInput = %s, want %s", got.ToolInput, req.ToolInput)
	}
}

func TestResponseOmitsEmptyFields(t *testing.T) {
	resp := Response{}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	// An empty Response should marshal to {} (no hook-specific output).
	s := string(b)
	if s != "{}" {
		t.Errorf("empty Response marshaled to %q, want {}", s)
	}
}

func TestHookSpecificOutputRoundtrip(t *testing.T) {
	out := HookSpecificOutput{
		HookEventName:            "PreToolUse",
		PermissionDecision:       Deny,
		PermissionDecisionReason: "sudo is forbidden",
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var got HookSpecificOutput
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.PermissionDecision != Deny {
		t.Errorf("PermissionDecision = %q, want %q", got.PermissionDecision, Deny)
	}
	if got.PermissionDecisionReason != "sudo is forbidden" {
		t.Errorf("PermissionDecisionReason = %q", got.PermissionDecisionReason)
	}
}

func TestStop(t *testing.T) {
	req := Request{HookEventName: "Stop"}
	resp := Stop(req)
	if resp.HookSpecificOutput != nil {
		t.Errorf("Stop: expected nil HookSpecificOutput, got %+v", resp.HookSpecificOutput)
	}
	if resp.SystemMessage != "" {
		t.Errorf("Stop: expected empty SystemMessage, got %q", resp.SystemMessage)
	}
}

func TestStopWithBashRequest(t *testing.T) {
	in, _ := json.Marshal(BashInput{Command: "ls"})
	req := Request{
		HookEventName: "Stop",
		ToolName:      "Bash",
		ToolInput:     in,
	}
	resp := Stop(req)
	if resp.HookSpecificOutput != nil {
		t.Error("Stop should return zero Response regardless of input")
	}
}
