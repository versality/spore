package claude

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
		{name: "default", def: DefaultAgentEffort, want: "default"},
		{name: "explicit default", input: "default", def: "high", want: "default"},
		{name: "low", input: "low", def: DefaultAgentEffort, want: "low"},
		{name: "xhigh", input: "xhigh", def: DefaultAgentEffort, want: "xhigh"},
		{name: "max", input: "max", def: DefaultAgentEffort, want: "max"},
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
		{effort: "default", complexity: "heavy", want: "default"},
		{effort: "very-high", complexity: "light", want: "xhigh"},
		{effort: "max", want: "max"},
		{complexity: "light", want: "medium"},
		{complexity: "medium", want: "medium"},
		{complexity: "heavy", want: "high"},
		{complexity: "", want: "default"},
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
	tests := []struct {
		name   string
		effort string
		want   []string
	}{
		{name: "missing effort", want: []string{"claude", "--dangerously-skip-permissions"}},
		{name: "default effort", effort: "default", want: []string{"claude", "--dangerously-skip-permissions"}},
		{name: "high effort", effort: "high", want: []string{"claude", "--dangerously-skip-permissions", "--effort", "high"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InteractiveArgs("ignored-model", tt.effort)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("args = %#v want %#v", got, tt.want)
			}
		})
	}
}
