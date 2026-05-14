package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProbeMode controls how render() decides to surface a probe.
type ProbeMode int

const (
	ModeAlways ProbeMode = iota
	ModeSilentOnOK
	ModeEmitOnFail
	ModeFilePresence
	ModeTaskLS
	ModeAgents
)

// ProbeResult is one probe's outcome. Name is the short label for the
// silent-on-ok rollup line; Title is the section heading.
type ProbeResult struct {
	Name    string
	Title   string
	Mode    ProbeMode
	OKShort string
	RC      int
	Out     string
}

type probeDef struct {
	Name    string
	Title   string
	Mode    ProbeMode
	OKShort string
	Run     func() (int, string)
}

func probeDefs(cfg Config) []probeDef {
	openCodeLive := filepath.Join(cfg.Root, "harness", "opencode-rower-liveness.sh")
	stopErrors := filepath.Join(cfg.WTState, "rower-stop-errors.jsonl")

	return []probeDef{
		{
			Name: "task-ls", Title: "wt task ls", Mode: ModeTaskLS,
			Run: func() (int, string) { return cfg.Exec("wt", "task", "ls") },
		},
		{
			Name: "fleet-status", Title: "wt task fleet status", Mode: ModeAlways,
			Run: func() (int, string) { return cfg.Exec("wt", "task", "fleet", "status") },
		},
		{
			Name: "budget-summary", Title: "skyhelm-budget summary", Mode: ModeAlways,
			Run: func() (int, string) { return cfg.Exec("skyhelm-budget", "summary") },
		},
		{
			Name: "agents-state", Title: "wt task agents", Mode: ModeAgents,
			OKShort: "agents",
			Run:     func() (int, string) { return probeAgents(cfg) },
		},
		{
			Name: "sla-scanner", Title: "skyhelm-sla-scanner", Mode: ModeEmitOnFail,
			Run: func() (int, string) { return cfg.Exec("skyhelm-sla-scanner") },
		},
		{
			Name: "opencode-liveness", Title: "opencode rower liveness", Mode: ModeSilentOnOK,
			OKShort: "opencode liveness",
			Run:     func() (int, string) { return cfg.Exec(openCodeLive) },
		},
		{
			Name: "coordinator-monitor", Title: "spore coordinator monitor", Mode: ModeSilentOnOK,
			OKShort: "spore monitor",
			Run:     func() (int, string) { return cfg.Exec(cfg.SelfExe, "coordinator", "monitor") },
		},
		{
			Name: "reconcile-health", Title: "reconcile health", Mode: ModeSilentOnOK,
			OKShort: "reconcile health",
			Run:     func() (int, string) { return probeReconcileHealth(cfg) },
		},
		{
			Name: "coordinator-state-debt", Title: "spore coordinator state-debt", Mode: ModeSilentOnOK,
			OKShort: "state-debt",
			Run:     func() (int, string) { return cfg.Exec(cfg.SelfExe, "coordinator", "state-debt") },
		},
		{
			Name: "idle-watchdog", Title: "skyhelm-idle-watchdog", Mode: ModeSilentOnOK,
			OKShort: "idle watchdog",
			Run:     func() (int, string) { return cfg.Exec("skyhelm-idle-watchdog") },
		},
		{
			Name: "rower-stop-errors", Title: "rower Stop hook errors", Mode: ModeSilentOnOK,
			OKShort: "rower stop errors",
			Run:     func() (int, string) { return probeStopErrors(stopErrors) },
		},
		{
			Name: "comm-feedback", Title: "comm-feedback.ready", Mode: ModeFilePresence,
			OKShort: "comm-feedback (no)",
			Run:     func() (int, string) { return probeCommFeedback(cfg) },
		},
	}
}

// probeAgents shells out to `wt task agents status` and classifies the
// first line. Bash prints `ok: agents on` / `ok: agents off (auto-promote
// disabled)` / `failed: ...`; the silent-on-ok rollup picks up the `ok:`
// suffix verbatim.
func probeAgents(cfg Config) (int, string) {
	rc, raw := cfg.Exec("wt", "task", "agents", "status")
	if rc != 0 {
		return 2, fmt.Sprintf("failed: wt task agents status: %s\n", strings.TrimSpace(raw))
	}
	first := firstNonEmptyLine(raw)
	switch first {
	case "on":
		return 0, "ok: agents on\n"
	case "off":
		return 0, "ok: agents off (auto-promote disabled)\n"
	default:
		return 2, fmt.Sprintf("failed: unexpected wt task agents status: %s\n", first)
	}
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// probeStopErrors emits a `tail -n 5` snapshot of the rower-stop-errors
// ledger when it has any content. An empty / missing ledger is silent.
func probeStopErrors(path string) (int, string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return 0, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "unresolved rower Stop hook errors from %s:\n", path)
	for _, line := range lines {
		fmt.Fprintln(&b, line)
	}
	return 2, b.String()
}

// probeCommFeedback returns the comm-feedback.ready body when present;
// absence is silent and rolls into the `ok: ...` line via render().
func probeCommFeedback(cfg Config) (int, string) {
	path := filepath.Join(cfg.StateDir, "comm-feedback.ready")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "present: yes (path=%s)\n", path)
	b.Write(data)
	if !strings.HasSuffix(string(data), "\n") {
		b.WriteByte('\n')
	}
	return 0, b.String()
}
