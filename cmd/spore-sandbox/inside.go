package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"
)

// runInside is the bwrap-child entrypoint when network is sandboxed.
// It opens a loopback TCP listener and forwards every accepted byte
// stream to the host-side HTTPS CONNECT proxy via a unix socket bound
// into the sandbox. It exposes that listener to the target program
// via HTTPS_PROXY and execs the program. Exits with the target's rc.
func runInside(args []string) {
	fs := flag.NewFlagSet("--inside", flag.ExitOnError)
	var sock string
	fs.StringVar(&sock, "sock", "", "unix socket path inside sandbox that connects to host proxy")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "spore-sandbox --inside: missing target command")
		os.Exit(2)
	}
	if sock == "" {
		fmt.Fprintln(os.Stderr, "spore-sandbox --inside: -sock is required")
		os.Exit(2)
	}

	// Wait briefly for the host proxy to bind the socket. The outer
	// process starts the listener before tmux launches us, so this
	// usually succeeds on the first dial.
	if err := waitForUnixSocket(sock, 2*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "spore-sandbox --inside: %v\n", err)
		os.Exit(1)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore-sandbox --inside: loopback listen: %v\n", err)
		os.Exit(1)
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "sandbox-proxy: "+format+"\n", args...)
	}
	go forwardTCPToUnix(ln, sock, logf)
	proxyURL := fmt.Sprintf("http://%s", ln.Addr().(*net.TCPAddr).String())

	cmd := exec.Command(rest[0], rest[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	loopPort := ln.Addr().(*net.TCPAddr).Port
	cmd.Env = append(os.Environ(),
		"HTTPS_PROXY="+proxyURL,
		"HTTP_PROXY="+proxyURL,
		"ALL_PROXY="+proxyURL,
		"NO_PROXY=localhost,127.0.0.1,::1",
		fmt.Sprintf("SPORE_LOOP_PORT=%d", loopPort),
	)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "spore-sandbox --inside: start %s: %v\n", rest[0], err)
		os.Exit(1)
	}
	stopSignals := forwardSignals(cmd.Process)
	err = cmd.Wait()
	stopSignals()
	ln.Close()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "spore-sandbox --inside: wait: %v\n", err)
		os.Exit(1)
	}
}

func waitForUnixSocket(path string, deadline time.Duration) error {
	end := time.Now().Add(deadline)
	for {
		if _, err := os.Stat(path); err == nil {
			c, err := net.Dial("unix", path)
			if err == nil {
				c.Close()
				return nil
			}
		}
		if time.Now().After(end) {
			return fmt.Errorf("host proxy socket %q never appeared", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
