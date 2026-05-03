package codexpolicy

import (
	"reflect"
	"testing"
)

func TestNormalizeEffort(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		def     string
		want    string
		wantErr bool
	}{
		{name: "default", def: DefaultCoordinatorEffort, want: "high"},
		{name: "low", input: "low", def: DefaultAgentEffort, want: "low"},
		{name: "xhigh", input: "xhigh", def: DefaultAgentEffort, want: "xhigh"},
		{name: "very high hyphen", input: "very-high", def: DefaultAgentEffort, want: "xhigh"},
		{name: "very high underscore", input: "very_high", def: DefaultAgentEffort, want: "xhigh"},
		{name: "invalid", input: "turbo", def: DefaultAgentEffort, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeEffort(tt.input, tt.def)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeEffort: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestEffortForTask(t *testing.T) {
	tests := []struct {
		effort     string
		complexity string
		want       string
	}{
		{effort: "very-high", complexity: "light", want: "xhigh"},
		{complexity: "light", want: "medium"},
		{complexity: "medium", want: "medium"},
		{complexity: "heavy", want: "high"},
		{complexity: "", want: "high"},
	}
	for _, tt := range tests {
		got, err := EffortForTask(tt.effort, tt.complexity)
		if err != nil {
			t.Fatalf("EffortForTask(%q, %q): %v", tt.effort, tt.complexity, err)
		}
		if got != tt.want {
			t.Errorf("EffortForTask(%q, %q) = %q want %q", tt.effort, tt.complexity, got, tt.want)
		}
	}
}

func TestInteractiveArgs(t *testing.T) {
	got := InteractiveArgs("gpt-5.5", "high")
	want := []string{
		"codex",
		"--dangerously-bypass-approvals-and-sandbox",
		"--no-alt-screen",
		"--disable", "apps",
		"-m", "gpt-5.5",
		"-c", `model_reasoning_effort="high"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args = %#v want %#v", got, want)
	}
}
