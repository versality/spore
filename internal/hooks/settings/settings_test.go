package settings

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConfig = `{
  "events": {
    "Stop": [
      {"command": "coord-only", "kinds": ["coordinator"]},
      {"command": "worker-only", "kinds": ["worker"]},
      {"command": "everywhere"}
    ]
  }
}`

func TestParseAndHookBins(t *testing.T) {
	cfg, err := Parse([]byte(sampleConfig))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	bins := cfg.HookBins()["Stop"]
	if len(bins) != 3 {
		t.Fatalf("want 3 Stop bins, got %d", len(bins))
	}
	if bins[0].BinPath != "coord-only" || bins[0].Kinds[0] != "coordinator" {
		t.Errorf("bin[0] mismatch: %+v", bins[0])
	}
	if bins[2].BinPath != "everywhere" || len(bins[2].Kinds) != 0 {
		t.Errorf("bin[2] mismatch: %+v", bins[2])
	}
}

func TestMergeExtrasEmpty(t *testing.T) {
	rendered := []byte(`{"hooks": {"Stop": [1]}}`)
	out, err := MergeExtras(rendered, nil)
	if err != nil {
		t.Fatalf("MergeExtras: %v", err)
	}
	if !bytes.Contains(out, []byte("Stop")) {
		t.Errorf("rendered output lost Stop event: %s", out)
	}
	if !bytes.HasSuffix(out, []byte{'\n'}) {
		t.Errorf("output missing trailing newline")
	}
}

func TestMergeExtrasOverrides(t *testing.T) {
	rendered := []byte(`{"hooks": {"Stop": [1]}, "x": 1}`)
	extras := []byte(`{"x": 2, "y": 3}`)
	out, err := MergeExtras(rendered, extras)
	if err != nil {
		t.Fatalf("MergeExtras: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"x": 2`) || !strings.Contains(s, `"y": 3`) {
		t.Errorf("merge did not apply extras: %s", s)
	}
}

func TestRenderClaudeMissingConfigIsNoop(t *testing.T) {
	dir := t.TempDir()
	out, ok, err := RenderClaude(filepath.Join(dir, "absent.json"), "", "")
	if err != nil {
		t.Fatalf("RenderClaude: %v", err)
	}
	if ok || out != nil {
		t.Errorf("expected (nil,false), got (%q, %v)", out, ok)
	}
}

func TestRenderClaudeMergesExtras(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks-config.json")
	ep := filepath.Join(dir, "extras.json")
	if err := os.WriteFile(hp, []byte(sampleConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ep, []byte(`{"permissions":{"allow":["x"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok, err := RenderClaude(hp, ep, "worker")
	if err != nil || !ok {
		t.Fatalf("RenderClaude: ok=%v err=%v", ok, err)
	}
	s := string(out)
	if !strings.Contains(s, "worker-only") {
		t.Errorf("worker render missing worker-only: %s", s)
	}
	if strings.Contains(s, "coord-only") {
		t.Errorf("worker render leaked coord-only: %s", s)
	}
	if !strings.Contains(s, `"allow"`) {
		t.Errorf("extras overlay not applied: %s", s)
	}
}

func TestRenderClaudeMissingExtrasIsOptional(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks-config.json")
	if err := os.WriteFile(hp, []byte(sampleConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok, err := RenderClaude(hp, filepath.Join(dir, "absent.json"), "")
	if err != nil || !ok {
		t.Fatalf("RenderClaude: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(string(out), "everywhere") {
		t.Errorf("render missing fallthrough binding: %s", out)
	}
}

func TestRenderCodex(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks-config.json")
	if err := os.WriteFile(hp, []byte(sampleConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok, err := RenderCodex(hp, "worker")
	if err != nil || !ok {
		t.Fatalf("RenderCodex: ok=%v err=%v", ok, err)
	}
	s := string(out)
	if strings.Contains(s, "$schema") || strings.Contains(s, "permissions") {
		t.Errorf("codex render must not carry claude-specific keys: %s", s)
	}
	if !strings.Contains(s, "worker-only") || strings.Contains(s, "coord-only") {
		t.Errorf("codex worker filter mismatch: %s", s)
	}
}
