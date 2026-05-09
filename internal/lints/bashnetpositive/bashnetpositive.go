// Package bashnetpositive implements the kernel hard gate that refuses
// merges growing harness/ bash. It runs out-of-band from the default
// lint set because it needs a base ref and inspects commit messages,
// not just the working tree.
//
// Verdict logic:
//   - net bash LOC over harness/*.sh > 0 (added minus removed) -> refuse.
//   - new harness/*.sh file not listed under keep-here-glue in
//     docs/harness-inventory.md -> refuse.
//   - any commit body in the diff range carrying an
//     "allow-bash-net-positive: <reason>" trailer flips a refuse to
//     override-applied (exit 0).
//   - missing docs/harness-inventory.md is graceful: net-zero or
//     net-negative passes with a warning, net-positive still refuses.
package bashnetpositive

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Verdict is the outcome of a Run.
type Verdict string

const (
	Pass            Verdict = "pass"
	Refuse          Verdict = "refuse"
	OverrideApplied Verdict = "override-applied"
)

// Result is the structured outcome the CLI prints (text or JSON) and
// the merge gate inspects to decide whether to abort.
type Result struct {
	NetBashLoc      int      `json:"net_bash_loc"`
	NewBashFiles    []string `json:"new_bash_files"`
	AllowedNewFiles []string `json:"allowed_new_files"`
	RefusedNewFiles []string `json:"refused_new_files"`
	Override        string   `json:"override,omitempty"`
	NoInventory     bool     `json:"no_inventory"`
	Verdict         Verdict  `json:"verdict"`
	Reasons         []string `json:"reasons,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

// Run computes the verdict for the diff range baseRef...HEAD against
// repoRoot. baseRef is typically "main".
func Run(repoRoot, baseRef string) (Result, error) {
	if repoRoot == "" {
		repoRoot = "."
	}
	if baseRef == "" {
		baseRef = "main"
	}
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve repo root: %w", err)
	}

	netLoc, err := netBashLoc(abs, baseRef)
	if err != nil {
		return Result{}, err
	}
	newFiles, err := newBashFiles(abs, baseRef)
	if err != nil {
		return Result{}, err
	}
	override, err := overrideReason(abs, baseRef)
	if err != nil {
		return Result{}, err
	}

	allowed, noInventory, err := readGlueList(abs)
	if err != nil {
		return Result{}, err
	}

	res := Result{
		NetBashLoc:  netLoc,
		NoInventory: noInventory,
		Override:    override,
	}

	for _, p := range newFiles {
		if allowed[p] {
			res.AllowedNewFiles = append(res.AllowedNewFiles, p)
			continue
		}
		res.NewBashFiles = append(res.NewBashFiles, p)
		res.RefusedNewFiles = append(res.RefusedNewFiles, p)
	}
	if noInventory {
		// No glue list to enforce against: drop the new-file refusals,
		// fall back to the LOC check alone with a warning.
		res.RefusedNewFiles = nil
		res.Warnings = append(res.Warnings,
			"docs/harness-inventory.md not found; only the net-LOC check applies")
	}

	if netLoc > 0 {
		res.Reasons = append(res.Reasons,
			fmt.Sprintf("net bash LOC in harness/ is +%d (must be <= 0)", netLoc))
	}
	for _, p := range res.RefusedNewFiles {
		res.Reasons = append(res.Reasons,
			fmt.Sprintf("new harness bash file %q not listed under keep-here-glue", p))
	}

	switch {
	case len(res.Reasons) == 0:
		res.Verdict = Pass
	case override != "":
		res.Verdict = OverrideApplied
	default:
		res.Verdict = Refuse
	}
	return res, nil
}

// netBashLoc returns sum(added - removed) across harness/**.sh in the
// diff range. Binary entries (added/removed reported as "-") are
// skipped: a tracked .sh file should not be binary, and we don't want
// to refuse on a renamed image.
func netBashLoc(root, baseRef string) (int, error) {
	out, err := runGit(root, "diff", "--numstat", baseRef+"...HEAD", "--", "harness/")
	if err != nil {
		return 0, err
	}
	net := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		added, removed, path := fields[0], fields[1], fields[2]
		if !isShPath(path) {
			continue
		}
		if added == "-" || removed == "-" {
			continue
		}
		a, err := strconv.Atoi(added)
		if err != nil {
			return 0, fmt.Errorf("parse numstat added %q: %w", added, err)
		}
		r, err := strconv.Atoi(removed)
		if err != nil {
			return 0, fmt.Errorf("parse numstat removed %q: %w", removed, err)
		}
		net += a - r
	}
	return net, nil
}

// newBashFiles lists harness/**.sh paths added (status A) in the diff
// range. Sorted for deterministic output.
func newBashFiles(root, baseRef string) ([]string, error) {
	out, err := runGit(root, "diff", "--name-status", "--diff-filter=A",
		baseRef+"...HEAD", "--", "harness/")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		path := fields[1]
		if isShPath(path) {
			files = append(files, path)
		}
	}
	return files, nil
}

// overrideReason returns the first allow-bash-net-positive: trailer
// found in any commit body in the diff range, or "" if none.
func overrideReason(root, baseRef string) (string, error) {
	out, err := runGit(root, "log", "--format=%B%x00", baseRef+"..HEAD")
	if err != nil {
		return "", err
	}
	for _, body := range strings.Split(out, "\x00") {
		scanner := bufio.NewScanner(strings.NewReader(body))
		for scanner.Scan() {
			line := scanner.Text()
			rest, ok := matchOverride(line)
			if ok {
				return rest, nil
			}
		}
	}
	return "", nil
}

// matchOverride matches a body line shaped like
// "allow-bash-net-positive: <reason>" and returns the reason. The
// match is case-insensitive on the key, anchored at start of line, and
// requires non-empty whitespace-trimmed reason text.
func matchOverride(line string) (string, bool) {
	const key = "allow-bash-net-positive:"
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) < len(key) {
		return "", false
	}
	if !strings.EqualFold(trimmed[:len(key)], key) {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[len(key):])
	if rest == "" {
		return "", false
	}
	return rest, true
}

// readGlueList parses docs/harness-inventory.md and returns the set of
// harness paths under the keep-here-glue section. The section header
// is matched as "keep-here-glue" anywhere in a markdown heading
// (`#`-prefixed) line, case-insensitive. Path lines are accepted in
// three shapes:
//
//   - bare path:     harness/foo.sh
//   - bullet:        - harness/foo.sh
//   - inline-code:   `harness/foo.sh`
//
// Lines after a non-keep-here-glue heading end the section.
//
// If the file is missing, noInventory is true and allowed is empty.
func readGlueList(root string) (allowed map[string]bool, noInventory bool, err error) {
	path := filepath.Join(root, "docs", "harness-inventory.md")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, true, nil
		}
		return nil, false, fmt.Errorf("read harness inventory: %w", err)
	}
	defer f.Close()

	allowed = map[string]bool{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	inSection := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			inSection = strings.Contains(heading, "keep-here-glue")
			continue
		}
		if !inSection {
			continue
		}
		if p := extractPath(trimmed); p != "" {
			allowed[p] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("scan harness inventory: %w", err)
	}
	return allowed, false, nil
}

// extractPath pulls a harness/<path>.sh token out of a glue-section
// line. Returns "" when no path is found.
func extractPath(line string) string {
	if line == "" {
		return ""
	}
	// strip common bullet prefixes
	for _, p := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(line, p) {
			line = line[len(p):]
			break
		}
	}
	// take first whitespace-delimited token
	tok := line
	if i := strings.IndexAny(tok, " \t"); i >= 0 {
		tok = tok[:i]
	}
	tok = strings.Trim(tok, "`'\"")
	if strings.HasPrefix(tok, "harness/") && strings.HasSuffix(tok, ".sh") {
		return tok
	}
	return ""
}

func isShPath(p string) bool {
	return strings.HasPrefix(p, "harness/") && strings.HasSuffix(p, ".sh")
}

// runGit runs git -C root <args...> with safe.directory set so that a
// repo whose ownership trips git's "dubious ownership" guard still
// works (mirroring listFiles in the parent lints package).
func runGit(root string, args ...string) (string, error) {
	full := append([]string{"-c", "safe.directory=" + root, "-C", root}, args...)
	cmd := exec.Command("git", full...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err,
			strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}
