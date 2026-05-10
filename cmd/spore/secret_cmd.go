package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/versality/spore/internal/secret"
)

const secretUsage = `spore secret - age secret helpers

Usage:
  spore secret add --recipient KEY [--recipient KEY ...] [--recipients-file PATH] --out PATH [--label LABEL]
  spore secret audit [--repo PATH] [--secrets-dir DIR] [--format text|json]

add: Opens a tmux popup so the operator can paste a value, encrypts
under all recipients via the system 'age' binary, and writes the
ciphertext to --out. The agent invoking this command never sees the
plaintext.

audit: Inventories every .age file under <repo>/<secrets-dir> against
secrets.nix declarations and consumer references in <repo>/nix and
<repo>/bash. Exits non-zero on any orphan-on-disk, ghost manifest
entry, or unconsumed secret.

add flags:
  -r, --recipient KEY     age public key (age1...). Repeatable.
      --recipients-file P File of one age public key per line; '#'
                          starts a line comment; blanks ignored.
  -o, --out PATH          Destination .age path. Parent must exist.
      --label TEXT        Shown in the popup title (cosmetic).

audit flags:
      --repo PATH         Repo root (default: current directory).
      --secrets-dir DIR   Secrets directory under repo (default: secrets).
      --format FMT        text (default) or json.

Environment (add):
  TMUX                    Required. The popup launches inside the
                          calling tmux session.
  XDG_RUNTIME_DIR         Tmpfs scratch dir for the operator paste
                          (falls back to TMPDIR).
`

type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func runSecret(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, secretUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(secretUsage)
		return 0
	case "add":
		return runSecretAdd(args[1:])
	case "audit":
		return runSecretAudit(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "spore secret: unknown subcommand %q\n\n%s", args[0], secretUsage)
		return 2
	}
}

func runSecretAdd(args []string) int {
	fs := flag.NewFlagSet("secret add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var recipients stringSlice
	fs.Var(&recipients, "recipient", "age public key (repeatable)")
	fs.Var(&recipients, "r", "age public key (repeatable, short)")
	recipientsFile := fs.String("recipients-file", "", "file of one recipient per line")
	out := fs.String("out", "", "destination .age path")
	outShort := fs.String("o", "", "destination .age path (short)")
	label := fs.String("label", "", "popup title text")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")

	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore secret add:", err)
		fmt.Fprint(os.Stderr, secretUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(secretUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore secret add: unexpected positional args")
		fmt.Fprint(os.Stderr, secretUsage)
		return 2
	}
	target := *out
	if target == "" {
		target = *outShort
	}
	cfg := secret.Config{
		Recipients:     []string(recipients),
		RecipientsFile: *recipientsFile,
		Out:            target,
		Label:          *label,
		Stderr:         os.Stderr,
	}
	if err := secret.Add(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "spore secret add:", err)
		return 1
	}
	return 0
}

func runSecretAudit(args []string) int {
	fs := flag.NewFlagSet("secret audit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", "", "repo root (default: current directory)")
	secretsDir := fs.String("secrets-dir", "", "secrets directory under repo (default: secrets)")
	format := fs.String("format", "text", "output format: text|json")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")

	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore secret audit:", err)
		fmt.Fprint(os.Stderr, secretUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(secretUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore secret audit: unexpected positional args")
		fmt.Fprint(os.Stderr, secretUsage)
		return 2
	}
	switch *format {
	case "text", "json":
	default:
		fmt.Fprintf(os.Stderr, "spore secret audit: unknown --format %q (want text|json)\n", *format)
		return 2
	}

	result, err := secret.Audit(secret.AuditConfig{
		Repo:       *repo,
		SecretsDir: *secretsDir,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore secret audit:", err)
		return 2
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fmt.Fprintln(os.Stderr, "spore secret audit: encode:", err)
			return 2
		}
	default:
		secret.WriteAuditTable(os.Stdout, result)
	}
	if !result.Clean {
		return 1
	}
	return 0
}
