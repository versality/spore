package codex

import (
	"reflect"
	"testing"
	"time"
)

func TestApplyEnvLeavesDefaultsWhenUnset(t *testing.T) {
	for _, k := range []string{EnvPaneCmds, EnvWakeCmd, EnvWakeTTL, EnvPollSec, EnvStartupWait, EnvOnce} {
		t.Setenv(k, "")
	}
	cfg := InboxWatcherConfig{
		PaneCmds: []string{"codex-raw"},
	}
	cfg.ApplyEnv()
	if !reflect.DeepEqual(cfg.PaneCmds, []string{"codex-raw"}) {
		t.Fatalf("PaneCmds clobbered: %v", cfg.PaneCmds)
	}
	if cfg.WakeArgv != nil || cfg.WakePendingTTL != 0 || cfg.PollInterval != 0 || cfg.StartupWait != 0 || cfg.Once {
		t.Fatalf("unset env leaked into cfg: %+v", cfg)
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	t.Setenv(EnvPaneCmds, "claude:codex-raw : zsh")
	t.Setenv(EnvWakeCmd, "wt-task launch-coordinator 'foo bar'")
	t.Setenv(EnvWakeTTL, "120")
	t.Setenv(EnvPollSec, "2")
	t.Setenv(EnvStartupWait, "10")
	t.Setenv(EnvOnce, "1")

	cfg := InboxWatcherConfig{PaneCmds: []string{"old"}}
	cfg.ApplyEnv()

	wantPanes := []string{"claude", "codex-raw", "zsh"}
	if !reflect.DeepEqual(cfg.PaneCmds, wantPanes) {
		t.Fatalf("PaneCmds = %v, want %v", cfg.PaneCmds, wantPanes)
	}
	wantArgv := []string{"wt-task", "launch-coordinator", "foo bar"}
	if !reflect.DeepEqual(cfg.WakeArgv, wantArgv) {
		t.Fatalf("WakeArgv = %v, want %v", cfg.WakeArgv, wantArgv)
	}
	if cfg.WakePendingTTL != 120*time.Second {
		t.Fatalf("WakePendingTTL = %v, want 120s", cfg.WakePendingTTL)
	}
	if cfg.PollInterval != 2*time.Second {
		t.Fatalf("PollInterval = %v, want 2s", cfg.PollInterval)
	}
	if cfg.StartupWait != 10*time.Second {
		t.Fatalf("StartupWait = %v, want 10s", cfg.StartupWait)
	}
	if !cfg.Once {
		t.Fatalf("Once = false, want true")
	}
}

func TestApplyEnvIgnoresBadIntegers(t *testing.T) {
	t.Setenv(EnvWakeTTL, "not-an-int")
	t.Setenv(EnvPollSec, "0")
	t.Setenv(EnvStartupWait, "-5")
	cfg := InboxWatcherConfig{}
	cfg.ApplyEnv()
	if cfg.WakePendingTTL != 0 || cfg.PollInterval != 0 || cfg.StartupWait != 0 {
		t.Fatalf("bad int values leaked: %+v", cfg)
	}
}
