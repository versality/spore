// spore-rover wraps an LLM coding agent in a bubblewrap (bwrap)
// sandbox and runs it in a tmux window the operator can watch.
//
// Threat model (three points, nothing else):
//
//   - FS write-allowlist. The rover writes only inside its worktree
//     and the small RW set listed in the policy. Operator dotfiles
//     (~/.ssh, ~/.bashrc, sibling worktrees) are inaccessible.
//   - FS read-deny. /etc/shadow, ~/.ssh, /root, etc. unreadable.
//   - Network egress allowlist. (Phase 2; not yet wired.)

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	var (
		worktree   string
		windowName string
		shell      bool
		homeBase   string
		extraRW    multiFlag
		extraRO    multiFlag
		dryRun     bool
	)
	flag.StringVar(&worktree, "worktree", ".", "directory the rover may write to (becomes cwd inside sandbox)")
	flag.StringVar(&windowName, "window", "", "tmux window name (default: rover-<worktree-base>)")
	flag.BoolVar(&shell, "shell", false, "drop into bash instead of launching claude")
	flag.StringVar(&homeBase, "home", os.Getenv("HOME"), "path the sandbox exposes as $HOME (tmpfs-backed; must exist on host)")
	flag.Var(&extraRW, "rw", "additional rw bind (repeatable; host path)")
	flag.Var(&extraRO, "ro", "additional ro bind (repeatable; host path)")
	flag.BoolVar(&dryRun, "dry-run", false, "print the bwrap argv and exit")
	flag.Parse()

	if os.Getenv("TMUX") == "" && !dryRun {
		fatal("spore-rover must run inside a tmux session (TMUX not set)")
	}

	wtAbs, err := filepath.Abs(worktree)
	if err != nil {
		fatal("resolve worktree: %v", err)
	}
	if windowName == "" {
		windowName = "rover-" + filepath.Base(wtAbs)
	}

	rw := append([]string(nil), extraRW...)
	if !shell {
		// claude needs to read its credentials and write session
		// state. Bind the operator's claude config rw. Caveat: this
		// exposes ~/.claude/.credentials.json to the rover. Per-rover
		// credential isolation is a separate phase.
		for _, p := range claudeStatePaths() {
			rw = append(rw, p)
		}
	}

	policy := Policy{
		Worktree: wtAbs,
		Home:     homeBase,
		RW:       rw,
		RO:       extraRO,
	}

	bw, err := exec.LookPath("bwrap")
	if err != nil {
		fatal("bwrap not on PATH: %v", err)
	}
	argv, err := policy.bwrapArgs()
	if err != nil {
		fatal("build policy: %v", err)
	}

	var inside []string
	if shell {
		inside = []string{"bash"}
	} else {
		claude, err := exec.LookPath("claude")
		if err != nil {
			fatal("claude not on PATH: %v", err)
		}
		inside = []string{claude}
	}
	argv = append(argv, "--")
	argv = append(argv, inside...)

	if dryRun {
		fmt.Println(quoteCmd(bw, argv...))
		return
	}

	if exists, err := tmuxHasWindow(windowName); err != nil {
		fatal("tmux check: %v", err)
	} else if exists {
		fatal("tmux window %q already exists; kill it or pass -window <name>", windowName)
	}

	cmdStr := quoteCmd(bw, argv...)
	if err := tmuxNewWindow(windowName, cmdStr, 30); err != nil {
		fatal("launch: %v", err)
	}
	fmt.Printf("launched in tmux window %q (Ctrl-b w to switch)\n", windowName)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "spore-rover: "+format+"\n", args...)
	os.Exit(1)
}

// quoteCmd renders an argv as a single shell-safe command line. The
// quoting is conservative: anything outside [A-Za-z0-9_./:=-] gets
// single-quoted.
func quoteCmd(prog string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(prog))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '_' || r == '.' || r == '/' || r == ':' || r == '=' || r == '-' || r == '+' || r == ',') {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

// claudeStatePaths returns the host paths claude needs rw access to
// in order to start. Missing paths are skipped silently; absExisting
// in the policy stage will surface anything mandatory.
func claudeStatePaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, rel := range []string{".claude", ".claude.json"} {
		p := filepath.Join(home, rel)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
