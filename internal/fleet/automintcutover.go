// Auto-mint cutover drafts: when a spore lint has shipped and the
// consumer's bash-migration allowlist still names a bash file that
// the lint replaces, mint a `consume-spore-lint-<name>` task in the
// consumer so the cutover happens without operator prompting.
//
// Companion to the consumer-side auto-mint-bash-mig daemon: that
// minter spawns migrate-*-port tasks against spore BEFORE the lint
// exists; this one spawns consume-spore-lint-* tasks against the
// consumer AFTER the lint has shipped. Different scheduler-key
// namespaces (cutover-automint: vs bash-mig:) so the two daemons
// cannot collide.
//
// The floor gate (active-live < WT_FLEET_FLOOR) is deliberately NOT
// enforced here. The existing reconciler decides whether to promote a
// fresh draft; gating mint on floor would re-introduce the failure
// mode where the consumer's coordinator leaves the fleet at zero with
// drafts un-minted. Per-tick volume is bounded by MaxMints.
package fleet

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/lints"
)

// AutoMintCutoverConfig is the inputs for one tick of the auto-mint
// cutover pass. Zero values for the test seams resolve to production
// implementations.
type AutoMintCutoverConfig struct {
	// Repo is the consumer repo root (the host that holds the
	// allowlist and the tasks/ directory). Defaults to cwd.
	Repo string

	// AllowlistPath defaults to <Repo>/harness/bash-migration-allowlist.txt.
	AllowlistPath string

	// TasksDir defaults to <Repo>/tasks.
	TasksDir string

	// TargetProject is the wt project name minted tasks land under.
	// Defaults to "nix-config".
	TargetProject string

	// MaxMints caps fresh mints in one tick. Defaults to 3 (parity with
	// auto-mint-bash-mig).
	MaxMints int

	// WTBin is the wt binary path. Defaults to "wt".
	WTBin string

	// DryRun skips the actual mint call and only logs the intent.
	DryRun bool

	// Test seams.
	ShippedLints         func() map[string]bool
	ReadAllowlist        func(path string) ([]string, error)
	ScanMinted           func(tasksDir string) (map[string]bool, error)
	ScanExistingCutovers func(tasksDir string) (map[string]bool, error)
	Mint                 func(spec MintSpec, stdout, stderr io.Writer) error

	Stdout, Stderr io.Writer
}

// MintSpec is the payload handed to the Mint hook.
type MintSpec struct {
	LintName     string
	BashFile     string
	Title        string
	Body         string
	SchedulerKey string
	WTBin        string
	Project      string
}

// SkipReason records why a row was skipped this tick.
type SkipReason struct {
	BashFile string
	LintName string
	Reason   string
}

// AutoMintCutoverResult is the per-tick summary returned to the CLI.
type AutoMintCutoverResult struct {
	Minted        []string
	Skipped       []SkipReason
	Errors        []error
	AllowlistRows int
}

const cutoverSchedulerKeyPrefix = "cutover-automint:"

// AutoMintCutover runs one tick. Returns nil error for normal
// operation; a non-nil error means the pass could not start
// (allowlist unreadable, tasks dir unreadable). Per-row mint failures
// are recorded on res.Errors and do not abort the pass.
func AutoMintCutover(cfg AutoMintCutoverConfig) (AutoMintCutoverResult, error) {
	var res AutoMintCutoverResult

	if cfg.Repo == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return res, fmt.Errorf("getwd: %w", err)
		}
		cfg.Repo = cwd
	}
	if cfg.AllowlistPath == "" {
		cfg.AllowlistPath = filepath.Join(cfg.Repo, "harness", "bash-migration-allowlist.txt")
	}
	if cfg.TasksDir == "" {
		cfg.TasksDir = filepath.Join(cfg.Repo, "tasks")
	}
	if cfg.TargetProject == "" {
		cfg.TargetProject = "nix-config"
	}
	if cfg.MaxMints <= 0 {
		cfg.MaxMints = 3
	}
	if cfg.WTBin == "" {
		cfg.WTBin = "wt"
	}
	if cfg.Stdout == nil {
		cfg.Stdout = io.Discard
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}
	if cfg.ShippedLints == nil {
		cfg.ShippedLints = defaultShippedLints
	}
	if cfg.ReadAllowlist == nil {
		cfg.ReadAllowlist = readAllowlist
	}
	if cfg.ScanMinted == nil {
		cfg.ScanMinted = scanCutoverMintedKeys
	}
	if cfg.ScanExistingCutovers == nil {
		cfg.ScanExistingCutovers = scanExistingCutoverFiles
	}
	if cfg.Mint == nil {
		cfg.Mint = realMint
	}

	rows, err := cfg.ReadAllowlist(cfg.AllowlistPath)
	if err != nil {
		return res, fmt.Errorf("read allowlist %s: %w", cfg.AllowlistPath, err)
	}
	res.AllowlistRows = len(rows)

	minted, err := cfg.ScanMinted(cfg.TasksDir)
	if err != nil {
		return res, fmt.Errorf("scan minted tasks under %s: %w", cfg.TasksDir, err)
	}
	existing, err := cfg.ScanExistingCutovers(cfg.TasksDir)
	if err != nil {
		return res, fmt.Errorf("scan existing cutover tasks under %s: %w", cfg.TasksDir, err)
	}

	shipped := cfg.ShippedLints()
	mintedThisTick := map[string]bool{}

	for _, row := range rows {
		if len(res.Minted) >= cfg.MaxMints {
			res.Skipped = append(res.Skipped, SkipReason{BashFile: row, Reason: "max-mints-cap"})
			continue
		}
		lint := deriveLintName(row)
		if lint == "" {
			res.Skipped = append(res.Skipped, SkipReason{BashFile: row, Reason: "unparseable-basename"})
			continue
		}
		if !shipped[lint] {
			res.Skipped = append(res.Skipped, SkipReason{BashFile: row, LintName: lint, Reason: "lint-not-shipped"})
			continue
		}
		key := cutoverSchedulerKeyPrefix + lint
		if minted[key] {
			res.Skipped = append(res.Skipped, SkipReason{BashFile: row, LintName: lint, Reason: "already-minted"})
			continue
		}
		if existing[lint] {
			res.Skipped = append(res.Skipped, SkipReason{BashFile: row, LintName: lint, Reason: "existing-cutover-task"})
			continue
		}
		if mintedThisTick[lint] {
			res.Skipped = append(res.Skipped, SkipReason{BashFile: row, LintName: lint, Reason: "dup-in-tick"})
			continue
		}

		title, body := briefForCutover(lint, row)
		spec := MintSpec{
			LintName:     lint,
			BashFile:     row,
			Title:        title,
			Body:         body,
			SchedulerKey: key,
			WTBin:        cfg.WTBin,
			Project:      cfg.TargetProject,
		}
		if cfg.DryRun {
			fmt.Fprintf(cfg.Stdout, "[auto-mint-cutover] DRY mint: %s (key=%s)\n", title, key)
			res.Minted = append(res.Minted, key)
			mintedThisTick[lint] = true
			continue
		}
		if err := cfg.Mint(spec, cfg.Stdout, cfg.Stderr); err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("mint %s: %w", lint, err))
			fmt.Fprintf(cfg.Stderr, "[auto-mint-cutover] mint failed for %s: %v\n", lint, err)
			continue
		}
		res.Minted = append(res.Minted, key)
		mintedThisTick[lint] = true
	}

	fmt.Fprintf(cfg.Stdout, "[auto-mint-cutover] minted=%d skipped=%d errors=%d (cap=%d, allowlist=%d)\n",
		len(res.Minted), len(res.Skipped), len(res.Errors), cfg.MaxMints, res.AllowlistRows)
	return res, nil
}

// defaultShippedLints returns the set of lint names spore knows
// about. Used to gate cutover mints: only mint when the lint
// the bash file replaces has actually shipped.
func defaultShippedLints() map[string]bool {
	out := map[string]bool{}
	for name := range lints.Named() {
		out[name] = true
	}
	return out
}

// deriveLintName extracts the lint name from an allowlist row.
// Recognised basename shapes (in order):
//
//	lint-<name>.sh    -> <name>
//	check-<name>.sh   -> <name>
//	block-<name>.sh   -> <name>
//
// Returns "" when no prefix matches. Directory-form (e.g. lint-X/)
// is captured by stripping any trailing slash before basename.
func deriveLintName(row string) string {
	row = strings.TrimRight(row, "/")
	base := filepath.Base(row)
	base = strings.TrimSuffix(base, ".sh")
	base = strings.TrimSuffix(base, ".bash")
	for _, pfx := range []string{"lint-", "check-", "block-"} {
		if strings.HasPrefix(base, pfx) {
			return strings.TrimPrefix(base, pfx)
		}
	}
	return ""
}

// readAllowlist parses harness/bash-migration-allowlist.txt: blank
// lines and `#`-comments stripped, leading/trailing whitespace
// trimmed, original order preserved. Matches the bash-mig reader
// verbatim so the two daemons share file semantics.
func readAllowlist(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, scanner.Err()
}

// scanCutoverMintedKeys walks tasks/*.md and returns the set of
// scheduler_key values that carry the cutover-automint: prefix. Done
// tasks count: a re-mint after a successful cutover would re-open
// already-shipped work.
func scanCutoverMintedKeys(tasksDir string) (map[string]bool, error) {
	out := map[string]bool{}
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		key, err := readSchedulerKey(filepath.Join(tasksDir, e.Name()))
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(key, cutoverSchedulerKeyPrefix) {
			out[key] = true
		}
	}
	return out, nil
}

// scanExistingCutoverFiles returns the set of lint names already
// covered by a `tasks/consume-spore-lint-<x>.md` file, regardless of
// status or scheduler_key. Catches manually-minted predecessors that
// predate the cutover-automint scheduler-key convention.
func scanExistingCutoverFiles(tasksDir string) (map[string]bool, error) {
	out := map[string]bool{}
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		base := strings.TrimSuffix(name, ".md")
		const pfx = "consume-spore-lint-"
		if !strings.HasPrefix(base, pfx) {
			continue
		}
		lint := strings.TrimPrefix(base, pfx)
		if lint == "" {
			continue
		}
		out[lint] = true
	}
	return out, nil
}

// readSchedulerKey returns the trimmed value of the `scheduler_key:`
// frontmatter row, or "" if absent. Lifted from auto-mint-bash-mig
// to keep the two daemons reading frontmatter the same way.
func readSchedulerKey(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	inFrontmatter := false
	delimSeen := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			delimSeen++
			if delimSeen == 1 {
				inFrontmatter = true
				continue
			}
			break
		}
		if !inFrontmatter {
			if delimSeen == 0 {
				return "", nil
			}
			continue
		}
		if strings.HasPrefix(line, "scheduler_key:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "scheduler_key:")), nil
		}
	}
	return "", scanner.Err()
}

// briefForCutover returns the title and body for the
// consume-spore-lint-<name> mint. Mirrors the bash-mig brief shape:
// short, settled-design, plan-first-exempt.
func briefForCutover(lintName, bashFile string) (string, string) {
	title := "consume-spore-lint-" + lintName + ": drop bash " + bashFile + ", wire spore lint " + lintName
	body := "Cut nix-config over to the shipped spore lint `" + lintName + "`; drop the bash predecessor at `" + bashFile + "`.\n\n" +
		"## Scope (nix-config side only)\n\n" +
		"1. Bump the spore flake input (`just spore-bump --apply`) so `spore lint " + lintName + "` is callable from the host.\n" +
		"2. Wire the lint into the same call site `" + bashFile + "` runs from (justfile recipe, hook, or check shim). Prefer `spore lint " + lintName + "` over `spore scout scan` so failures stay scoped.\n" +
		"3. Delete `" + bashFile + "`. Drop the matching row from `harness/bash-migration-allowlist.txt`.\n" +
		"4. `just check` + `just validate-nix` green; `wt merge` on green.\n\n" +
		"## Prior art\n\n" +
		"- Policy: `nix/harness/claude/rules/no-new-bash.md`.\n" +
		"- Allowlist: `harness/bash-migration-allowlist.txt`.\n" +
		"- Recent consume-spore-lint-* commits: `git log --all --oneline | grep consume-spore-lint` for the shape.\n\n" +
		"## Acceptance\n\n" +
		"- `" + bashFile + "` removed from nix-config.\n" +
		"- Allowlist entry dropped.\n" +
		"- `spore lint " + lintName + "` runs at the cut-over call site.\n" +
		"- `just check` and `just validate-nix` green.\n\n" +
		"Plan-first-exempt (effort: medium, settled-design cutover).\n\n" +
		"Auto-minted by `spore fleet auto-mint-cutover`. Re-mint suppressed via scheduler-key `" + cutoverSchedulerKeyPrefix + lintName + "`.\n"
	return title, body
}

// realMint shells out to wt task new with the cutover payload.
func realMint(spec MintSpec, stdout, stderr io.Writer) error {
	cmd := exec.Command(spec.WTBin, "task", "new",
		"--no-edit",
		"--project", spec.Project,
		"--effort", "medium",
		"--scheduler-key", spec.SchedulerKey,
		"--body", spec.Body,
		spec.Title,
	)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
