// spore-rover wraps an LLM coding agent in a bubblewrap (bwrap)
// sandbox and runs it in a tmux window the operator can watch.
//
// Threat model (three points, nothing else):
//
//   - FS write-allowlist. The rover writes only inside its worktree
//     and the small RW set listed in the policy. Operator dotfiles
//     (~/.ssh, ~/.bashrc, sibling worktrees) are inaccessible.
//   - FS read-deny. /etc/shadow, ~/.ssh, /root, etc. unreadable.
//   - Network egress allowlist. When -allow is set, --unshare-net is
//     passed to bwrap and the rover sees only an in-sandbox HTTPS
//     CONNECT proxy that lets through the named SNIs.

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
	// Subcommands selected by the wrapper script. Dispatched before
	// flag.Parse so each subcommand owns its own argv tail.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--inside":
			runInside(os.Args[2:])
			return
		case "--proxy-serve":
			runProxyServe(os.Args[2:])
			return
		}
	}

	var (
		worktree   string
		windowName string
		shell      bool
		homeBase   string
		extraRW    multiFlag
		extraRO    multiFlag
		allowHost  multiFlag
		dryRun     bool
	)
	flag.StringVar(&worktree, "worktree", ".", "directory the rover may write to (becomes cwd inside sandbox)")
	flag.StringVar(&windowName, "window", "", "tmux window name (default: rover-<worktree-base>)")
	flag.BoolVar(&shell, "shell", false, "drop into bash instead of launching claude")
	flag.StringVar(&homeBase, "home", os.Getenv("HOME"), "path the sandbox exposes as $HOME (tmpfs-backed; must exist on host)")
	flag.Var(&extraRW, "rw", "additional rw bind (repeatable; host path)")
	flag.Var(&extraRO, "ro", "additional ro bind (repeatable; host path)")
	flag.Var(&allowHost, "allow", "HTTPS CONNECT hostname allowlist (repeatable); enables --unshare-net + loopback proxy")
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
		Worktree:  wtAbs,
		Home:      homeBase,
		RW:        rw,
		RO:        extraRO,
		AllowHost: allowHost,
	}

	bw, err := exec.LookPath("bwrap")
	if err != nil {
		fatal("bwrap not on PATH: %v", err)
	}
	argv, err := policy.bwrapArgs()
	if err != nil {
		fatal("build policy: %v", err)
	}

	var target []string
	if shell {
		target = []string{"bash"}
	} else {
		claude, err := exec.LookPath("claude")
		if err != nil {
			fatal("claude not on PATH: %v", err)
		}
		target = []string{claude}
	}

	var launchCmd, sockDir string
	if len(allowHost) > 0 {
		selfPath, err := os.Executable()
		if err != nil {
			fatal("locate self: %v", err)
		}
		if dryRun {
			sockDir = "/tmp/spore-rover-DRYRUN"
		} else {
			sockDir, err = os.MkdirTemp("", "spore-rover-"+windowName+"-")
			if err != nil {
				fatal("mkdir tempdir: %v", err)
			}
		}
		sock := filepath.Join(sockDir, "proxy.sock")
		argv = append(argv, "--unshare-net", "--bind", sockDir, sockDir)
		insideArgs := []string{selfPath, "--inside", "-sock", sock}
		insideArgs = append(insideArgs, target...)
		argv = append(argv, "--")
		argv = append(argv, insideArgs...)

		logPath := filepath.Join(sockDir, "proxy.log")
		proxyCmd := quoteCmd(selfPath, "--proxy-serve", "-sock", sock, "-log", logPath, "-allow", strings.Join(allowHost, ","))
		bwrapCmd := quoteCmd(bw, argv...)
		// Pane script: start host-side proxy in background, run
		// bwrap+target in foreground (so the tmux pane is the tty
		// for claude's TUI), then tear down on exit. The trap fires
		// even if bwrap exits abnormally.
		launchCmd = fmt.Sprintf(
			"%s &\nPROXY_PID=$!\ntrap 'kill $PROXY_PID 2>/dev/null; rm -rf %s' EXIT\n%s\n",
			proxyCmd, shellQuote(sockDir), bwrapCmd,
		)
	} else {
		argv = append(argv, "--")
		argv = append(argv, target...)
		launchCmd = quoteCmd(bw, argv...)
	}

	if dryRun {
		fmt.Println(launchCmd)
		return
	}

	if exists, err := tmuxHasWindow(windowName); err != nil {
		fatal("tmux check: %v", err)
	} else if exists {
		fatal("tmux window %q already exists; kill it or pass -window <name>", windowName)
	}

	if err := tmuxNewWindow(windowName, launchCmd, 30); err != nil {
		fatal("launch: %v", err)
	}
	fmt.Printf("launched in tmux window %q (Ctrl-b w to switch)\n", windowName)
	if sockDir != "" {
		fmt.Printf("proxy log: %s/proxy.log\n", sockDir)
	}
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
