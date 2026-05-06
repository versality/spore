package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/lints"
)

const claudeUsage = `spore claude - CLAUDE.md helpers

Usage:
  spore claude <subcommand> [flags]

Subcommands:
  lint-distribute    Scan a composer body for sections concentrated under one
                     top-level subdir (homePath candidates).
  apply-distribute   Mutate the composer body to insert <!-- homePath -->
                     markers, then optionally re-render + check + commit.
  lint-subdir        Scan rendered CLAUDE.md files for sections whose
                     dominant scope already owns a different CLAUDE.md.
`

const claudeLintDistributeUsage = `spore claude lint-distribute - scan composer body for homePath candidates

Usage:
  spore claude lint-distribute --source <path> [--root <dir>] [--min-paths N]
                               [--floor-percent N] [--exclude SEG,SEG]

Flags:
  --source         Path (repo-relative) to the composer body to scan (required).
  --root           Repo root. Defaults to the current directory.
  --min-paths      Minimum distinct path tokens per section. Default 3.
  --floor-percent  Subdir must capture this share to flag. Default 60.
  --exclude        Comma-separated top-level segments to ignore (e.g. docs,templates).

Exit 0 when no candidates, 1 when candidates are reported, 2 on usage error.
`

const claudeApplyDistributeUsage = `spore claude apply-distribute - tag homePath candidates and (optionally) render+check+commit

Usage:
  spore claude apply-distribute --source <path> [--root <dir>] [--min-paths N]
                                [--floor-percent N] [--exclude SEG,SEG]
                                [--render-cmd '<sh>'] [--check-cmd '<sh>']
                                [--ledger <path>] [--dry-run]

Flags:
  --source         Path (repo-relative) to the composer body to mutate (required).
  --root           Repo root. Defaults to the current directory.
  --render-cmd     Shell command to re-render after marker insertion. Skipped if empty.
  --check-cmd      Shell command to verify the rendered tree. Skipped if empty.
  --ledger         JSONL path to append a pending row when --check-cmd fails.
                   Default: $XDG_STATE_HOME/spore/claude-distribute-pending.jsonl.
  --dry-run        Insert markers + render, but skip check + commit.
  --min-paths      Minimum distinct path tokens per section. Default 3.
  --floor-percent  Subdir must capture this share to flag. Default 60.
  --exclude        Comma-separated top-level segments to ignore.

When --check-cmd is set: on green, the source file plus any other modified or
new files are committed. On red, the working tree is left dirty and a row
is appended to --ledger so an operator (or self-care loop) can pick it up.

Exit 0 on a green run (or a clean no-candidates pass), 1 on a red check
or other failure, 2 on usage error.
`

const claudeLintSubdirUsage = `spore claude lint-subdir - scan rendered CLAUDE.md for misplaced sections

Usage:
  spore claude lint-subdir [--root <dir>] [--min-paths N] [--floor-percent N]

Flags:
  --root           Repo root. Defaults to the current directory.
  --min-paths      Minimum distinct path tokens per section. Default 3.
  --floor-percent  Subdir must capture this share to flag. Default 60.

Opt out by placing '<!-- lint: scope-ok -->' inside a section body.
Exit 0 when clean, 1 when issues are reported, 2 on usage error.
`

func runClaude(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, claudeUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(claudeUsage)
		return 0
	case "lint-distribute":
		return runClaudeLintDistribute(rest)
	case "apply-distribute":
		return runClaudeApplyDistribute(rest)
	case "lint-subdir":
		return runClaudeLintSubdir(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore claude: unknown subcommand %q\n\n%s", sub, claudeUsage)
		return 2
	}
}

type distributeFlags struct {
	root      string
	source    string
	minPaths  int
	floorPct  int
	excludes  string
	renderCmd string
	checkCmd  string
	ledger    string
	dryRun    bool
}

func registerCommonDistributeFlags(fs *flag.FlagSet, f *distributeFlags) {
	fs.StringVar(&f.root, "root", ".", "repo root")
	fs.StringVar(&f.source, "source", "", "composer body path (repo-relative)")
	fs.IntVar(&f.minPaths, "min-paths", 0, "minimum distinct path tokens per section")
	fs.IntVar(&f.floorPct, "floor-percent", 0, "share threshold to flag a section")
	fs.StringVar(&f.excludes, "exclude", "", "comma-separated top-level segments to ignore")
}

func (f *distributeFlags) lint() lints.ClaudeDistribute {
	var ex []string
	for _, s := range strings.Split(f.excludes, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			ex = append(ex, s)
		}
	}
	return lints.ClaudeDistribute{
		Source:       f.source,
		MinPaths:     f.minPaths,
		FloorPercent: f.floorPct,
		Excludes:     ex,
	}
}

func runClaudeLintDistribute(args []string) int {
	fs := flag.NewFlagSet("claude lint-distribute", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var f distributeFlags
	registerCommonDistributeFlags(fs, &f)
	help := fs.Bool("h", false, "")
	helpLong := fs.Bool("help", false, "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore claude lint-distribute:", err)
		fmt.Fprint(os.Stderr, claudeLintDistributeUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(claudeLintDistributeUsage)
		return 0
	}
	if f.source == "" {
		fmt.Fprintln(os.Stderr, "spore claude lint-distribute: --source is required")
		fmt.Fprint(os.Stderr, claudeLintDistributeUsage)
		return 2
	}
	cands, err := f.lint().Scan(f.root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore claude lint-distribute:", err)
		return 1
	}
	if len(cands) == 0 {
		fmt.Println("[claude-distribute] no candidates")
		return 0
	}
	for _, c := range cands {
		fmt.Printf("%s:%d section %q -> homePath: %s (%d/%d, %d%%)\n",
			f.source, c.Line, c.Name, c.Subdir, c.Count, c.Total, c.Percent)
	}
	fmt.Println()
	fmt.Println("Run 'spore claude apply-distribute --source <path>' to auto-tag,")
	fmt.Println("or add '<!-- lint: scope-ok -->' inside a section to keep it cross-cutting.")
	return 1
}

func runClaudeApplyDistribute(args []string) int {
	fs := flag.NewFlagSet("claude apply-distribute", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var f distributeFlags
	registerCommonDistributeFlags(fs, &f)
	fs.StringVar(&f.renderCmd, "render-cmd", "", "shell command to re-render after marker insertion")
	fs.StringVar(&f.checkCmd, "check-cmd", "", "shell command to verify after render")
	fs.StringVar(&f.ledger, "ledger", "", "JSONL path for pending rows on red check")
	fs.BoolVar(&f.dryRun, "dry-run", false, "stop after render, before check + commit")
	help := fs.Bool("h", false, "")
	helpLong := fs.Bool("help", false, "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore claude apply-distribute:", err)
		fmt.Fprint(os.Stderr, claudeApplyDistributeUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(claudeApplyDistributeUsage)
		return 0
	}
	if f.source == "" {
		fmt.Fprintln(os.Stderr, "spore claude apply-distribute: --source is required")
		fmt.Fprint(os.Stderr, claudeApplyDistributeUsage)
		return 2
	}

	lint := f.lint()
	cands, err := lint.Scan(f.root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore claude apply-distribute:", err)
		return 1
	}
	if len(cands) == 0 {
		fmt.Println("[claude-distribute] no candidates")
		return 0
	}
	for _, c := range cands {
		fmt.Printf("[apply] %s:%d %q -> homePath: %s\n", f.source, c.Line, c.Name, c.Subdir)
	}
	if err := lint.ApplyMarkers(f.root, cands); err != nil {
		fmt.Fprintln(os.Stderr, "spore claude apply-distribute:", err)
		return 1
	}

	if f.renderCmd != "" {
		if err := runShell(f.root, f.renderCmd, nil, nil); err != nil {
			fmt.Fprintln(os.Stderr, "spore claude apply-distribute: render-cmd:", err)
			return 1
		}
	}
	if f.dryRun {
		fmt.Println("[claude-distribute] --dry-run: stopped before check")
		return 0
	}
	if f.checkCmd == "" {
		return 0
	}

	var checkLog strings.Builder
	if err := runShell(f.root, f.checkCmd, &checkLog, &checkLog); err == nil {
		if cerr := commitChanges(f.root, f.source, cands); cerr != nil {
			fmt.Fprintln(os.Stderr, "spore claude apply-distribute: commit:", cerr)
			return 1
		}
		return 0
	}

	ledger := f.ledger
	if ledger == "" {
		ledger = defaultDistributeLedger()
	}
	if err := writeDistributeLedger(ledger, f.root, cands, checkLog.String()); err != nil {
		fmt.Fprintln(os.Stderr, "spore claude apply-distribute: ledger:", err)
	}
	fmt.Println("[claude-distribute] check failed; tree dirty, ledger updated:")
	fmt.Println("  ", ledger)
	return 1
}

func runClaudeLintSubdir(args []string) int {
	fs := flag.NewFlagSet("claude lint-subdir", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", ".", "repo root")
	minPaths := fs.Int("min-paths", 0, "minimum distinct path tokens per section")
	floorPct := fs.Int("floor-percent", 0, "share threshold to flag a section")
	help := fs.Bool("h", false, "")
	helpLong := fs.Bool("help", false, "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore claude lint-subdir:", err)
		fmt.Fprint(os.Stderr, claudeLintSubdirUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(claudeLintSubdirUsage)
		return 0
	}
	issues, err := lints.ClaudeSubdir{
		MinPaths:     *minPaths,
		FloorPercent: *floorPct,
	}.Run(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore claude lint-subdir:", err)
		return 1
	}
	if len(issues) == 0 {
		return 0
	}
	for _, i := range issues {
		fmt.Println(i.String())
	}
	fmt.Println()
	fmt.Println("Move the section to the suggested subdir CLAUDE.md, or add")
	fmt.Println("'<!-- lint: scope-ok -->' inside the section body if intentional.")
	return 1
}

func runShell(dir, command string, stdout, stderr io.Writer) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	if stdout != nil {
		cmd.Stdout = stdout
	} else {
		cmd.Stdout = os.Stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func commitChanges(root, source string, cands []lints.Candidate) error {
	statusOut, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if len(strings.TrimSpace(string(statusOut))) == 0 {
		return nil
	}
	files := []string{source}
	for _, line := range strings.Split(strings.TrimRight(string(statusOut), "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if path == "" || path == source {
			continue
		}
		files = append(files, path)
	}
	addArgs := append([]string{"-C", root, "add", "--"}, files...)
	if out, err := exec.Command("git", addArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	body := claudeDistributeCommitBody(source, cands)
	commitArgs := []string{"-C", root, "commit", "-m", "claude: auto-distribute subdir-owned sections", "-m", body}
	if out, err := exec.Command("git", commitArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	logOut, err := exec.Command("git", "-C", root, "log", "-1", "--format=%h %s").Output()
	if err == nil {
		fmt.Printf("[claude-distribute] committed: %s", string(logOut))
	}
	return nil
}

func claudeDistributeCommitBody(source string, cands []lints.Candidate) string {
	var b strings.Builder
	for _, c := range cands {
		fmt.Fprintf(&b, "%s:%d section %q -> homePath: %s (%d/%d, %d%%)\n",
			source, c.Line, c.Name, c.Subdir, c.Count, c.Total, c.Percent)
	}
	return b.String()
}

func defaultDistributeLedger() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(os.Getenv("HOME"), ".local/state")
	}
	return filepath.Join(state, "spore", "claude-distribute-pending.jsonl")
}

func writeDistributeLedger(path, root string, cands []lints.Candidate, checkLog string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sha := strings.TrimSpace(commandOutput(root, "git", "rev-parse", "HEAD"))
	ts := time.Now().UTC().Format(time.RFC3339)

	var b strings.Builder
	b.WriteString(`{"ts":"`)
	b.WriteString(ts)
	b.WriteString(`","sha":"`)
	b.WriteString(jsonEscape(sha))
	b.WriteString(`","candidates":[`)
	for i, c := range cands {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"line":%d,"name":"%s","subdir":"%s","percent":%d}`,
			c.Line, jsonEscape(c.Name), jsonEscape(c.Subdir), c.Percent)
	}
	b.WriteString(`],"check_excerpt":"`)
	b.WriteString(jsonEscape(tailLines(checkLog, 20)))
	b.WriteString("\"}\n")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		return err
	}
	return nil
}

func commandOutput(dir, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " ")
}

func jsonEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}
