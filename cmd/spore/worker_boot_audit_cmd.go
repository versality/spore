package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/versality/spore/internal/worker/bootaudit"
)

func runWorkerBootAudit(args []string) int {
	fs := flag.NewFlagSet("worker boot-audit", flag.ContinueOnError)
	session := fs.String("session", "", "path to session.jsonl (required)")
	bootTurns := fs.Int("boot-turns", bootaudit.DefaultBootTurns, "how many assistant turns count as 'boot' for per-turn metrics")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore worker boot-audit:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore worker boot-audit - cold-boot quality profile from a session.jsonl")
		fmt.Println("  --session PATH       path to ~/.claude/projects/<project>/<session>.jsonl (required)")
		fmt.Println("  --boot-turns N       turn window for per-turn metrics (default 5)")
		fmt.Println("Exit:")
		fmt.Println("  0  clean")
		fmt.Println("  1  read error")
		fmt.Println("  2  findings (e.g. boot-time tool errors)")
		return 0
	}
	if *session == "" {
		fmt.Fprintln(os.Stderr, "spore worker boot-audit: --session is required")
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore worker boot-audit: unexpected positional args")
		return 2
	}

	prof, err := bootaudit.Audit(bootaudit.Config{SessionPath: *session, BootTurns: *bootTurns})
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore worker boot-audit: %v\n", err)
		return 1
	}
	bootaudit.Format(prof, os.Stdout)
	if len(prof.Findings) > 0 {
		for _, f := range prof.Findings {
			fmt.Fprintln(os.Stderr, "finding:", f)
		}
		return 2
	}
	return 0
}
