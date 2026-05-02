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
	case "pretooluse":
		return runHooksPreToolUse()
	case "stop":
		return runHooksStop()
	case "settings":
		return runHooksSettings()
	case "watch-inbox":
		return runHooksWatchInbox(rest)
	case "notify-coordinator":
		return runHooksNotifyCoordinator(rest)
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
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: spore hooks watch-inbox <slug>")
		return 2
	}
	err := hooks.WatchInbox(args[0])
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
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: spore hooks notify-coordinator <slug>")
		return 2
	}
	if err := hooks.NotifyCoordinator(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "spore hooks notify-coordinator:", err)
		return 1
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
