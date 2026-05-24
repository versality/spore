package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/versality/spore/internal/agentpreflight"
	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/hooks/settings"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

const doctorUsage = `spore doctor - report project readiness

Usage:
  spore doctor [--json]
`

type doctorReport struct {
	OK     bool                   `json:"ok"`
	Issues []agentpreflight.Issue `json:"issues"`
}

func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore doctor:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(doctorUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore doctor: unexpected positional args:", fs.Args())
		return 2
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore doctor:", err)
		return 1
	}
	report := buildDoctorReport(root)
	if *jsonOut {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore doctor:", err)
			return 1
		}
		fmt.Println(string(body))
	} else if report.OK {
		fmt.Println("doctor: ready")
	} else {
		fmt.Println("doctor: issues found")
		for _, issue := range report.Issues {
			fmt.Printf("  %s %s %s: %s\n", issue.Severity, issue.Code, issue.Tool, issue.Message)
		}
	}
	if !report.OK {
		return 1
	}
	return 0
}

func buildDoctorReport(root string) doctorReport {
	checker := agentpreflight.Checker{}
	var issues []agentpreflight.Issue
	issues = append(issues, checker.CheckRequiredTools(root)...)
	issues = append(issues, checker.CheckCoordinatorAgent(root)...)
	worker := frontmatter.Meta{}
	if cfg, err := fleet.LoadWorkersConfig(root); err == nil {
		worker.Agent = fleet.SelectAgent(worker, cfg, nil)
	}
	if worker.Agent == "" {
		worker.Agent = fleet.DefaultWorkerAgent
	}
	issues = append(issues, checker.CheckWorkerAgent(worker, root)...)
	issues = append(issues, hookConfigIssues(root, worker.Agent)...)
	issues = dedupeIssues(issues)
	ok := true
	for _, issue := range issues {
		if issue.Severity != agentpreflight.SeverityInfo {
			ok = false
			break
		}
	}
	return doctorReport{OK: ok, Issues: issues}
}

func hookConfigIssues(root, workerAgent string) []agentpreflight.Issue {
	var issues []agentpreflight.Issue
	if workerAgent == "codex" {
		source := filepath.Join(root, "configs", "codex", "hooks-config.json")
		runtime := filepath.Join(root, ".codex", "hooks.json")
		if _, err := os.Stat(source); os.IsNotExist(err) {
			issues = append(issues, agentpreflight.Issue{Severity: agentpreflight.SeverityWarn, Code: "missing-codex-hooks-config", Tool: "codex", Message: "configs/codex/hooks-config.json is missing"})
		} else {
			issues = append(issues, runtimeDriftIssue("codex", source, "", runtime, task.SessionKindWorker)...)
		}
	}
	if workerAgent == "" || workerAgent == "claude" || workerAgent == "claude-code" {
		source := filepath.Join(root, "configs", "claude", "hooks-config.json")
		extras := filepath.Join(root, "configs", "claude", "settings-extras.json")
		runtime := filepath.Join(root, ".claude", "settings.local.json")
		if _, err := os.Stat(source); os.IsNotExist(err) {
			issues = append(issues, agentpreflight.Issue{Severity: agentpreflight.SeverityWarn, Code: "missing-claude-hooks-config", Tool: "claude", Message: "configs/claude/hooks-config.json is missing"})
		} else {
			issues = append(issues, runtimeDriftIssue("claude", source, extras, runtime, task.SessionKindWorker)...)
		}
	}
	return issues
}

func runtimeDriftIssue(driver, source, extras, runtime, kind string) []agentpreflight.Issue {
	current, err := os.ReadFile(runtime)
	if err != nil {
		if os.IsNotExist(err) {
			return []agentpreflight.Issue{{Severity: agentpreflight.SeverityInfo, Code: "runtime-hooks-not-rendered", Tool: driver, Message: runtime + " will be rendered when a session spawns"}}
		}
		return []agentpreflight.Issue{{Severity: agentpreflight.SeverityWarn, Code: "runtime-hooks-unreadable", Tool: driver, Message: err.Error()}}
	}
	var rendered []byte
	var ok bool
	if driver == "codex" {
		rendered, ok, err = settings.RenderCodex(source, kind)
	} else {
		rendered, ok, err = settings.RenderClaude(source, extras, kind)
	}
	if err != nil {
		return []agentpreflight.Issue{{Severity: agentpreflight.SeverityWarn, Code: "hook-render-failed", Tool: driver, Message: err.Error()}}
	}
	if !ok {
		return nil
	}
	if !bytes.Equal(bytes.TrimSpace(current), bytes.TrimSpace(rendered)) {
		return []agentpreflight.Issue{{Severity: agentpreflight.SeverityWarn, Code: "runtime-hooks-drift", Tool: driver, Message: runtime + " differs from rendered source config"}}
	}
	return nil
}
