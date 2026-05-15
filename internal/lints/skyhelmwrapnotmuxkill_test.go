package lints

import (
	"sort"
	"strings"
	"testing"
)

func TestSkyhelmWrapNoTmuxKill(t *testing.T) {
	const goodService = `{
  systemd.services.skyhelm = {
    serviceConfig.KillMode = "process";
  };
}
`
	const selfRestartDoc = "Restart skyhelm via `wt task skyhelm self-restart`.\n"

	tests := []struct {
		name  string
		files map[string]string
		want  []string // "path:msg-substring"
	}{
		{
			name: "clean tree: self-restart named, KillMode set, no forbidden kills",
			files: map[string]string{
				"docs/wrap.md":              selfRestartDoc,
				"nix/features/skyhelm.nix":  goodService,
				"nix/packages/wt/runner.sh": "#!/usr/bin/env bash\necho hi\n",
			},
		},
		{
			name: "forbidden kill-session in wrap code -> refuse",
			files: map[string]string{
				"docs/wrap.md":             selfRestartDoc + "tmux kill-session -t skyhelm\n",
				"nix/features/skyhelm.nix": goodService,
			},
			want: []string{"docs/wrap.md:context-wrap guidance must not tell skyhelm to kill its tmux session"},
		},
		{
			name: "forbidden Wrap.*kill-session phrasing -> refuse",
			files: map[string]string{
				"configs/wrap.toml":        selfRestartDoc + "Wrap should call kill-session on stop\n",
				"nix/features/skyhelm.nix": goodService,
			},
			want: []string{"configs/wrap.toml:context-wrap guidance must not tell skyhelm to kill its tmux session"},
		},
		{
			name: "unrelated kill in agent script outside wrap roots -> allow",
			files: map[string]string{
				"docs/wrap.md":             selfRestartDoc,
				"nix/features/skyhelm.nix": goodService,
				"harness/agent-stop.sh":    "tmux kill-session -t skyhelm\n",
			},
		},
		{
			name: "commented-out forbidden form -> allow",
			files: map[string]string{
				"docs/wrap.md":             selfRestartDoc + "# tmux kill-session -t skyhelm (banned)\n",
				"nix/features/skyhelm.nix": goodService,
			},
		},
		{
			name: "self-restart mention missing -> refuse",
			files: map[string]string{
				"docs/wrap.md":             "no mention of the restart command here\n",
				"nix/features/skyhelm.nix": goodService,
			},
			want: []string{"context-wrap guidance must name the self-only skyhelm restart command"},
		},
		{
			name: "service file missing KillMode -> refuse",
			files: map[string]string{
				"docs/wrap.md":             selfRestartDoc,
				"nix/features/skyhelm.nix": "{ services.skyhelm = {}; }\n",
			},
			want: []string{`nix/features/skyhelm.nix:skyhelm.service must use KillMode = "process"`},
		},
		{
			name: "no wrap roots and no service file -> clean",
			files: map[string]string{
				"README.md": "no wrap content here\n",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestRepo(t, tt.files)
			issues, err := (SkyhelmWrapNoTmuxKill{}).Run(root)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := make([]string, 0, len(issues))
			for _, i := range issues {
				got = append(got, i.Path+":"+i.Message)
			}
			sort.Strings(got)
			if len(got) != len(tt.want) {
				t.Fatalf("issues: got %v want %v", got, tt.want)
			}
			for i, want := range tt.want {
				if !strings.Contains(got[i], want) {
					t.Fatalf("issue[%d]=%q does not contain %q", i, got[i], want)
				}
			}
		})
	}
}

func TestSkyhelmWrapNoTmuxKill_Override(t *testing.T) {
	root := newTestRepo(t, map[string]string{
		"custom/wrap.md":   "tmux kill-session -t skyhelm\nwt task skyhelm self-restart\n",
		"path/skyhelm.nix": `KillMode = "process";` + "\n",
	})
	lint := SkyhelmWrapNoTmuxKill{
		ScanDirs:    []string{"custom"},
		ServiceFile: "path/skyhelm.nix",
	}
	issues, err := lint.Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d: %v", len(issues), issues)
	}
	if !strings.Contains(issues[0].Message, "must not tell skyhelm to kill") {
		t.Fatalf("unexpected message: %q", issues[0].Message)
	}
}
