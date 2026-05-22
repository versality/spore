// Package auditversions audits the host's installed tools and flake
// inputs against the pinned versions declared in a project's
// versions.json. It is the Go body shared by `spore audit-versions`
// and the nix-config `audit-versions` shim that wraps it.
//
// Ported 1:1 from nix-config's nix/packages/audit-versions/main.go;
// the only structural change is the split between configFromEnv (CLI
// concern, lives in the cmd wrapper) and Run (library entry point).
package auditversions

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Config matches the env-derived shape from the nix-config package.
// VersionsJSON / LockJSON are optional raw JSON byte slices; when
// empty, Run falls back to `nix eval .#lib.versions` and reading
// `<Root>/flake.lock` respectively.
type Config struct {
	Root         string
	BinRoot      string
	Host         string
	Strict       bool
	CheckDev     bool
	VersionsJSON []byte
	LockJSON     []byte
}

type tool struct {
	Bin      string `json:"bin"`
	Expected string `json:"expected"`
}

type inputVersion struct {
	Rev string `json:"rev"`
}

type versionsFile struct {
	Inputs map[string]inputVersion `json:"inputs"`
	Audit  struct {
		Tools struct {
			Host     map[string]map[string]tool `json:"host"`
			DevShell map[string]tool            `json:"devShell"`
		} `json:"tools"`
	} `json:"audit"`
}

type lockFile struct {
	Nodes map[string]struct {
		Locked struct {
			Rev string `json:"rev"`
		} `json:"locked"`
	} `json:"nodes"`
}

type counts struct {
	checked      int
	missing      int
	failed       int
	drift        int
	inputChecked int
	inputDrift   int
	devChecked   int
	devMissing   int
	devFailed    int
	devDrift     int
}

var versionPattern = regexp.MustCompile(`[0-9]+([.][0-9]+){1,3}([+._-][A-Za-z0-9][A-Za-z0-9.+_-]*)?`)

// Run audits inputs and tools per cfg, writing one line per finding
// plus a summary line to stdout. Returns the process exit code (1 in
// strict mode when any drift / failure showed up, 0 otherwise) plus
// a non-nil err on hard load failures.
func Run(cfg Config, stdout io.Writer) (int, error) {
	versions, err := loadVersions(cfg)
	if err != nil {
		return 1, err
	}
	lock, err := loadLock(cfg)
	if err != nil {
		return 1, err
	}

	c := counts{}
	auditInputs(stdout, versions, lock, &c)
	auditHostTools(stdout, cfg, versions.Audit.Tools.Host[cfg.Host], &c)
	if cfg.CheckDev {
		auditDevShellTools(stdout, versions.Audit.Tools.DevShell, &c)
	} else {
		fmt.Fprintf(stdout, "[audit-versions] devshell-skip reason=not-in-dev-shell\n")
	}

	fmt.Fprintf(
		stdout,
		"[audit-versions] summary checked=%d drift=%d missing=%d command_failed=%d input_checked=%d input_drift=%d dev_checked=%d dev_drift=%d dev_missing=%d dev_command_failed=%d\n",
		c.checked,
		c.drift,
		c.missing,
		c.failed,
		c.inputChecked,
		c.inputDrift,
		c.devChecked,
		c.devDrift,
		c.devMissing,
		c.devFailed,
	)
	if cfg.Strict && (c.drift > 0 || c.failed > 0 || c.inputDrift > 0 || c.devDrift > 0 || c.devFailed > 0) {
		return 1, nil
	}
	return 0, nil
}

func loadVersions(cfg Config) (versionsFile, error) {
	var raw []byte
	var err error
	if len(cfg.VersionsJSON) > 0 {
		raw = cfg.VersionsJSON
	} else {
		cmd := exec.Command("nix", "eval", "--json", ".#lib.versions")
		cmd.Dir = cfg.Root
		raw, err = cmd.Output()
		if err != nil {
			return versionsFile{}, fmt.Errorf("nix eval .#lib.versions: %w", err)
		}
	}
	var versions versionsFile
	if err := json.Unmarshal(raw, &versions); err != nil {
		return versionsFile{}, fmt.Errorf("parse versions json: %w", err)
	}
	return versions, nil
}

func loadLock(cfg Config) (lockFile, error) {
	raw := cfg.LockJSON
	if len(raw) == 0 {
		var err error
		raw, err = os.ReadFile(filepath.Join(cfg.Root, "flake.lock"))
		if err != nil {
			return lockFile{}, fmt.Errorf("read flake.lock: %w", err)
		}
	}
	var lock lockFile
	if err := json.Unmarshal(raw, &lock); err != nil {
		return lockFile{}, fmt.Errorf("parse flake.lock: %w", err)
	}
	return lock, nil
}

func auditInputs(out io.Writer, versions versionsFile, lock lockFile, c *counts) {
	for _, name := range sortedInputNames(versions.Inputs) {
		expected := versions.Inputs[name].Rev
		actual := lock.Nodes[name].Locked.Rev
		if actual == expected {
			c.inputChecked++
			fmt.Fprintf(out, "[audit-versions] input-ok name=%s expected=%s actual=%s\n", name, expected, actual)
			continue
		}
		c.inputDrift++
		if actual == "" {
			actual = "missing"
		}
		fmt.Fprintf(out, "[audit-versions] input-drift name=%s expected=%s actual=%s\n", name, expected, actual)
	}
}

func auditHostTools(out io.Writer, cfg Config, tools map[string]tool, c *counts) {
	if len(tools) == 0 {
		fmt.Fprintf(out, "[audit-versions] host-skip host=%s reason=no-host-tools\n", cfg.Host)
		return
	}
	for _, name := range sortedToolNames(tools) {
		t := tools[name]
		path := filepath.Join(cfg.BinRoot, t.Bin)
		result := checkExecutable(path, t.Expected)
		printToolResult(out, "", name, path, t.Expected, result)
		addToolCount(c, result)
	}
}

func auditDevShellTools(out io.Writer, tools map[string]tool, c *counts) {
	for _, name := range sortedToolNames(tools) {
		t := tools[name]
		path, err := exec.LookPath(t.Bin)
		if err != nil {
			c.devMissing++
			fmt.Fprintf(out, "[audit-versions] devshell-missing name=%s bin=%s expected=%s\n", name, t.Bin, t.Expected)
			continue
		}
		result := checkExecutable(path, t.Expected)
		printToolResult(out, "devshell-", name, path, t.Expected, result)
		switch result.kind {
		case "ok", "store-ok":
			c.devChecked++
		case "missing":
			c.devMissing++
		case "command-failed":
			c.devFailed++
		case "drift":
			c.devDrift++
		}
	}
}

type toolResult struct {
	kind     string
	actual   string
	output   string
	resolved string
}

func checkExecutable(path, expected string) toolResult {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return toolResult{kind: "missing"}
	}
	output, err := exec.Command(path, "--version").CombinedOutput()
	resolved, _ := filepath.EvalSymlinks(path)
	if err != nil {
		if storePathMatches(resolved, expected) {
			return toolResult{kind: "store-ok", resolved: resolved}
		}
		return toolResult{kind: "command-failed", output: strings.TrimSpace(string(output))}
	}
	text := string(output)
	actual := extractVersion(text)
	if actual == expected || strings.Contains(text, expected) {
		return toolResult{kind: "ok", actual: actual}
	}
	return toolResult{kind: "drift", actual: actual, output: strings.TrimSpace(text)}
}

func storePathMatches(path, expected string) bool {
	return strings.Contains(path, "-"+expected+"/") || strings.Contains(path, "-"+expected+"-")
}

func extractVersion(output string) string {
	match := versionPattern.FindString(output)
	if match == "" {
		return "unknown"
	}
	return match
}

func printToolResult(out io.Writer, prefix, name, path, expected string, result toolResult) {
	switch result.kind {
	case "ok":
		fmt.Fprintf(out, "[audit-versions] %sok name=%s bin=%s expected=%s actual=%s\n", prefix, name, path, expected, result.actual)
	case "store-ok":
		fmt.Fprintf(out, "[audit-versions] %sstore-ok name=%s bin=%s expected=%s resolved=%s\n", prefix, name, path, expected, result.resolved)
	case "missing":
		fmt.Fprintf(out, "[audit-versions] %smissing name=%s bin=%s expected=%s\n", prefix, name, path, expected)
	case "command-failed":
		fmt.Fprintf(out, "[audit-versions] %scommand-failed name=%s bin=%s expected=%s output=%q\n", prefix, name, path, expected, result.output)
	case "drift":
		fmt.Fprintf(out, "[audit-versions] %sdrift name=%s bin=%s expected=%s actual=%s output=%q\n", prefix, name, path, expected, result.actual, result.output)
	}
}

func addToolCount(c *counts, result toolResult) {
	switch result.kind {
	case "ok", "store-ok":
		c.checked++
	case "missing":
		c.missing++
	case "command-failed":
		c.failed++
	case "drift":
		c.drift++
	}
}

func sortedInputNames(inputs map[string]inputVersion) []string {
	names := make([]string, 0, len(inputs))
	for name, input := range inputs {
		if input.Rev != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func sortedToolNames(tools map[string]tool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
