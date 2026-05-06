package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/versality/spore/internal/worker/tokenmonitor"
)

const workerUsage = `spore worker - worker support hooks

Usage:
  spore worker <subcommand> [flags]

Subcommands:
  token-monitor   Stop-hook: check the worker's context budget and fire
                  a wrap-up reminder once it crosses the tier-keyed cap.
                  Tier read from $SPORE_ACCOUNT_TIER (defaults to non-max);
                  override per-tier caps with $SPORE_WORKER_TOKEN_WRAP,
                  $SPORE_WORKER_TOKEN_WRAP_MAX, $SPORE_WORKER_TOKEN_WRAP_SUB.
                  Skips coordinator inboxes (handled by spore coordinator
                  token-monitor) and sessions with no $SPORE_TASK_INBOX.
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
		fmt.Fprint(os.Stderr, result.Message)
		return 2
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
