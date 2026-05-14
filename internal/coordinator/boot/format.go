package boot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// render assembles the final boot summary. Order is: header banner,
// state.md size header, inline state.md body (when present), wt task
// ls + fleet + budget + agents (when not ok-rolled), sla on fail,
// silent-on-ok sections that broke, comm-feedback when present,
// trailing `ok: <names>` rollup.
func render(cfg Config, state StateInfo, probes []ProbeResult) string {
	var b strings.Builder
	ts := cfg.Now().Format("2006-01-02T15:04:05Z")
	fmt.Fprintf(&b, "=== skyhelm boot summary %s (host=%s) ===\n", ts, cfg.Hostname())

	renderState(&b, state)

	okNames := []string{}
	commPresent := false

	// First pass: emit always-on sections in fixed order.
	emitNamed(&b, probes, "task-ls", func(p ProbeResult) string {
		return renderTaskLS(cfg, p.RC, p.Out)
	})
	emitNamed(&b, probes, "fleet-status", nil)
	emitNamed(&b, probes, "budget-summary", nil)

	// agents-state: silent on ok, otherwise emitted as a section.
	for _, p := range probes {
		if p.Name != "agents-state" {
			continue
		}
		if p.RC == 0 && strings.HasPrefix(p.Out, "ok: ") {
			okNames = append(okNames, strings.TrimSpace(strings.TrimPrefix(p.Out, "ok: ")))
		} else {
			writeSection(&b, p.Title, p.RC, p.Out)
		}
	}

	// SLA scanner: emit-on-fail with cap + sidecar write.
	for _, p := range probes {
		if p.Name != "sla-scanner" {
			continue
		}
		if p.Out != "" {
			writeSLALatest(cfg, p.Out)
		}
		if p.RC != 0 {
			writeSection(&b, p.Title, p.RC, capSLAOutput(cfg, p.Out))
		}
	}

	// comm-feedback: emitted as a section when present, rolled in when not.
	for _, p := range probes {
		if p.Name != "comm-feedback" {
			continue
		}
		if p.Out != "" {
			commPresent = true
			writeSection(&b, p.Title, 0, p.Out)
		}
	}

	// Remaining silent-on-ok probes: emit section on fail / non-ok output,
	// otherwise contribute to the rollup line.
	silent := []string{
		"opencode-liveness", "coordinator-monitor",
		"coordinator-state-debt", "idle-watchdog", "rower-stop-errors",
	}
	for _, name := range silent {
		for _, p := range probes {
			if p.Name != name {
				continue
			}
			if p.RC == 0 && isOKOutput(p.Out) {
				okNames = append(okNames, p.OKShort)
			} else {
				writeSection(&b, p.Title, p.RC, p.Out)
			}
		}
	}

	if !commPresent {
		for _, p := range probes {
			if p.Name == "comm-feedback" {
				okNames = append(okNames, p.OKShort)
			}
		}
	}

	if len(okNames) > 0 {
		fmt.Fprintf(&b, "\nok: %s\n", strings.Join(okNames, ", "))
	}
	return b.String()
}

func renderState(b *strings.Builder, state StateInfo) {
	if !state.Exists {
		fmt.Fprintf(b, "\n## state.md (path=%s)\n", state.Path)
		fmt.Fprintln(b, "(missing - create from template before reconciling)")
		return
	}
	rcSuffix := ""
	if state.RC != 0 {
		rcSuffix = fmt.Sprintf(" [exit=%d]", state.RC)
	}
	fmt.Fprintf(b, "\n## state.md (path=%s, lines=%d, bytes=%d)%s\n",
		state.Path, state.Lines, state.Bytes, rcSuffix)
	fmt.Fprintf(b, "first-line: %s\n", state.FirstLine)
	if len(state.Oversized) > 0 {
		fmt.Fprintf(b, "oversized:%s - trim Recent events / Active tasks to bring state.md back under cap.\n",
			oversizedSuffix(state.Oversized))
	}

	fmt.Fprintf(b, "\n## state.md (inline)\n")
	b.Write(state.Body)
	if len(state.Body) == 0 || state.Body[len(state.Body)-1] != '\n' {
		b.WriteByte('\n')
	}
}

func oversizedSuffix(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	return " " + strings.Join(reasons, " ")
}

// emitNamed walks probes once, finds the named probe, and renders its
// section. transform overrides body rendering (used by task-ls). A
// probe whose Out is empty falls back to "(no output)" so the section
// header isn't followed by a blank line.
func emitNamed(b *strings.Builder, probes []ProbeResult, name string, transform func(ProbeResult) string) {
	for _, p := range probes {
		if p.Name != name {
			continue
		}
		out := p.Out
		if transform != nil {
			out = transform(p)
		}
		writeSection(b, p.Title, p.RC, out)
		return
	}
}

func writeSection(b *strings.Builder, title string, rc int, body string) {
	fmt.Fprintf(b, "\n## %s", title)
	if rc != 0 {
		fmt.Fprintf(b, " [exit=%d]", rc)
	}
	b.WriteByte('\n')
	if body == "" {
		fmt.Fprintln(b, "(no output)")
		return
	}
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
}

// isOKOutput matches the bash predicate: empty body counts as ok; any
// "ok" prefix counts as ok. Anything else (a warning line, a stderr
// drip, a probe rendering a diagnostic on rc=0) gets surfaced.
func isOKOutput(out string) bool {
	t := strings.TrimSpace(out)
	if t == "" || t == "ok" {
		return true
	}
	if strings.HasPrefix(t, "ok") {
		if len(t) == 2 {
			return true
		}
		c := t[2]
		if c == ' ' || c == ':' || c == '\t' {
			return true
		}
	}
	return false
}

func capSLAOutput(cfg Config, out string) string {
	if out == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) <= cfg.SLACap {
		return strings.Join(lines, "\n") + "\n"
	}
	kept := lines[:cfg.SLACap]
	footer := fmt.Sprintf("(%d more, full at %s)", len(lines)-cfg.SLACap, slaLatestPath(cfg))
	return strings.Join(append(kept, footer), "\n") + "\n"
}

func slaLatestPath(cfg Config) string {
	return filepath.Join(cfg.StateDir, "sla-scan-latest.txt")
}

func writeSLALatest(cfg Config, body string) {
	path := slaLatestPath(cfg)
	tmp := path + ".tmp"
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
