package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// runProxyServe is the host-side entrypoint that owns the actual
// CONNECT proxy. It binds a unix socket listed in the --inside
// shim's argv, serves until SIGTERM/SIGINT, and removes the socket
// file on exit. The caller (the tmux pane wrapper) is expected to
// background it and kill it once the bwrap'd target exits.
func runProxyServe(args []string) {
	fs := flag.NewFlagSet("--proxy-serve", flag.ExitOnError)
	var sock, allowList, logFile string
	fs.StringVar(&sock, "sock", "", "unix socket path to bind")
	fs.StringVar(&allowList, "allow", "", "comma-separated CONNECT hostname allowlist")
	fs.StringVar(&logFile, "log", "", "redirect proxy log to file (default: stderr)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if sock == "" {
		fmt.Fprintln(os.Stderr, "spore-sandbox --proxy-serve: -sock is required")
		os.Exit(2)
	}

	logOut := os.Stderr
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err == nil {
			logOut = f
			defer f.Close()
		}
	}

	var allow []string
	for h := range strings.SplitSeq(allowList, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			allow = append(allow, h)
		}
	}

	// Pre-clean stale socket. The path lives in a per-sandbox tmpdir
	// so this is safe.
	_ = os.Remove(sock)

	p := newProxy(allow)
	p.logf = log.New(logOut, "sandbox-proxy: ", log.LstdFlags).Printf
	ln, err := p.listenUnix(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore-sandbox --proxy-serve: bind %s: %v\n", sock, err)
		os.Exit(1)
	}
	defer os.Remove(sock)
	defer ln.Close()

	// On the host, unix socket file inherits umask. Make it
	// accessible only by the owning user.
	_ = os.Chmod(sock, 0o600)

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
}
