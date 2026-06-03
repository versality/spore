package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Policy is the contract between the operator and the sandbox. The
// the sandboxed agent may write inside Worktree and the named RW paths, may read
// inside RO paths and the read-only root, and may speak HTTPS only to
// the listed AllowHost SNIs via the loopback proxy.
type Policy struct {
	Worktree  string
	Home      string   // path the sandbox sees as $HOME (tmpfs-backed)
	RW        []string // additional rw bind mounts (paths as seen on host)
	RO        []string // additional ro bind mounts on top of the ro root
	AllowHost []string // SNI allowlist for the HTTPS CONNECT proxy
}

// bwrapArgs renders the bwrap argv up to (but not including) the
// trailing "-- <prog> <args...>" terminator. The caller appends the
// command to run inside the sandbox.
func (p Policy) bwrapArgs() ([]string, error) {
	wt, err := absExisting(p.Worktree, true)
	if err != nil {
		return nil, fmt.Errorf("worktree: %w", err)
	}
	if p.Home == "" {
		return nil, fmt.Errorf("policy.Home is required")
	}
	home := filepath.Clean(p.Home)
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("policy.Home must be absolute, got %q", home)
	}

	// Namespace isolation first. Without these the sandbox shares the
	// host's pid/ipc/uts/net/user namespaces and can ps/kill operator
	// processes, read /proc/<pid>/environ for every host process, and
	// post to host POSIX shm. --new-session detaches the controlling
	// tty so a hostile child cannot TIOCSTI-inject keystrokes into the
	// parent tmux pane on kernels without legacy_tiocsti=0.
	// --die-with-parent guarantees cleanup if bwrap or the launcher
	// dies abnormally. --unshare-user-try is best-effort: on a setuid
	// bwrap install it is a no-op, on a non-setuid install it activates
	// the user namespace.
	//
	// Then ro-bind to lay down the host filesystem, tmpfs overlays at
	// the world-writable mount points to mask whatever the operator
	// left there (sibling worktrees in /tmp, dotfiles in $HOME, etc.).
	// Worktree and RW binds come last so they punch through the tmpfs
	// layers.
	args := []string{
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--unshare-cgroup-try",
		"--unshare-net",
		"--unshare-user-try",
		"--new-session",
		"--die-with-parent",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", home,
		"--tmpfs", "/tmp",
		"--tmpfs", "/var/tmp",
		"--tmpfs", "/run/user",
		"--bind", wt, wt,
		"--chdir", wt,
		"--setenv", "HOME", home,
	}
	// Re-expose the nix user profile read-only over the tmpfs home. The
	// agent reaches its own toolchain (git, rg, ...) through
	// $HOME/.nix-profile/bin on nix-user-profile systems; the tmpfs home
	// would otherwise hide it and break `git commit`. Symlinks into the
	// already-ro /nix/store, so this exposes no writable or secret state.
	// On NixOS hosts the toolchain lives in the system profile (outside
	// $HOME) and is already covered by the ro root, so this is a no-op
	// when the path is absent.
	profile := filepath.Join(home, ".nix-profile")
	if _, err := os.Lstat(profile); err == nil {
		args = append(args, "--ro-bind", profile, profile)
	}
	for _, dir := range p.RW {
		abs, err := absExisting(dir, false)
		if err != nil {
			return nil, fmt.Errorf("rw %q: %w", dir, err)
		}
		args = append(args, "--bind", abs, abs)
	}
	for _, dir := range p.RO {
		abs, err := absExisting(dir, false)
		if err != nil {
			return nil, fmt.Errorf("ro %q: %w", dir, err)
		}
		args = append(args, "--ro-bind", abs, abs)
	}
	return args, nil
}

func absExisting(path string, mustBeDir bool) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if mustBeDir && !st.IsDir() {
		return "", fmt.Errorf("%q is not a directory", abs)
	}
	return abs, nil
}
