package lints

import (
	"sort"
	"testing"
)

func TestAgentKillSwitches(t *testing.T) {
	base := AgentKillSwitches{
		AgentSession:      "alpha",
		AgentService:      "alpha.service",
		AgentServiceAllow: []string{"harness/alpha-lifecycle", "docs/alpha-lifecycle.md"},
		AgentProcesses:    []string{"alpha", "beta", "gamma"},
	}
	tests := []struct {
		name  string
		lint  AgentKillSwitches
		files map[string]string
		want  []string
	}{
		{
			name: "targeted kill of unrelated session is clean",
			lint: base,
			files: map[string]string{
				"harness/safe.sh": "#!/bin/bash\ntmux kill-session -t other\n",
			},
			want: nil,
		},
		{
			name: "literal self-kill is refused",
			lint: base,
			files: map[string]string{
				"harness/oops.sh": "#!/bin/bash\ntmux kill-session -t alpha\n",
			},
			want: []string{"harness/oops.sh:2"},
		},
		{
			name: "quoted self-kill is refused",
			lint: base,
			files: map[string]string{
				"harness/oops.sh": "#!/bin/bash\ntmux kill-session -t \"alpha\"\n",
			},
			want: []string{"harness/oops.sh:2"},
		},
		{
			name: "kill-session without explicit -t is refused",
			lint: base,
			files: map[string]string{
				"harness/oops.sh": "#!/bin/bash\ntmux kill-session\n",
			},
			want: []string{"harness/oops.sh:2"},
		},
		{
			name: "service stop outside allow is refused",
			lint: base,
			files: map[string]string{
				"harness/oops.sh": "#!/bin/bash\nsystemctl --user stop alpha.service\n",
			},
			want: []string{"harness/oops.sh:2"},
		},
		{
			name: "service restart inside allow is clean",
			lint: base,
			files: map[string]string{
				"harness/alpha-lifecycle": "#!/bin/bash\nsystemctl --user restart alpha.service\n",
			},
			want: nil,
		},
		{
			name: "pkill against agent process is refused",
			lint: base,
			files: map[string]string{
				"harness/oops.sh": "#!/bin/bash\npkill -f alpha\n",
			},
			want: []string{"harness/oops.sh:2"},
		},
		{
			name: "killall against agent process is refused",
			lint: base,
			files: map[string]string{
				"harness/oops.sh": "#!/bin/bash\nkillall beta\n",
			},
			want: []string{"harness/oops.sh:2"},
		},
		{
			name: "pkill against unrelated process is clean",
			lint: base,
			files: map[string]string{
				"harness/safe.sh": "#!/bin/bash\npkill -f unrelated\n",
			},
			want: nil,
		},
		{
			name: "skip_path excludes a file",
			lint: AgentKillSwitches{
				AgentSession: "alpha",
				SkipPath:     []string{"harness/test-*"},
			},
			files: map[string]string{
				"harness/test-fixture.sh": "tmux kill-session -t alpha\n",
				"harness/real.sh":         "tmux kill-session -t alpha\n",
			},
			want: []string{"harness/real.sh:1"},
		},
		{
			name: "scan_dirs limits the walk",
			lint: AgentKillSwitches{
				AgentSession: "alpha",
				ScanDirs:     []string{"harness"},
			},
			files: map[string]string{
				"harness/a.sh": "tmux kill-session -t alpha\n",
				"other/b.sh":   "tmux kill-session -t alpha\n",
			},
			want: []string{"harness/a.sh:1"},
		},
		{
			name: "empty config matches only the broad kill-session",
			lint: AgentKillSwitches{},
			files: map[string]string{
				"harness/a.sh": "tmux kill-session\n",
				"harness/b.sh": "tmux kill-session -t alpha\nsystemctl --user stop alpha.service\npkill alpha\n",
			},
			want: []string{"harness/a.sh:1"},
		},
		{
			name: "no scannable files is a no-op",
			lint: base,
			files: map[string]string{
				"data.json": "{}\n",
			},
			want: nil,
		},
		{
			name: "self-kill flagged even when only AgentSession set",
			lint: AgentKillSwitches{AgentSession: "alpha"},
			files: map[string]string{
				"harness/oops.sh": "tmux kill-session -t alpha\n",
			},
			want: []string{"harness/oops.sh:1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestRepo(t, tt.files)
			issues, err := tt.lint.Run(root)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := make([]string, 0, len(issues))
			for _, i := range issues {
				got = append(got, locTag(i))
			}
			sort.Strings(got)
			want := append([]string(nil), tt.want...)
			sort.Strings(want)
			if !equalStrings(got, want) {
				t.Fatalf("issues: got %v want %v (full: %+v)", got, want, issues)
			}
		})
	}
}
