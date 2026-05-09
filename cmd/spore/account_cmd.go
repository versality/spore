package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/versality/spore/internal/account"
)

const accountUsage = `spore account - per-driver multi-account store, switch, and auto-pick

Usage:
  spore account list   --driver {claude|codex}
  spore account save   --driver <d> <id>
  spore account switch --driver <d> --to <id> [--reason <text>]
  spore account auto   --driver <d> [--reason <text>]
  spore account active --driver <d>

Exit codes:
  0  success (or no-op for switch / auto)
  1  generic error
  2  bad usage
  3  auto: no candidate qualifies. stdout: {"status":"all-ration"}
  4  switch: --to refers to an account not present in the store

Env (per driver):
  AGENT_BUDGET_ACCOUNTS_DIR    claude store dir (default ~/.local/state/claude-accounts)
  AGENT_BUDGET_CREDS           claude live creds (default ~/.claude/.credentials.json)
  SPORE_CODEX_ACCOUNTS_DIR     codex store dir  (default ~/.local/state/codex-accounts)
  SPORE_CODEX_CREDS            codex live creds (default ~/.codex/auth.json)
  AGENT_BUDGET_STATE_DIR       state dir for /usage snapshots (claude only)
`

func runAccount(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, accountUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(accountUsage)
		return 0
	case "list":
		return runAccountList(rest)
	case "save":
		return runAccountSave(rest)
	case "switch":
		return runAccountSwitch(rest)
	case "auto":
		return runAccountAuto(rest)
	case "active":
		return runAccountActive(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore account: unknown subcommand %q\n\n%s", sub, accountUsage)
		return 2
	}
}

func parseDriverFS(name string, args []string, extra func(*flag.FlagSet)) (string, *flag.FlagSet, int) {
	fs := flag.NewFlagSet("account "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	driver := fs.String("driver", "", "driver: claude or codex (required)")
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintf(os.Stderr, "spore account %s: %v\n", name, err)
		return "", nil, 2
	}
	if *driver != account.DriverClaude && *driver != account.DriverCodex {
		fmt.Fprintf(os.Stderr, "spore account %s: --driver must be %q or %q\n", name, account.DriverClaude, account.DriverCodex)
		return "", nil, 2
	}
	return *driver, fs, 0
}

func runAccountList(args []string) int {
	driver, fs, code := parseDriverFS("list", args, nil)
	if code != 0 {
		return code
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore account list: unexpected positional args")
		return 2
	}
	rows, err := account.List(driver)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore account list:", err)
		return 1
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore account list:", err)
		return 1
	}
	fmt.Println(string(b))
	return 0
}

func runAccountSave(args []string) int {
	driver, fs, code := parseDriverFS("save", args, nil)
	if code != 0 {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "spore account save: expected exactly one positional <id>")
		return 2
	}
	if err := account.Save(driver, fs.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "spore account save:", err)
		return 1
	}
	return 0
}

func runAccountSwitch(args []string) int {
	var to, reason string
	driver, fs, code := parseDriverFS("switch", args, func(fs *flag.FlagSet) {
		fs.StringVar(&to, "to", "", "target account id (required)")
		fs.StringVar(&reason, "reason", "", "reason recorded in switches.jsonl")
	})
	if code != 0 {
		return code
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore account switch: unexpected positional args")
		return 2
	}
	if to == "" {
		fmt.Fprintln(os.Stderr, "spore account switch: --to is required")
		return 2
	}
	if err := account.Switch(driver, to, reason); err != nil {
		if errors.Is(err, account.ErrNoSuchAccount) {
			fmt.Fprintln(os.Stderr, "spore account switch:", err)
			return 4
		}
		fmt.Fprintln(os.Stderr, "spore account switch:", err)
		return 1
	}
	return 0
}

func runAccountAuto(args []string) int {
	var reason string
	driver, fs, code := parseDriverFS("auto", args, func(fs *flag.FlagSet) {
		fs.StringVar(&reason, "reason", "", "reason recorded in switches.jsonl")
	})
	if code != 0 {
		return code
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore account auto: unexpected positional args")
		return 2
	}
	picked, err := account.Auto(driver, reason)
	if err != nil {
		if errors.Is(err, account.ErrAllRationed) {
			fmt.Println(`{"status":"all-ration"}`)
			return 3
		}
		fmt.Fprintln(os.Stderr, "spore account auto:", err)
		return 1
	}
	if picked == "" {
		fmt.Println(`{"status":"no-op"}`)
		return 0
	}
	out, _ := json.Marshal(map[string]string{"status": "switched", "to": picked})
	fmt.Println(string(out))
	return 0
}

func runAccountActive(args []string) int {
	driver, fs, code := parseDriverFS("active", args, nil)
	if code != 0 {
		return code
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore account active: unexpected positional args")
		return 2
	}
	id, err := account.Active(driver)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore account active:", err)
		return 1
	}
	if id != "" {
		fmt.Println(id)
	}
	return 0
}
