package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/versality/spore/internal/codextrust"
)

const codexUsage = `spore codex - Codex integration helpers

Usage:
  spore codex trust status
  spore codex trust add --yes
`

func runCodex(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, codexUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(codexUsage)
		return 0
	case "trust":
		return runCodexTrust(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "spore codex: unknown subcommand %q\n\n%s", args[0], codexUsage)
		return 2
	}
}

func runCodexTrust(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, codexUsage)
		return 2
	}
	switch args[0] {
	case "status":
		root, ok := codexTrustProjectRoot()
		if !ok {
			fmt.Fprintln(os.Stderr, "spore codex trust status: spore.toml not found")
			return 1
		}
		st, err := codextrust.Inspect(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore codex trust status:", err)
			return 1
		}
		if st.Trusted {
			fmt.Printf("codex trust: trusted %s\n", st.Root)
			return 0
		}
		fmt.Printf("codex trust: untrusted %s\nconfig: %s\n", st.Root, st.ConfigPath)
		return 1
	case "add":
		return runCodexTrustAdd(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "spore codex trust: unknown subcommand %q\n\n%s", args[0], codexUsage)
		return 2
	}
}

func runCodexTrustAdd(args []string) int {
	fs := flag.NewFlagSet("codex trust add", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "write Codex config")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore codex trust add:", err)
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore codex trust add: unexpected positional args:", fs.Args())
		return 2
	}
	root, ok := codexTrustProjectRoot()
	if !ok {
		fmt.Fprintln(os.Stderr, "spore codex trust add: spore.toml not found")
		return 1
	}
	if !*yes {
		fmt.Fprintln(os.Stderr, "spore codex trust add: pass --yes to modify Codex config")
		return 2
	}
	st, err := codextrust.Inspect(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore codex trust add:", err)
		return 1
	}
	if st.Trusted {
		fmt.Printf("codex trust: already trusted %s\n", st.Root)
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(st.ConfigPath), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "spore codex trust add:", err)
		return 1
	}
	f, err := os.OpenFile(st.ConfigPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore codex trust add:", err)
		return 1
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[projects.%s]\ntrust_level = \"trusted\"\n", strconv.Quote(st.Root)); err != nil {
		fmt.Fprintln(os.Stderr, "spore codex trust add:", err)
		return 1
	}
	fmt.Printf("codex trust: trusted %s\n", st.Root)
	return 0
}

func codexTrustProjectRoot() (string, bool) {
	if root := os.Getenv("SPORE_PROJECT_ROOT"); root != "" {
		if _, err := os.Stat(filepath.Join(root, "spore.toml")); err == nil {
			return filepath.Clean(root), true
		}
	}
	return findSporeRoot(cwdOrDot())
}
