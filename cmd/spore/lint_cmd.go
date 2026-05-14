package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/versality/spore/internal/lints"
)

// runLint runs the default lint set or a single named lint over the
// working tree and prints findings. Exits 0 when clean, 1 on any
// issue, 2 on usage error.
func runLint(args []string) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	root := fs.String("root", ".", "repo root to lint")
	list := fs.Bool("list", false, "list every named lint and exit")
	jsonOut := fs.Bool("json", false, "emit findings as JSONL on stdout instead of human text")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	allowlist := stringListFlag{}
	ext := stringListFlag{}
	skipPath := stringListFlag{}
	consumersDir := fs.String("consumers-dir", "", "override claude-drift consumers dir")
	rulesDir := fs.String("rules-dir", "", "override claude-drift rules dir")
	renderCmd := fs.String("render-cmd", "", "override claude-drift renderer command")
	consumersCmd := fs.String("consumers-cmd", "", "claude-drift composer-driven adapter: command emitting JSON [{name,target_path,rendered_text}]")
	limit := fs.Int("limit", 0, "override filesize line limit")
	rootLineLimit := fs.Int("root-line-limit", 0, "override claude-totalsize root line limit")
	rootCharLimit := fs.Int("root-char-limit", 0, "override claude-totalsize root char limit")
	subdirLineLimit := fs.Int("subdir-line-limit", 0, "override claude-totalsize subdir line limit")
	fs.Var(&allowlist, "allowlist", "comma-separated extra emdash allowlist paths")
	fs.Var(&ext, "ext", "comma-separated extension or basename list")
	fs.Var(&skipPath, "skip-path", "comma-separated paths, prefixes, or globs to skip")
	if err := fs.Parse(reorderLintArgs(args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore lint:", err)
		fmt.Fprint(os.Stderr, lintUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(lintUsage)
		return 0
	}
	if *list {
		printLintList(os.Stdout)
		return 0
	}

	var toRun []lints.Lint
	switch fs.NArg() {
	case 0:
		toRun = lints.Default()
	case 1:
		name := fs.Arg(0)
		l, ok := lints.Named()[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "spore lint: unknown lint %q\n\n", name)
			printLintList(os.Stderr)
			return 2
		}
		toRun = []lints.Lint{l}
	default:
		fmt.Fprintln(os.Stderr, "spore lint: expected at most one positional <name>:", fs.Args())
		return 2
	}

	cfg, err := lints.LoadProjectConfig(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore lint:", err)
		return 2
	}
	cfg = lints.MergeConfig(cfg, flagLintConfig(toRun, lintFlagValues{
		allowlist:       allowlist.values,
		consumersDir:    *consumersDir,
		rulesDir:        *rulesDir,
		renderCmd:       *renderCmd,
		consumersCmd:    *consumersCmd,
		limit:           *limit,
		ext:             ext.values,
		skipPath:        skipPath.values,
		rootLineLimit:   *rootLineLimit,
		rootCharLimit:   *rootCharLimit,
		subdirLineLimit: *subdirLineLimit,
	}))
	toRun = lints.ApplyConfig(toRun, cfg)

	bad := false
	taskEvidenceWarnOnly := lints.EvidenceWarnOnly()
	taskPriorityWarnOnly := lints.PriorityWarnOnly()
	var firstErr error
	enc := json.NewEncoder(os.Stdout)
	for _, l := range toRun {
		issues, err := l.Run(*root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "spore lint: %s: %v\n", l.Name(), err)
			if firstErr == nil {
				firstErr = err
			}
			bad = true
			continue
		}
		warnOnly := (l.Name() == "task-evidence" && taskEvidenceWarnOnly) ||
			(l.Name() == "task-priority" && taskPriorityWarnOnly)
		for _, i := range issues {
			if *jsonOut {
				if err := emitJSON(enc, l.Name(), i, warnOnly); err != nil {
					fmt.Fprintln(os.Stderr, "spore lint:", err)
					return 2
				}
				if !warnOnly {
					bad = true
				}
				continue
			}
			line := prefix(l.Name(), i.String())
			if warnOnly {
				fmt.Fprintln(os.Stderr, "warn: "+line)
				continue
			}
			fmt.Fprintln(os.Stdout, line)
			bad = true
		}
	}
	if bad {
		return 1
	}
	return 0
}

// jsonFinding is the stable on-wire schema emitted by `spore lint --json`.
// Field names follow the scout-findings.jsonl format consumed by
// `spore scout mint-healers`; adding fields is forward-compatible, but
// renames require a `FingerprintVersion` bump so stale dedup state
// invalidates.
type jsonFinding struct {
	Ts          string `json:"ts"`
	Lint        string `json:"lint"`
	Severity    string `json:"severity"`
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Message     string `json:"message"`
	Fingerprint string `json:"fingerprint"`
}

func emitJSON(enc *json.Encoder, lintName string, i lints.Issue, warnOnly bool) error {
	return enc.Encode(buildFinding(lintName, i, warnOnly))
}

// buildFinding fills in defaults for Severity (error, or warn for
// warn-only lints) and Fingerprint (computed from the issue tuple) so
// scout and lint share one row schema.
func buildFinding(lintName string, i lints.Issue, warnOnly bool) jsonFinding {
	severity := i.Severity
	if severity == "" {
		if warnOnly {
			severity = "warn"
		} else {
			severity = "error"
		}
	}
	fp := i.Fingerprint
	if fp == "" {
		fp = lints.Fingerprint(lintName, i.Path, i.Line, i.Message)
	}
	return jsonFinding{
		Ts:          time.Now().UTC().Format(time.RFC3339),
		Lint:        lintName,
		Severity:    severity,
		Path:        i.Path,
		Line:        i.Line,
		Message:     i.Message,
		Fingerprint: fp,
	}
}

func printLintList(w *os.File) {
	defaults := map[string]bool{}
	for _, l := range lints.Default() {
		defaults[l.Name()] = true
	}
	names := make([]string, 0, len(lints.Named()))
	for n := range lints.Named() {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintln(w, "available lints (D = in spore lint default set):")
	for _, n := range names {
		marker := " "
		if defaults[n] {
			marker = "D"
		}
		fmt.Fprintf(w, "  [%s] %s\n", marker, n)
	}
}

func prefix(name, msg string) string {
	return "[" + name + "] " + strings.TrimRight(msg, "\n")
}

type stringListFlag struct {
	values []string
}

func (f *stringListFlag) String() string {
	return strings.Join(f.values, ",")
}

func (f *stringListFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			f.values = append(f.values, part)
		}
	}
	return nil
}

type lintFlagValues struct {
	allowlist       []string
	consumersDir    string
	rulesDir        string
	renderCmd       string
	consumersCmd    string
	limit           int
	ext             []string
	skipPath        []string
	rootLineLimit   int
	rootCharLimit   int
	subdirLineLimit int
}

func flagLintConfig(toRun []lints.Lint, v lintFlagValues) lints.Config {
	out := lints.Config{ByName: map[string]lints.LintConfig{}}
	for _, lint := range toRun {
		out.ByName[lint.Name()] = lints.LintConfig{
			Allowlist:       v.allowlist,
			ConsumersDir:    v.consumersDir,
			RulesDir:        v.rulesDir,
			RenderCmd:       v.renderCmd,
			ConsumersCmd:    v.consumersCmd,
			Limit:           v.limit,
			Ext:             v.ext,
			SkipPath:        v.skipPath,
			RootLineLimit:   v.rootLineLimit,
			RootCharLimit:   v.rootCharLimit,
			SubdirLineLimit: v.subdirLineLimit,
		}
	}
	return out
}

func reorderLintArgs(args []string) []string {
	var flags []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			if lintFlagNeedsValue(arg) && !strings.Contains(arg, "=") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, arg)
	}
	return append(flags, positional...)
}

func lintFlagNeedsValue(arg string) bool {
	name := strings.TrimLeft(arg, "-")
	if eq := strings.IndexByte(name, '='); eq >= 0 {
		name = name[:eq]
	}
	switch name {
	case "h", "help", "list", "json":
		return false
	default:
		return true
	}
}
