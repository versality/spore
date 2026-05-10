package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/versality/spore/internal/search"
)

const searchUsage = `spore search - lookup helpers against external search backends

Usage:
  spore search nix packages    QUERY [flags]
  spore search nix options     QUERY [flags]
  spore search home-manager    QUERY [flags]

Searches nixpkgs packages or NixOS options against the Elasticsearch
backend at https://search.nixos.org/backend, and home-manager options
against the JSON dump that powers https://home-manager-options.extranix.com.

Flags (nix):
  -c, --channel CH    Channel: unstable (default), 25.11, 25.05, ...
  -n, --size N        Max results (default 10).
      --json          Emit JSON instead of TSV.
  -h, --help          This help.

Flags (home-manager):
  -r, --release REL   Release (default release-25.11). Examples:
                      release-25.11, release-25.05, master.
  -n, --size N        Max results (default 10).
      --refresh       Force re-download of the options JSON.
      --json          Emit JSON instead of TSV.
  -h, --help          This help.

Environment overrides (nix):
  NIXOS_SEARCH_VERSION   Index mapping version.
  NIXOS_SEARCH_USER      Basic-auth username.
  NIXOS_SEARCH_PASS      Basic-auth password.
`

func runSearch(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, searchUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(searchUsage)
		return 0
	case "nix":
		return runSearchNix(args[1:])
	case "home-manager", "hm":
		return runSearchHM(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "spore search: unknown subcommand %q\n\n%s", args[0], searchUsage)
		return 2
	}
}

func runSearchHM(args []string) int {
	fs := flag.NewFlagSet("search home-manager", flag.ContinueOnError)
	release := fs.String("release", search.HMDefaultRelease, "release")
	releaseShort := fs.String("r", "", "release (short)")
	size := fs.Int("size", search.HMDefaultSize, "max results")
	sizeShort := fs.Int("n", 0, "max results (short)")
	refresh := fs.Bool("refresh", false, "force re-download")
	asJSON := fs.Bool("json", false, "emit JSON")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore search home-manager:", err)
		fmt.Fprint(os.Stderr, searchUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(searchUsage)
		return 0
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "spore search home-manager: expected exactly one QUERY")
		fmt.Fprint(os.Stderr, searchUsage)
		return 2
	}
	if *releaseShort != "" {
		release = releaseShort
	}
	if *sizeShort != 0 {
		size = sizeShort
	}

	opts := search.HMRequest{
		Query:   fs.Arg(0),
		Release: *release,
		Size:    *size,
		Refresh: *refresh,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	res, err := search.HM(ctx, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore search home-manager:", err)
		return 1
	}

	if *asJSON {
		out, err := search.FormatHMJSON(res)
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore search home-manager:", err)
			return 1
		}
		_, _ = io.WriteString(os.Stdout, out)
	} else {
		_, _ = io.WriteString(os.Stdout, search.FormatHMText(res))
	}
	return 0
}

func runSearchNix(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, searchUsage)
		return 2
	}
	var kind search.NixKind
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(searchUsage)
		return 0
	case "packages":
		kind = search.KindPackages
	case "options":
		kind = search.KindOptions
	default:
		fmt.Fprintf(os.Stderr, "spore search nix: unknown kind %q (want packages or options)\n\n%s", args[0], searchUsage)
		return 2
	}
	rest := args[1:]

	fs := flag.NewFlagSet("search nix", flag.ContinueOnError)
	channel := fs.String("channel", search.DefaultChannel, "channel")
	channelShort := fs.String("c", "", "channel (short)")
	size := fs.Int("size", search.DefaultSize, "max results")
	sizeShort := fs.Int("n", 0, "max results (short)")
	asJSON := fs.Bool("json", false, "emit JSON")
	help := fs.Bool("h", false, "show help")
	helpLong := fs.Bool("help", false, "show help")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, rest)); err != nil {
		fmt.Fprintln(os.Stderr, "spore search nix:", err)
		fmt.Fprint(os.Stderr, searchUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(searchUsage)
		return 0
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "spore search nix: expected exactly one QUERY")
		fmt.Fprint(os.Stderr, searchUsage)
		return 2
	}
	if *channelShort != "" {
		channel = channelShort
	}
	if *sizeShort != 0 {
		size = sizeShort
	}

	opts := search.NixRequest{
		Kind:    kind,
		Query:   fs.Arg(0),
		Channel: *channel,
		Size:    *size,
		Version: strings.TrimSpace(os.Getenv("NIXOS_SEARCH_VERSION")),
		User:    strings.TrimSpace(os.Getenv("NIXOS_SEARCH_USER")),
		Pass:    os.Getenv("NIXOS_SEARCH_PASS"),
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	res, err := search.Nix(ctx, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore search nix:", err)
		return 1
	}

	if *asJSON {
		out, err := search.FormatJSON(res)
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore search nix:", err)
			return 1
		}
		_, _ = io.WriteString(os.Stdout, out)
	} else {
		_, _ = io.WriteString(os.Stdout, search.FormatText(res))
	}
	return 0
}
