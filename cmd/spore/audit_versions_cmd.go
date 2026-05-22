package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/versality/spore/internal/auditversions"
)

const auditVersionsUsage = `spore audit-versions - audit installed tools and flake inputs against versions.json

Usage:
  spore audit-versions [--root <path>] [--bin-root <path>] [--host <name>]
                       [--strict] [--check-dev] [--no-check-dev]

Flags:
  --root          Repo root. Default: $AUDIT_VERSIONS_ROOT or
                  ` + "`git rev-parse --show-toplevel`" + `.
  --bin-root      Directory holding the host's system bin symlinks.
                  Default: $AUDIT_VERSIONS_BIN_ROOT or
                  /run/current-system/sw/bin.
  --host          Host scope (key under audit.tools.host in
                  versions.json). Default: $AUDIT_VERSIONS_HOST or the
                  short hostname.
  --strict        Exit non-zero on any drift / failure. Honors
                  $AUDIT_VERSIONS_STRICT=1.
  --check-dev     Force-on devShell tool audit. Without either flag,
                  defaults to $AUDIT_VERSIONS_DEV_SHELL=1 or
                  $IN_NIX_SHELL non-empty.
  --no-check-dev  Force-off devShell tool audit.

Env (also read directly):
  AUDIT_VERSIONS_VERSIONS_JSON   Raw versions JSON; skips ` + "`nix eval`" + `.
  AUDIT_VERSIONS_LOCK_JSON       Raw flake.lock JSON; skips file read.
`

func runAuditVersions(args []string) int {
	fs := flag.NewFlagSet("audit-versions", flag.ContinueOnError)
	root := fs.String("root", "", "repo root")
	binRoot := fs.String("bin-root", "", "host bin root")
	host := fs.String("host", "", "host scope")
	strict := fs.Bool("strict", false, "exit non-zero on drift/failure")
	checkDev := fs.Bool("check-dev", false, "force devShell audit on")
	noCheckDev := fs.Bool("no-check-dev", false, "force devShell audit off")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprint(os.Stdout, auditVersionsUsage)
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "spore audit-versions: unexpected positional args: %v\n", fs.Args())
		return 2
	}
	if *checkDev && *noCheckDev {
		fmt.Fprintln(os.Stderr, "spore audit-versions: --check-dev and --no-check-dev are mutually exclusive")
		return 2
	}

	resolvedRoot, err := resolveAuditRoot(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore audit-versions:", err)
		return 1
	}

	resolvedHost := *host
	if resolvedHost == "" {
		resolvedHost = os.Getenv("AUDIT_VERSIONS_HOST")
	}
	if resolvedHost == "" {
		h, _ := os.Hostname()
		resolvedHost = strings.Split(h, ".")[0]
	}
	if resolvedHost == "" {
		resolvedHost = "unknown"
	}

	resolvedBinRoot := *binRoot
	if resolvedBinRoot == "" {
		resolvedBinRoot = envDefault("AUDIT_VERSIONS_BIN_ROOT", "/run/current-system/sw/bin")
	}

	resolvedStrict := *strict || os.Getenv("AUDIT_VERSIONS_STRICT") == "1"

	dev := os.Getenv("AUDIT_VERSIONS_DEV_SHELL")
	resolvedCheckDev := *checkDev ||
		(!*noCheckDev && (dev == "1" || (dev == "" && os.Getenv("IN_NIX_SHELL") != "")))

	cfg := auditversions.Config{
		Root:         resolvedRoot,
		BinRoot:      resolvedBinRoot,
		Host:         resolvedHost,
		Strict:       resolvedStrict,
		CheckDev:     resolvedCheckDev,
		VersionsJSON: []byte(os.Getenv("AUDIT_VERSIONS_VERSIONS_JSON")),
		LockJSON:     []byte(os.Getenv("AUDIT_VERSIONS_LOCK_JSON")),
	}

	code, err := auditversions.Run(cfg, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore audit-versions:", err)
		return 1
	}
	return code
}

func resolveAuditRoot(flagRoot string) (string, error) {
	if flagRoot != "" {
		return flagRoot, nil
	}
	if v := os.Getenv("AUDIT_VERSIONS_ROOT"); v != "" {
		return v, nil
	}
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git repo root: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
