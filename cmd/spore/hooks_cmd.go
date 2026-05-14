package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/hooks/contexttee"
	"github.com/versality/spore/internal/hooks/prfinish"
	"github.com/versality/spore/internal/hooks/pushpending"
	"github.com/versality/spore/internal/hooks/workercontinue"
	"github.com/versality/spore/internal/hooks/wtmergemechanical"
)

func runHooks(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, hooksUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(hooksUsage)
		return 0
	case "install":
		return runHooksInstall(rest)
	case "commit-msg":
		return runHooksCommitMsg(rest)
	case "pre-commit":
		return runHooksPreCommit(rest)
	case "pretooluse":
		return runHooksPreToolUse()
	case "stop":
		return runHooksStop()
	case "wtmerge-mechanical":
		return runHooksWtMergeMechanical()
	case "push-pending":
		return runHooksPushPending()
	case "pr-finish":
		return runHooksPRFinish()
	case "settings":
		return runHooksSettings()
	case "watch-inbox":
		return runHooksWatchInbox(rest)
	case "notify-coordinator":
		return runHooksNotifyCoordinator(rest)
	case "plan-ready-mechanical":
		return runHooksPlanReadyMechanical(rest)
	case "worker-continue":
		return runHooksWorkerContinue(rest)
	case "codex":
		return runHooksCodex(rest)
	case "context-tee":
		return runHooksContextTee(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore hooks: unknown subcommand %q\n\n%s", sub, hooksUsage)
		return 2
	}
}

func runHooksInstall(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "spore hooks install: takes no args")
		return 2
	}
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks install:", err)
		return 1
	}
	dir, err := hooks.Install(root, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks install:", err)
		return 1
	}
	fmt.Println(dir)
	return 0
}

func runHooksCommitMsg(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "spore hooks commit-msg: usage: commit-msg <file>")
		return 2
	}
	if err := hooks.CommitMsg(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks commit-msg:", err)
		return 1
	}
	return 0
}

func runHooksPreCommit(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "spore hooks pre-commit: takes no args")
		return 2
	}
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks pre-commit:", err)
		return 1
	}
	if err := hooks.PreCommit(root); err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks pre-commit:", err)
		return 1
	}
	return 0
}

func runHooksPreToolUse() int {
	req, err := readHookRequest()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks pretooluse:", err)
		return 1
	}
	resp := hooks.PreToolUse(req, hooks.DefaultForbidden())
	return writeHookResponse(resp)
}

func runHooksStop() int {
	req, err := readHookRequest()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks stop:", err)
		return 1
	}
	resp := hooks.Stop(req)
	return writeHookResponse(resp)
}

// runHooksWtMergeMechanical is the M1 Stop-hook entry point: when a
// claude worker stops idle on its wt/<slug> branch with shipped-but-
// unmerged commits and a clean tree, exit 2 with a deterministic
// nudge to run `wt merge` (or write the next step and continue).
// Otherwise exit 0 silently. See docs/todo/worker-lifecycle-fsm.md
// section 9.
func runHooksWtMergeMechanical() int {
	req, err := readHookRequest()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks wtmerge-mechanical:", err)
		return 1
	}
	res := wtmergemechanical.Run(req, wtmergemechanical.Deps{})
	if res.Stderr != "" {
		fmt.Fprint(os.Stderr, res.Stderr)
	}
	return res.ExitCode
}

// runHooksPushPending is the M-finish-B Stop-hook entry point: when a
// worker idles after `wt merge` has fast-forwarded local main but
// origin/main is still behind, exit 2 with a deterministic nudge to
// run `git push` (or `wt ship` once it lands). Otherwise exit 0
// silently. See tasks/spore-worker-finish-contract.md section 5.
func runHooksPushPending() int {
	req, err := readHookRequest()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks push-pending:", err)
		return 1
	}
	res := pushpending.Run(req, pushpending.Deps{})
	if res.Stderr != "" {
		fmt.Fprint(os.Stderr, res.Stderr)
	}
	return res.ExitCode
}

// runHooksPRFinish is the M-finish-C Stop-hook entry point: when a
// worker idles on wt/<slug>, inspect the matching PR via `gh pr view`
// and exit 2 with a deterministic next-step prompt (merge / rebase /
// fix CI) when the PR needs one. Otherwise exit 0 silently. See
// tasks/spore-worker-finish-contract.md section 5.
func runHooksPRFinish() int {
	req, err := readHookRequest()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks pr-finish:", err)
		return 1
	}
	res := prfinish.Run(req, prfinish.Deps{})
	if res.Stderr != "" {
		fmt.Fprint(os.Stderr, res.Stderr)
	}
	return res.ExitCode
}

func readHookRequest() (hooks.Request, error) {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		return hooks.Request{}, fmt.Errorf("read stdin: %w", err)
	}
	var req hooks.Request
	if err := json.Unmarshal(body, &req); err != nil {
		return hooks.Request{}, fmt.Errorf("unmarshal request: %w", err)
	}
	return req, nil
}

func writeHookResponse(resp hooks.Response) int {
	enc, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks: marshal response:", err)
		return 1
	}
	if _, err := os.Stdout.Write(enc); err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks: write response:", err)
		return 1
	}
	os.Stdout.Write([]byte{'\n'})
	return 0
}

// settingsInput is the JSON schema read from stdin by `spore hooks settings`.
type settingsInput struct {
	Events map[string][]settingsInputBin `json:"events"`
}

type settingsInputBin struct {
	Command     string `json:"command"`
	Matcher     string `json:"matcher,omitempty"`
	Timeout     int    `json:"timeout,omitempty"`
	Async       bool   `json:"async,omitempty"`
	AsyncRewake bool   `json:"asyncRewake,omitempty"`
}

func runHooksSettings() int {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks settings:", err)
		return 1
	}
	var input settingsInput
	if err := json.Unmarshal(body, &input); err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks settings: bad input:", err)
		return 1
	}
	events := make(map[string][]hooks.HookBin, len(input.Events))
	for name, bins := range input.Events {
		for _, b := range bins {
			events[name] = append(events[name], hooks.HookBin{
				BinPath:     b.Command,
				Matcher:     b.Matcher,
				Timeout:     b.Timeout,
				Async:       b.Async,
				AsyncRewake: b.AsyncRewake,
			})
		}
	}
	out, err := hooks.Settings(events)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks settings:", err)
		return 1
	}
	os.Stdout.Write(out)
	return 0
}

func runHooksWatchInbox(args []string) int {
	var err error
	switch len(args) {
	case 0:
		inbox := os.Getenv("SPORE_TASK_INBOX")
		if inbox == "" {
			fmt.Fprintln(os.Stderr, "spore hooks watch-inbox: SPORE_TASK_INBOX is required when slug is omitted")
			return 2
		}
		err = hooks.WatchInboxAt(inbox)
	case 1:
		err = hooks.WatchInbox(args[0])
	default:
		fmt.Fprintln(os.Stderr, "usage: spore hooks watch-inbox [slug]")
		return 2
	}
	if err == hooks.ErrWake {
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks watch-inbox:", err)
		return 1
	}
	return 0
}

func runHooksNotifyCoordinator(args []string) int {
	switch len(args) {
	case 0:
		if err := hooks.NotifyCoordinatorEnv(); err != nil {
			fmt.Fprintln(os.Stderr, "spore hooks notify-coordinator:", err)
			return 1
		}
		return 0
	case 1:
		if err := hooks.NotifyCoordinator(args[0]); err != nil {
			fmt.Fprintln(os.Stderr, "spore hooks notify-coordinator:", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: spore hooks notify-coordinator [project]")
		return 2
	}
}

func runHooksPlanReadyMechanical(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "spore hooks plan-ready-mechanical: takes no args")
		return 2
	}
	if err := hooks.PlanReadyMechanicalEnv(); err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks plan-ready-mechanical:", err)
		return 1
	}
	return 0
}

func runHooksWorkerContinue(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "spore hooks worker-continue: takes no args")
		return 2
	}
	res, err := workercontinue.RunEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks worker-continue:", err)
		return 1
	}
	if res.ShouldFire {
		fmt.Fprint(os.Stderr, res.Message)
		return 2
	}
	return 0
}

func runHooksContextTee(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "spore hooks context-tee: takes no args")
		return 2
	}
	cfg := contexttee.Config{
		Inbox:               os.Getenv("SPORE_TASK_INBOX"),
		CoordinatorStateDir: defaultCoordinatorStateDirEnv(),
		Tier:                os.Getenv("SPORE_ACCOUNT_TIER"),
		CoordSoftCap:        envInt("SPORE_COORDINATOR_TOKEN_SOFT"),
		CoordHardCap:        envInt("SPORE_COORDINATOR_TOKEN_HARD"),
		WorkerWrapMax:       envInt("SPORE_WORKER_TOKEN_WRAP_MAX"),
		WorkerWrapSub:       envInt("SPORE_WORKER_TOKEN_WRAP_SUB"),
		WorkerWrapOverride:  envInt("SPORE_WORKER_TOKEN_WRAP"),
	}
	if d := os.Getenv("SPORE_WORKER_TOKEN_DIR"); d != "" {
		cfg.WorkerTokenDir = d
	}
	if _, err := contexttee.Run(cfg, os.Stdin); err != nil {
		// Best-effort: log and exit 0 so the Stop chain keeps going.
		fmt.Fprintln(os.Stderr, "spore hooks context-tee:", err)
	}
	return 0
}

func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "-c", "safe.directory="+wd, "rev-parse", "--show-toplevel")
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git rev-parse: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
