package transcript

import (
	"path/filepath"
	"testing"
)

func TestLastUnfinalizedToolCalls_StuckFunctionCall(t *testing.T) {
	got, err := LastUnfinalizedToolCalls(filepath.Join("testdata", "codex-stuck-fcall.jsonl"))
	if err != nil {
		t.Fatalf("LastUnfinalizedToolCalls: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1: %+v", len(got), got)
	}
	c := got[0]
	if c.CallID != "call_A" || c.Name != "exec_command" || c.Kind != "function_call" {
		t.Fatalf("got %+v, want call_A/exec_command/function_call", c)
	}
}

func TestLastUnfinalizedToolCalls_StuckCustomToolCall(t *testing.T) {
	got, err := LastUnfinalizedToolCalls(filepath.Join("testdata", "codex-stuck-custom.jsonl"))
	if err != nil {
		t.Fatalf("LastUnfinalizedToolCalls: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1: %+v", len(got), got)
	}
	if got[0].CallID != "call_Patch" || got[0].Kind != "custom_tool_call" || got[0].Name != "apply_patch" {
		t.Fatalf("got %+v", got[0])
	}
}

func TestLastUnfinalizedToolCalls_Clean(t *testing.T) {
	got, err := LastUnfinalizedToolCalls(filepath.Join("testdata", "codex-clean.jsonl"))
	if err != nil {
		t.Fatalf("LastUnfinalizedToolCalls: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestLastUnfinalizedToolCalls_FileMissing(t *testing.T) {
	if _, err := LastUnfinalizedToolCalls("/nonexistent/path"); err == nil {
		t.Fatalf("err=nil on missing file")
	}
}

func TestLastUnfinalizedToolCalls_BadJSONIgnored(t *testing.T) {
	path := writeCodexJSONL(t, []string{
		`not-json`,
		`{"type":"response_item","payload":{"type":"function_call","name":"x","call_id":"c1"}}`,
		`{also not json`,
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"ok"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"y","call_id":"c2"}}`,
	})
	got, err := LastUnfinalizedToolCalls(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].CallID != "c2" {
		t.Fatalf("got %+v, want [c2]", got)
	}
}

func TestLastUnfinalizedToolCalls_OrderedByLineNum(t *testing.T) {
	path := writeCodexJSONL(t, []string{
		`{"type":"response_item","payload":{"type":"function_call","name":"first","call_id":"c1"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"second","call_id":"c2"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"third","call_id":"c3"}}`,
	})
	got, err := LastUnfinalizedToolCalls(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Name != "first" || got[1].Name != "second" || got[2].Name != "third" {
		t.Fatalf("order wrong: %+v", got)
	}
}

func TestLastUnfinalizedToolCalls_OutputBeforeOpenIgnored(t *testing.T) {
	path := writeCodexJSONL(t, []string{
		`{"type":"response_item","payload":{"type":"function_call_output","call_id":"orphan","output":"x"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"k","call_id":"c1"}}`,
	})
	got, err := LastUnfinalizedToolCalls(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0].CallID != "c1" {
		t.Fatalf("got %+v", got)
	}
}

func TestLastUnfinalizedToolCalls_NonResponseItemIgnored(t *testing.T) {
	path := writeCodexJSONL(t, []string{
		`{"type":"session_meta","payload":{"id":"s"}}`,
		`{"type":"token_count","last_token_usage":{"total_tokens":10}}`,
		`{"type":"event_msg","payload":{"type":"function_call","call_id":"c-fake","name":"nope"}}`,
	})
	got, err := LastUnfinalizedToolCalls(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}
