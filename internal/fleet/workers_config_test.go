package fleet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/versality/spore/internal/task/frontmatter"
)

func TestParseWorkersTOML(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    WorkersConfig
		wantErr bool
	}{
		{
			name: "default only",
			input: `
[fleet.workers]
default = "claude"
`,
			want: WorkersConfig{Default: "claude"},
		},
		{
			name: "ratio and rules",
			input: `
[fleet.workers]
default = "claude"

[fleet.workers.ratio]
claude = 70
codex = 30

[fleet.workers.rules]
mechanical = "codex"
deep = "claude"
`,
			want: WorkersConfig{
				Default: "claude",
				Ratio:   map[string]int{"claude": 70, "codex": 30},
				Rules:   map[string]string{"mechanical": "codex", "deep": "claude"},
			},
		},
		{
			name: "comments and blanks",
			input: `
# top-level comment
[fleet.workers] # trailing
default = "codex" # inline
[fleet.workers.ratio]
codex = 100 # full codex
`,
			want: WorkersConfig{
				Default: "codex",
				Ratio:   map[string]int{"codex": 100},
			},
		},
		{
			name:  "other section ignored",
			input: "[fleet]\nmax_workers = 4\n[coordinator]\ndriver = \"claude\"\n",
			want:  WorkersConfig{},
		},
		{
			name:    "unknown key in fleet.workers",
			input:   "[fleet.workers]\nbogus = 1\n",
			wantErr: true,
		},
		{
			name:    "non-integer ratio",
			input:   "[fleet.workers.ratio]\nclaude = seventy\n",
			wantErr: true,
		},
		{
			name:    "negative ratio",
			input:   "[fleet.workers.ratio]\nclaude = -1\n",
			wantErr: true,
		},
		{
			name:    "malformed line",
			input:   "[fleet.workers]\nnoequals\n",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseWorkersTOML(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; result=%+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWorkersTOML: %v", err)
			}
			if !workersEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLoadWorkersConfigMissingFile(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadWorkersConfig(dir)
	if err != nil {
		t.Fatalf("LoadWorkersConfig: %v", err)
	}
	if !workersEqual(got, WorkersConfig{}) {
		t.Errorf("expected zero WorkersConfig for missing spore.toml, got %+v", got)
	}
}

func TestLoadWorkersConfigReadsFile(t *testing.T) {
	dir := t.TempDir()
	body := []byte("[fleet.workers]\ndefault = \"claude\"\n[fleet.workers.ratio]\nclaude = 60\ncodex = 40\n")
	if err := os.WriteFile(filepath.Join(dir, "spore.toml"), body, 0o600); err != nil {
		t.Fatalf("write spore.toml: %v", err)
	}
	got, err := LoadWorkersConfig(dir)
	if err != nil {
		t.Fatalf("LoadWorkersConfig: %v", err)
	}
	want := WorkersConfig{Default: "claude", Ratio: map[string]int{"claude": 60, "codex": 40}}
	if !workersEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSelectAgentExplicitWins(t *testing.T) {
	m := frontmatter.Meta{Agent: "codex"}
	cfg := WorkersConfig{Default: "claude", Rules: map[string]string{"deep": "claude"}}
	if got := SelectAgent(m, cfg, nil); got != "codex" {
		t.Errorf("explicit agent: got %q, want codex", got)
	}
}

func TestSelectAgentRuleByComplexity(t *testing.T) {
	m := frontmatter.Meta{Extra: map[string]string{"complexity": "mechanical"}}
	cfg := WorkersConfig{
		Default: "claude",
		Rules:   map[string]string{"mechanical": "codex", "deep": "claude"},
		Ratio:   map[string]int{"claude": 100},
	}
	if got := SelectAgent(m, cfg, nil); got != "codex" {
		t.Errorf("rule: got %q, want codex (rule wins over ratio)", got)
	}
}

func TestSelectAgentRuleMissesFallsThrough(t *testing.T) {
	m := frontmatter.Meta{Extra: map[string]string{"complexity": "exotic"}}
	cfg := WorkersConfig{
		Default: "claude",
		Rules:   map[string]string{"mechanical": "codex"},
	}
	if got := SelectAgent(m, cfg, nil); got != "claude" {
		t.Errorf("unmatched rule: got %q, want claude", got)
	}
}

func TestSelectAgentRatioBalances(t *testing.T) {
	cfg := WorkersConfig{Ratio: map[string]int{"claude": 70, "codex": 30}}
	// First spawn: empty counts -> highest target wins.
	if got := SelectAgent(frontmatter.Meta{}, cfg, map[string]int{}); got != "claude" {
		t.Errorf("first spawn: got %q, want claude", got)
	}
	// After 1 claude, 0 codex (total 1): claude share 100% > target 70%,
	// codex share 0% < target 30%; codex wins.
	if got := SelectAgent(frontmatter.Meta{}, cfg, map[string]int{"claude": 1}); got != "codex" {
		t.Errorf("after 1 claude: got %q, want codex", got)
	}
	// After 7 claude, 3 codex: ratio is on target; alphabetical tiebreak picks claude.
	if got := SelectAgent(frontmatter.Meta{}, cfg, map[string]int{"claude": 7, "codex": 3}); got != "claude" {
		t.Errorf("on-target: got %q, want claude (alphabetical tiebreak)", got)
	}
	// After 7 claude, 2 codex: codex deficit > claude deficit; codex wins.
	if got := SelectAgent(frontmatter.Meta{}, cfg, map[string]int{"claude": 7, "codex": 2}); got != "codex" {
		t.Errorf("codex behind: got %q, want codex", got)
	}
}

func TestSelectAgentRatioConvergesAcrossPass(t *testing.T) {
	// Simulate ten consecutive picks against a 70/30 target with empty
	// running counts; the balancer should end at 7 claude, 3 codex.
	cfg := WorkersConfig{Ratio: map[string]int{"claude": 70, "codex": 30}}
	counts := map[string]int{}
	for i := 0; i < 10; i++ {
		picked := SelectAgent(frontmatter.Meta{}, cfg, counts)
		counts[picked]++
	}
	if counts["claude"] != 7 || counts["codex"] != 3 {
		t.Errorf("10-pick distribution = %+v, want claude=7 codex=3", counts)
	}
}

func TestSelectAgentZeroRatioFallsBack(t *testing.T) {
	cfg := WorkersConfig{Default: "claude", Ratio: map[string]int{"codex": 0}}
	if got := SelectAgent(frontmatter.Meta{}, cfg, nil); got != "claude" {
		t.Errorf("zero ratio: got %q, want claude default", got)
	}
}

func TestSelectAgentEmptyConfigDefaults(t *testing.T) {
	if got := SelectAgent(frontmatter.Meta{}, WorkersConfig{}, nil); got != DefaultWorkerAgent {
		t.Errorf("empty config: got %q, want %q", got, DefaultWorkerAgent)
	}
}

func workersEqual(a, b WorkersConfig) bool {
	if a.Default != b.Default {
		return false
	}
	if len(a.Ratio) != len(b.Ratio) {
		return false
	}
	for k, v := range a.Ratio {
		if b.Ratio[k] != v {
			return false
		}
	}
	if len(a.Rules) != len(b.Rules) {
		return false
	}
	for k, v := range a.Rules {
		if b.Rules[k] != v {
			return false
		}
	}
	return true
}
