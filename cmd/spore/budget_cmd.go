package main

import (
	"fmt"
	"os"

	"github.com/versality/spore/internal/budget"
)

const budgetUsage = `spore budget - rolling 5h + 7d Anthropic-spend tracker

Usage:
  spore budget refresh       recompute state from the configured collection mode
  spore budget query         print state.json with a fresh advice band
  spore budget summary       human one-liner (default)
  spore budget capture       read one JSON header line from stdin, append to spool
  spore budget stop-hook     Stop-hook helper: exit 2 + stderr reminder on a
                             fresh band crossing, exit 0 otherwise
  spore budget debug-usage   hit /usage once and print raw + parsed response
                             (diagnostic; bypasses the freshness throttle)

Env (AGENT_BUDGET_* names preserved verbatim so spore and the basecamp
binary can shadow-soak against the same state file):
  AGENT_BUDGET_MODE          force "subscription" or "api" (default: auto-detect)
  AGENT_BUDGET_IDENTITY      identity tag used for capture and api-mode read filtering
  AGENT_BUDGET_SHORT_CAP     USD cap for the rolling 5h transcript window (default 250)
  AGENT_BUDGET_LONG_CAP      USD cap for the rolling 7d transcript window (default 2000)
  AGENT_BUDGET_STATE_DIR     state directory (default $XDG_STATE_HOME/agent-budget
                             or ~/.local/state/agent-budget)
  AGENT_BUDGET_PROJECTS      transcript root (default ~/.claude/projects)
  AGENT_BUDGET_CREDS         OAuth credentials path (default ~/.claude/.credentials.json)
  AGENT_BUDGET_USAGE_MIN_INTERVAL_SEC
                             minimum seconds between subscription /usage hits (default 60;
                             0 disables the throttle for tests / operator override)

Files (mode 0600):
  $AGENT_BUDGET_STATE_DIR/state.json         tracker state
  $AGENT_BUDGET_STATE_DIR/api-headers.jsonl  per-response Anthropic ratelimit spool
  $AGENT_BUDGET_STATE_DIR/markers/           per-window-per-band fresh-crossing markers
`

func runBudget(args []string) int {
	if len(args) < 1 {
		return runBudgetCmd("summary", nil)
	}
	return runBudgetCmd(args[0], args[1:])
}

func runBudgetCmd(sub string, rest []string) int {
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(budgetUsage)
		return 0
	case "refresh":
		return budgetExec(budget.Refresh, "refresh", rest)
	case "query":
		return budgetExec(budget.Query, "query", rest)
	case "summary":
		return budgetExec(budget.Summary, "summary", rest)
	case "capture":
		return budgetExec(budget.Capture, "capture", rest)
	case "debug-usage":
		return budgetExec(budget.DebugUsage, "debug-usage", rest)
	case "stop-hook":
		if len(rest) != 0 {
			fmt.Fprintln(os.Stderr, "spore budget stop-hook: takes no args")
			return 2
		}
		return budget.StopHook()
	default:
		fmt.Fprintf(os.Stderr, "spore budget: unknown subcommand %q\n\n%s", sub, budgetUsage)
		return 2
	}
}

func budgetExec(fn func() error, name string, rest []string) int {
	if len(rest) != 0 {
		fmt.Fprintf(os.Stderr, "spore budget %s: takes no args\n", name)
		return 2
	}
	if err := fn(); err != nil {
		fmt.Fprintf(os.Stderr, "spore budget %s: %v\n", name, err)
		return 1
	}
	return 0
}
