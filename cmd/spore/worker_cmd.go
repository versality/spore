package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"

	"github.com/versality/spore/internal/worker/exitkind"
	"github.com/versality/spore/internal/worker/tokenmonitor"
)

const workerUsage = `spore worker - worker support hooks

Usage:
  spore worker <subcommand> [flags]

Subcommands:
  exit-kind       Classify a rower wrapper's exit shape into one of
                  lifecycle | early-exit | sighup-external | crash-rc<n>
                  and (optionally) emit a single 'wt task tell skyhelm'
                  envelope so the coordinator gets one classified signal
                  per rower end. Flags:
                    --rc=<N>          wrapper's final rc (required)
                    --marker=<path>   clean-exit marker the rower writes
                                      BEFORE its own teardown; presence
                                      means lifecycle even on rc=129
                    --slug=<slug>     slug to include in the tell body
                    --tell-skyhelm    shell out to 'wt task tell skyhelm'
                                      with "<slug> exit kind=<k> rc=<N>"
                  Prints <kind> on stdout. When --tell-skyhelm is set
                  but wt is unavailable, exits non-zero so the wrapper
                  can log it; the classify line is still printed.

  token-monitor   Stop-hook: check the worker's context budget and fire
                  a wrap-up reminder once it crosses the tier-keyed cap.
                  Tier read from $SPORE_ACCOUNT_TIER (defaults to non-max);
                  override per-tier caps with $SPORE_WORKER_TOKEN_WRAP,
                  $SPORE_WORKER_TOKEN_WRAP_MAX, $SPORE_WORKER_TOKEN_WRAP_SUB.
                  Skips coordinator inboxes (handled by spore coordinator
                  token-monitor) and sessions with no $SPORE_TASK_INBOX.
                  On a wrap fire, the per-(slug, session) marker dedups
                  re-fires inside one session and the per-slug counter
                  at $WT_STATE/worker-wrap-count/<slug> ticks once per
                  resume cycle, surfacing in the wrap message and in
                  $WT_STATE/worker-voluntary-events.jsonl plus
                  $WT_STATE/events.jsonl.
`

func runWorker(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, workerUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(workerUsage)
		return 0
	case "token-monitor":
		return runWorkerTokenMonitor(rest)
	case "exit-kind":
		return runWorkerExitKind(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore worker: unknown subcommand %q\n\n%s", sub, workerUsage)
		return 2
	}
}

func runWorkerTokenMonitor(_ []string) int {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore worker token-monitor: read stdin:", err)
		return 1
	}

	var payload tokenmonitor.HookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0
	}

	cfg := tokenmonitor.Config{
		Inbox:        os.Getenv("SPORE_TASK_INBOX"),
		Tier:         os.Getenv("SPORE_ACCOUNT_TIER"),
		WrapOverride: envInt("SPORE_WORKER_TOKEN_WRAP"),
		WrapMax:      envInt("SPORE_WORKER_TOKEN_WRAP_MAX"),
		WrapSub:      envInt("SPORE_WORKER_TOKEN_WRAP_SUB"),
	}

	result := tokenmonitor.Check(cfg, payload)
	if result.ShouldFire {
		bk := tokenmonitor.Bookkeep(tokenmonitor.BookkeepingConfig{}, payload.SessionID, result)
		msg := tokenmonitor.AnnotateMessage(result.Message, bk, result.Slug)
		fmt.Fprint(os.Stderr, msg)
		return 2
	}
	return 0
}

// runWorkerExitKind classifies a rower wrapper exit and (optionally)
// emits the single skyhelm-bound tell that replaces the four-ledger
// split (rower-voluntary-events.jsonl, respawn-events.jsonl,
// rower-watch.json, agent.log). The classify is unconditional so a
// caller can pipe the kind into agent.log even when no tell goes out.
func runWorkerExitKind(args []string) int {
	fs := flag.NewFlagSet("exit-kind", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		rc     int
		marker string
		slug   string
		tell   bool
	)
	fs.IntVar(&rc, "rc", -1, "wrapper final rc")
	fs.StringVar(&marker, "marker", "", "clean-exit marker path")
	fs.StringVar(&slug, "slug", "", "rower slug for the tell body")
	fs.BoolVar(&tell, "tell-skyhelm", false, "emit `wt task tell skyhelm` envelope")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore worker exit-kind:", err)
		return 2
	}
	if rc < 0 {
		fmt.Fprintln(os.Stderr, "spore worker exit-kind: --rc is required")
		return 2
	}
	kind := exitkind.Classify(rc, marker)
	fmt.Println(kind)
	if !tell {
		return 0
	}
	if slug == "" {
		fmt.Fprintln(os.Stderr, "spore worker exit-kind: --slug is required with --tell-skyhelm")
		return 2
	}
	if _, err := exec.LookPath("wt"); err != nil {
		fmt.Fprintln(os.Stderr, "spore worker exit-kind: wt not on PATH; tell skipped")
		return 1
	}
	body := fmt.Sprintf("%s exit kind=%s rc=%d", slug, kind, rc)
	cmd := exec.Command("wt", "task", "tell", "skyhelm", body)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "spore worker exit-kind: tell failed:", err)
		return 1
	}
	return 0
}

func envInt(name string) int {
	v := os.Getenv(name)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
