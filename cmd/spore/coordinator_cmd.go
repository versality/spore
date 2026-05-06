package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	spore "github.com/versality/spore"
	"github.com/versality/spore/internal/coordinator/loopguard"
	"github.com/versality/spore/internal/coordinator/statedebt"
	"github.com/versality/spore/internal/coordinator/tokenmonitor"
	"github.com/versality/spore/internal/coordinator/verify"
	"github.com/versality/spore/internal/fleet"
)

// defaultCoordinatorStateDir resolves the coordinator state dir from
// the SPORE_COORDINATOR_STATE_DIR env var, falling back to
// $HOME/.local/state/spore/coordinator.
func defaultCoordinatorStateDir() string {
	if d := os.Getenv("SPORE_COORDINATOR_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "spore", "coordinator")
}

const coordinatorUsage = `spore coordinator - coordinator support commands

Usage:
  spore coordinator <subcommand> [flags]

Subcommands:
  start           Spawn the coordinator tmux session (idempotent).
  stop            Kill the coordinator tmux session.
  restart         Stop then start.
  status          Print whether the coordinator session is alive.
  role-brief      Render the coordinator role brief to stdout.
  state-debt      Scan state.md for prose lessons that should be lifted.
  verify-done     Run the verify-done verdict for a slug.
  loop-guard      Check the respawn circuit breaker.
  token-monitor   Stop-hook: check coordinator context budget.
  monitor         Boot-time verdict over the token-monitor ledger.
`

func runCoordinator(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, coordinatorUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(coordinatorUsage)
		return 0
	case "start":
		return runCoordinatorStart(rest)
	case "stop":
		return runCoordinatorStop(rest)
	case "restart":
		return runCoordinatorRestart(rest)
	case "status":
		return runCoordinatorStatus(rest)
	case "role-brief":
		return runCoordinatorRoleBrief(rest)
	case "state-debt":
		return runCoordinatorStateDebt(rest)
	case "verify-done":
		return runCoordinatorVerifyDone(rest)
	case "loop-guard":
		return runCoordinatorLoopGuard(rest)
	case "token-monitor":
		return runCoordinatorTokenMonitor(rest)
	case "monitor":
		return runCoordinatorMonitor(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore coordinator: unknown subcommand %q\n\n%s", sub, coordinatorUsage)
		return 2
	}
}

func runCoordinatorStart(args []string) int {
	fs := flag.NewFlagSet("coordinator start", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "block until the coordinator session exits")
	pollSec := fs.Int("poll-sec", 5, "poll interval for --wait, in seconds")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator start:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator start - spawn the coordinator tmux session")
		fmt.Println("  --wait        block until the session dies (one poll every --poll-sec)")
		fmt.Println("  --poll-sec N  poll interval for --wait (default 5)")
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: spore coordinator start [--wait] [--poll-sec N]")
		return 2
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator start:", err)
		return 1
	}

	session, spawned, err := fleet.EnsureCoordinator(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator start:", err)
		return 1
	}
	if spawned {
		fmt.Printf("coordinator: spawned %s\n", session)
	} else {
		fmt.Printf("coordinator: already running %s\n", session)
	}

	if !*wait {
		return 0
	}
	if *pollSec < 1 {
		*pollSec = 1
	}
	for fleet.CoordinatorAlive(root) {
		time.Sleep(time.Duration(*pollSec) * time.Second)
	}
	fmt.Printf("coordinator: %s exited\n", session)
	return 0
}

func runCoordinatorStop(args []string) int {
	fs := flag.NewFlagSet("coordinator stop", flag.ContinueOnError)
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator stop:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator stop - kill the coordinator tmux session (idempotent)")
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: spore coordinator stop")
		return 2
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator stop:", err)
		return 1
	}
	session := fleet.CoordinatorSessionName(root)
	if fleet.ReapCoordinator(root) {
		fmt.Printf("coordinator: killed %s\n", session)
		return 0
	}
	fmt.Printf("coordinator: not running (%s)\n", session)
	return 0
}

func runCoordinatorRestart(args []string) int {
	fs := flag.NewFlagSet("coordinator restart", flag.ContinueOnError)
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator restart:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator restart - stop then start")
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: spore coordinator restart")
		return 2
	}
	if code := runCoordinatorStop(nil); code != 0 {
		return code
	}
	return runCoordinatorStart(nil)
}

func runCoordinatorStatus(args []string) int {
	fs := flag.NewFlagSet("coordinator status", flag.ContinueOnError)
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator status:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator status - print whether the coordinator session is alive")
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: spore coordinator status")
		return 2
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator status:", err)
		return 1
	}
	session := fleet.CoordinatorSessionName(root)
	cfg, _ := fleet.LoadCoordinatorConfig(root)
	alive := fleet.CoordinatorAlive(root)

	state := "down"
	if alive {
		state = "up"
	}
	fmt.Printf("coordinator: %s (%s)\n", state, session)
	if cfg.Driver != "" {
		fmt.Printf("  driver: %s\n", cfg.Driver)
	}
	if cfg.Model != "" {
		fmt.Printf("  model:  %s\n", cfg.Model)
	}
	if cfg.Brief != "" {
		fmt.Printf("  brief:  %s\n", cfg.Brief)
	}
	if alive {
		return 0
	}
	return 3
}

func runCoordinatorRoleBrief(args []string) int {
	fs := flag.NewFlagSet("coordinator role-brief", flag.ContinueOnError)
	rolePath := fs.String("role", "", "path to role file (default: bundled role.md)")
	consumerPath := fs.String("consumer", "", "optional consumer overlay appended after a separator")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator role-brief:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator role-brief - render the coordinator role brief")
		fmt.Println("  --role <path>      path to role file (default: bundled role.md)")
		fmt.Println("  --consumer <path>  consumer overlay appended after the role file")
		return 0
	}

	role, err := readRole(*rolePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore coordinator role-brief: %v\n", err)
		return 1
	}

	out := role
	if *consumerPath != "" {
		consumer, err := os.ReadFile(*consumerPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "spore coordinator role-brief: %v\n", err)
			return 1
		}
		out = joinRoleConsumer(role, consumer)
	}
	os.Stdout.Write(out)
	return 0
}

// readRole returns the role file at path or, when path is empty, the
// embedded BundledCoordinatorRole shipped with the spore binary.
func readRole(path string) ([]byte, error) {
	if path == "" {
		return spore.BundledCoordinatorRole, nil
	}
	return os.ReadFile(path)
}

// joinRoleConsumer concatenates the role and consumer payloads with one
// blank line between them. Trailing newlines on the role are normalised
// so the join produces exactly one blank line regardless of whether the
// inputs end in `\n`, `\n\n`, or no trailing newline at all.
func joinRoleConsumer(role, consumer []byte) []byte {
	r := trimTrailingNewlines(role)
	out := make([]byte, 0, len(r)+2+len(consumer))
	out = append(out, r...)
	out = append(out, '\n', '\n')
	out = append(out, consumer...)
	return out
}

func trimTrailingNewlines(b []byte) []byte {
	for len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b
}

func runCoordinatorStateDebt(args []string) int {
	fs := flag.NewFlagSet("coordinator state-debt", flag.ContinueOnError)
	verbose := fs.Bool("verbose", false, "print full classification table")
	verboseShort := fs.Bool("v", false, "print full classification table")
	stateFile := fs.String("state-file", "", "path to state.md (default: auto-detect)")
	ageDays := fs.Int("age-days", statedebt.DefaultAgeDays, "threshold in days")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator state-debt:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator state-debt - scan state.md for stale lessons")
		fmt.Println("  --verbose, -v     print full classification table")
		fmt.Println("  --state-file      path to state.md")
		fmt.Println("  --age-days N      threshold in days (default 14)")
		return 0
	}

	cfg := statedebt.Config{AgeDays: *ageDays}
	if *stateFile != "" {
		cfg.StateFile = *stateFile
	}

	result, err := statedebt.Scan(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore coordinator state-debt: %v\n", err)
		return 1
	}

	if *verbose || *verboseShort {
		fmt.Print(statedebt.FormatVerbose(result))
	}

	if result.StaleCount > 0 {
		fmt.Println(statedebt.FormatSummary(result))
		return 2
	}
	return 0
}

func runCoordinatorVerifyDone(args []string) int {
	fs := flag.NewFlagSet("coordinator verify-done", flag.ContinueOnError)
	root := fs.String("root", "", "project root (default: auto-detect)")
	events := fs.String("events", "", "path to events.jsonl")
	projects := fs.String("projects", "", "path to projects dir")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator verify-done:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator verify-done <slug> - run the verify-done verdict")
		fmt.Println("  --root       project root")
		fmt.Println("  --events     events.jsonl path")
		fmt.Println("  --projects   claude projects dir")
		return 0
	}

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: spore coordinator verify-done <slug>")
		return 2
	}
	slug := fs.Arg(0)

	cfg := verify.Config{
		ProjectRoot: *root,
		EventsFile:  *events,
		ProjectsDir: *projects,
	}

	result := verify.Verify(slug, cfg)
	fmt.Print(result.Format())
	return 0
}

func runCoordinatorTokenMonitor(_ []string) int {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator token-monitor: read stdin:", err)
		return 1
	}

	var payload tokenmonitor.HookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0
	}

	cfg := tokenmonitor.Config{
		Inbox: os.Getenv("SPORE_TASK_INBOX"),
	}

	result := tokenmonitor.Check(cfg, payload)
	if result.ShouldFire {
		fmt.Fprint(os.Stderr, result.Message)
		return 2
	}
	return 0
}

func runCoordinatorMonitor(args []string) int {
	fs := flag.NewFlagSet("coordinator monitor", flag.ContinueOnError)
	threshold := fs.Int("threshold", 3, "consecutive-broken count")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator monitor:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator monitor - boot-time verdict over the token-monitor ledger")
		fmt.Println("  --threshold N  consecutive-broken count (default 3)")
		return 0
	}

	cfg := tokenmonitor.Config{Inbox: "self"}
	cfg = cfg.Defaults()

	broken, sessions := tokenmonitor.LedgerVerdict(cfg.LedgerFile, cfg.SoftCap, *threshold)
	if broken {
		fmt.Fprintf(os.Stderr, "broken-hook: %s\n", sessions)
		return 2
	}
	fmt.Println("ok")
	return 0
}

func runCoordinatorLoopGuard(args []string) int {
	fs := flag.NewFlagSet("coordinator loop-guard", flag.ContinueOnError)
	stateDir := fs.String("state-dir", "", "state directory")
	maxRespawns := fs.Int("max-respawns", loopguard.DefaultMaxRespawns, "max respawns in window")
	reset := fs.Bool("reset", false, "reset the trip marker")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore coordinator loop-guard:", err)
		return 2
	}
	if *help || *helpLong {
		fmt.Println("spore coordinator loop-guard - check respawn circuit breaker")
		fmt.Println("  --state-dir       state directory")
		fmt.Println("  --max-respawns N  max respawns in window (default 5)")
		fmt.Println("  --reset           clear the trip marker")
		return 0
	}

	dir := *stateDir
	if dir == "" {
		dir = defaultCoordinatorStateDir()
	}

	if *reset {
		if err := loopguard.Reset(dir); err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "spore coordinator loop-guard: %v\n", err)
				return 1
			}
		}
		fmt.Println("loop-guard: reset")
		return 0
	}

	cfg := loopguard.Config{
		StateDir:    dir,
		MaxRespawns: *maxRespawns,
	}
	status, err := loopguard.Check(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore coordinator loop-guard: %v\n", err)
		return 1
	}

	if status.Tripped {
		fmt.Printf("loop-guard: TRIPPED (recent=%d, max=%d, cooldown=%s)\n",
			status.RecentCount, status.MaxRespawns, status.CooldownLeft)
		return 2
	}
	fmt.Printf("loop-guard: ok (recent=%d, max=%d)\n",
		status.RecentCount, status.MaxRespawns)
	return 0
}
