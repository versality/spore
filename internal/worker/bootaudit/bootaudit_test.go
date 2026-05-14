package bootaudit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditMinimal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	// One metadata record, one user brief, one assistant turn with usage,
	// one user with a tool_result error, one assistant ToolSearch call.
	jsonl := strings.Join([]string{
		`{"type":"custom-title"}`,
		`{"type":"user","message":{"role":"user","content":"boot the worker on slug X"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"orienting"}],"usage":{"input_tokens":6,"cache_creation_input_tokens":11650,"cache_read_input_tokens":14915,"output_tokens":42}}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_x","is_error":true,"content":[{"type":"text","text":"Exit code 2"}]}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_y","name":"ToolSearch","input":{"query":"select:TaskCreate"}}],"usage":{"input_tokens":6,"cache_creation_input_tokens":3000,"cache_read_input_tokens":26565,"output_tokens":50}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}

	prof, err := Audit(Config{SessionPath: path})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if prof.Turn1CacheCreation != 11650 || prof.Turn1CacheRead != 14915 {
		t.Fatalf("turn1 tokens wrong: cc=%d cr=%d", prof.Turn1CacheCreation, prof.Turn1CacheRead)
	}
	if prof.BootToolErrors != 1 {
		t.Fatalf("boot tool errors: got %d want 1", prof.BootToolErrors)
	}
	if prof.ToolSearchRounds != 1 {
		t.Fatalf("toolsearch rounds: got %d want 1", prof.ToolSearchRounds)
	}
	if prof.BriefBytes != len("boot the worker on slug X") {
		t.Fatalf("brief bytes wrong: got %d", prof.BriefBytes)
	}
	if prof.AssistantTurns != 2 {
		t.Fatalf("assistant turns: got %d want 2", prof.AssistantTurns)
	}
	if len(prof.Findings) != 1 || !strings.Contains(prof.Findings[0], "boot-tool-errors") {
		t.Fatalf("expected boot-tool-errors finding, got %v", prof.Findings)
	}
}

func TestAuditMCPServersUsed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	jsonl := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"start"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"mcp__github__get_pull_request","input":{}},{"type":"tool_use","id":"b","name":"mcp__kagi-ken-mcp__kagi_search_fetch","input":{}},{"type":"tool_use","id":"c","name":"mcp__github__list_commits","input":{}}],"usage":{"input_tokens":1,"cache_creation_input_tokens":1000,"cache_read_input_tokens":2000,"output_tokens":5}}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}

	prof, err := Audit(Config{SessionPath: path})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"github", "kagi-ken-mcp"}
	if len(prof.MCPServersUsed) != 2 || prof.MCPServersUsed[0] != want[0] || prof.MCPServersUsed[1] != want[1] {
		t.Fatalf("mcp servers used: got %v want %v", prof.MCPServersUsed, want)
	}
}

func TestFormatShape(t *testing.T) {
	p := Profile{
		SessionPath:        "/x/y.jsonl",
		Turn1CacheCreation: 100,
		Turn1CacheRead:     200,
		Turn1InputTokens:   3,
		BootToolErrors:     0,
		ToolSearchRounds:   2,
		MCPServersUsed:     []string{"github", "kagi"},
		BriefBytes:         500,
		AssistantTurns:     7,
	}
	var buf bytes.Buffer
	Format(p, &buf)
	out := buf.String()
	for _, want := range []string{
		"boot-audit session=/x/y.jsonl",
		"turn1-tokens: 303 (cc=100 cr=200 ic=3)",
		"boot-tool-errors: 0",
		"toolsearch-rounds: 2",
		"mcp-servers-used: 2 (github,kagi)",
		"brief-bytes: 500",
		"assistant-turns: 7",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestAuditMissingFile(t *testing.T) {
	_, err := Audit(Config{SessionPath: "/nonexistent/x.jsonl"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
