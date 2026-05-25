package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	spore "github.com/versality/spore"
	"github.com/versality/spore/internal/agentpreflight"
	"github.com/versality/spore/internal/codextrust"
	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/hooks/inject"
	"github.com/versality/spore/internal/install"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

const installUsage = `spore install - drop spore assets into a target project

Usage:
  spore install [--root <path>]

Copies bundled assets into the target checkout:
  - skill bodies (spore-bootstrap, diagram) into <root>/.claude/skills/
  - generic harness shell scripts into <root>/harness/
  - missing hook source configs into <root>/configs/

Idempotent: re-runs only rewrite files whose contents drifted from the
embedded copy.

Flags:
  --root   Project root to install into. Defaults to the current
           directory.
`

func runInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	root := fs.String("root", "", "project root (default: cwd)")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore install:", err)
		fmt.Fprint(os.Stderr, installUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(installUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore install: unexpected positional args:", fs.Args())
		return 2
	}

	dest := *root
	if dest == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore install:", err)
			return 1
		}
		dest = cwd
	}

	skills, err := install.Install(dest, spore.BundledSkills, "bootstrap/skills", ".claude/skills")
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore install:", err)
		return 1
	}
	scripts, err := install.Install(dest, spore.BundledScripts, "bootstrap/scripts", "harness")
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore install:", err)
		return 1
	}
	configs, err := install.InstallIfMissing(dest, spore.BundledConfigs, "configs", "configs")
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore install:", err)
		return 1
	}
	codexRuntime, codexWrote, err := inject.InjectCodex(dest, dest, task.SessionKindCoordinator)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore install:", err)
		return 1
	}
	for _, p := range skills.Written {
		rel, _ := filepath.Rel(dest, p)
		fmt.Printf("wrote %s\n", rel)
	}
	for _, p := range scripts.Written {
		rel, _ := filepath.Rel(dest, p)
		fmt.Printf("wrote %s\n", rel)
	}
	for _, p := range configs.Written {
		rel, _ := filepath.Rel(dest, p)
		fmt.Printf("wrote %s\n", rel)
	}
	if codexWrote {
		rel, _ := filepath.Rel(dest, codexRuntime)
		fmt.Printf("wrote %s\n", rel)
	}
	total := len(skills.Written) + len(scripts.Written) + len(configs.Written)
	if codexWrote {
		total++
	}
	if total == 0 {
		fmt.Println("install: already up to date")
	} else {
		fmt.Printf("installed %d skill file(s) under %s/.claude/skills/, %d harness script(s) under %s/harness/, %d config file(s) under %s/configs/\n",
			len(skills.Written), dest, len(scripts.Written), dest, len(configs.Written), dest)
	}
	for _, line := range installReadinessWarnings(dest) {
		fmt.Fprintln(os.Stderr, line)
	}
	return 0
}

func installReadinessWarnings(root string) []string {
	checker := agentpreflight.Checker{}
	var issues []agentpreflight.Issue
	issues = append(issues, checker.CheckRequiredTools(root)...)
	issues = append(issues, checker.CheckCoordinatorAgent(root)...)
	meta := frontmatter.Meta{Agent: fleet.DefaultWorkerAgent}
	if cfg, err := fleet.LoadWorkersConfig(root); err == nil && cfg.Default != "" {
		meta.Agent = cfg.Default
	}
	issues = append(issues, checker.CheckWorkerAgent(meta, root)...)
	if meta.Agent == "codex" {
		if st, err := codextrust.Inspect(root); err != nil {
			issues = append(issues, agentpreflight.Issue{Severity: agentpreflight.SeverityWarn, Code: "codex-trust-read-failed", Tool: "codex", Message: err.Error()})
		} else if !st.Trusted {
			issues = append(issues, agentpreflight.Issue{Severity: agentpreflight.SeverityWarn, Code: "codex-project-untrusted", Tool: "codex", Message: "trust Codex root project " + st.Root + " before starting Codex tasks"})
		}
	}
	issues = append(issues, checker.CheckSetupToolHints()...)
	return agentpreflight.WarningLines(dedupeIssues(issues))
}

func dedupeIssues(issues []agentpreflight.Issue) []agentpreflight.Issue {
	seen := map[string]bool{}
	var out []agentpreflight.Issue
	for _, issue := range issues {
		key := string(issue.Severity) + "\x00" + issue.Code + "\x00" + issue.Tool + "\x00" + issue.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, issue)
	}
	return out
}
