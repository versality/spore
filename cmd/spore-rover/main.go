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
	"time"

	"github.com/versality/spore/internal/sandboxcfg"
)

// subcommands routes os.Args[1] to its handler. Each subcommand owns
// its own flag.FlagSet so its argv tail is parsed next to its
// implementation. The default (no recognized subcommand) is runLaunch.
var subcommands = map[string]func([]string){
	"--inside":      runInside,
	"--proxy-serve": runProxyServe,
}

func main() {
	if len(os.Args) > 1 {
		if h, ok := subcommands[os.Args[1]]; ok {
			h(os.Args[2:])
			return
		}
	}
	runLaunch(os.Args[1:])
}

func runLaunch(args []string) {
	fs := flag.NewFlagSet("spore-rover", flag.ExitOnError)
	var (
		worktree       string
		windowName     string
		shell          bool
		targetName     string
		homeBase       string
		extraRW        multiFlag
		extraRO        multiFlag
		allowHost      multiFlag
		redteam        bool
		redteamTimeout time.Duration
		dryRun         bool
	)
	fs.StringVar(&worktree, "worktree", ".", "directory the rover may write to (becomes cwd inside sandbox)")
	fs.StringVar(&windowName, "window", "", "tmux window name (default: rover-<worktree-base>)")
	fs.BoolVar(&shell, "shell", false, "drop into bash instead of launching the target binary")
	fs.StringVar(&targetName, "target", "claude", "registered target to launch (see target.go)")
	fs.StringVar(&homeBase, "home", os.Getenv("HOME"), "path the sandbox exposes as $HOME (tmpfs-backed; must exist on host)")
	fs.Var(&extraRW, "rw", "additional rw bind (repeatable; host path)")
	fs.Var(&extraRO, "ro", "additional ro bind (repeatable; host path)")
	fs.Var(&allowHost, "allow", "HTTPS CONNECT hostname allowlist (repeatable); enables --unshare-net + loopback proxy")
	fs.BoolVar(&redteam, "redteam", false, "after launching, paste the 12-probe rover prompt and write a verdict")
	fs.DurationVar(&redteamTimeout, "redteam-timeout", 5*time.Minute, "max wall time to wait for the redteam summary marker")
	fs.BoolVar(&dryRun, "dry-run", false, "print the bwrap argv and exit")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

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

	var tgt target
	if !shell {
		var ok bool
		tgt, ok = targets[targetName]
		if !ok {
			fatal("unknown -target %q (known: %s)", targetName, knownTargets())
		}
	}

	// Project [sandbox] (and user-level override) merge with CLI
	// flags. Precedence weakest-to-strongest: defaults < user <
	// project < CLI. Merge appends with dedupe, so CLI items always
	// end up last. The project root is the worktree dir.
	fileCfg, err := sandboxcfg.LoadForProject(wtAbs)
	if err != nil {
		fatal("load sandbox config: %v", err)
	}
	cliCfg := sandboxcfg.Config{
		AllowHosts: allowHost,
		RW:         extraRW,
		RO:         extraRO,
	}
	merged := sandboxcfg.Merge(fileCfg, cliCfg)

	rw := append([]string(nil), merged.RW...)
	if !shell {
		for _, p := range tgt.StatePaths() {
			rw = append(rw, p)
		}
	}

	policy := Policy{
		Worktree:  wtAbs,
		Home:      homeBase,
		RW:        rw,
		RO:        merged.RO,
		AllowHost: merged.AllowHosts,
	}

	bw, err := exec.LookPath("bwrap")
	if err != nil {
		fatal("bwrap not on PATH: %v", err)
	}
	bwrapArgv, err := policy.bwrapArgs()
	if err != nil {
		fatal("build policy: %v", err)
	}

	var targetArgv []string
	if shell {
		targetArgv = []string{"bash"}
	} else {
		bin, err := exec.LookPath(tgt.Bin)
		if err != nil {
			fatal("%s not on PATH: %v", tgt.Bin, err)
		}
		targetArgv = []string{bin}
	}

	launchCmd, sockDir, err := buildLaunchCmd(bw, bwrapArgv, targetArgv, merged.AllowHosts, windowName, dryRun)
	if err != nil {
		fatal("build launch: %v", err)
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

	if redteam {
		if shell {
			fatal("-redteam requires claude (drop -shell)")
		}
		pass, err := runRedteam(windowName, wtAbs, redteamTimeout)
		if err != nil {
			fatal("redteam: %v", err)
		}
		if !pass {
			os.Exit(2)
		}
	}
}

// buildLaunchCmd renders the shell command tmuxNewWindow runs in the
// rover's pane.
//
// Pane-lifecycle invariant: when allowHost is non-empty the rover
// runs with --unshare-net, so the CONNECT proxy must live on the
// host side and be reachable via a unix socket that bwrap binds into
// the sandbox. The shell here owns three lifetimes:
//
//  1. start the host proxy in the background so it is listening
//     before bwrap re-execs --inside;
//  2. install an EXIT trap that kills the proxy and removes the
//     per-rover socket dir, fires even on abnormal bwrap exit;
//  3. run bwrap in the foreground so the tmux pane is the tty for
//     claude's TUI.
//
// In the no-network case the launch is just bwrap + target with no
// host-side helper, so the lifecycle collapses to a single command.
//
// sockDir is the empty string when no proxy is needed.
func buildLaunchCmd(bw string, bwrapArgv, target, allowHost []string, windowName string, dryRun bool) (string, string, error) {
	if len(allowHost) == 0 {
		argv := append(bwrapArgv, "--")
		argv = append(argv, target...)
		return quoteCmd(bw, argv...), "", nil
	}

	selfPath, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("locate self: %w", err)
	}

	var sockDir string
	if dryRun {
		sockDir = "/tmp/spore-rover-DRYRUN"
	} else {
		sockDir, err = os.MkdirTemp("", "spore-rover-"+windowName+"-")
		if err != nil {
			return "", "", fmt.Errorf("mkdir tempdir: %w", err)
		}
	}
	sock := filepath.Join(sockDir, "proxy.sock")
	logPath := filepath.Join(sockDir, "proxy.log")

	// Ensure the host path of the rover binary stays reachable
	// inside the sandbox after the tmpfs layers wipe out /tmp and
	// /home. Without this the --inside re-exec fails with ENOENT.
	argv := append(bwrapArgv,
		"--unshare-net",
		"--bind", sockDir, sockDir,
		"--ro-bind", selfPath, selfPath,
		"--",
	)
	insideArgs := append([]string{selfPath, "--inside", "-sock", sock}, target...)
	argv = append(argv, insideArgs...)

	proxyCmd := quoteCmd(selfPath, "--proxy-serve", "-sock", sock, "-log", logPath, "-allow", strings.Join(allowHost, ","))
	bwrapCmd := quoteCmd(bw, argv...)
	launchCmd := fmt.Sprintf(
		"%s &\nPROXY_PID=$!\ntrap 'kill $PROXY_PID 2>/dev/null; rm -rf %s' EXIT\n%s\n",
		proxyCmd, shellQuote(sockDir), bwrapCmd,
	)
	return launchCmd, sockDir, nil
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

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
