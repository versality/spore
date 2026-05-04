// Package infect wraps nixos-anywhere to bootstrap a fresh,
// SSH-reachable host into a NixOS install. The package shells out to
// `nix run github:nix-community/nixos-anywhere`; it does not
// reimplement any part of that tool.
//
// Two halves are exposed for testing: Argv / SmokeArgv build argv
// slices as pure functions, and Run orchestrates staging the bundled
// flake, executing nixos-anywhere, and smoke-checking ssh.
package infect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// FlakeAttr is the attribute the bundled flake exports under
	// nixosConfigurations. spore infect appends it to the staged
	// flake path when the operator does not supply --flake.
	FlakeAttr = "spore-bootstrap"

	// DefaultHostname is networking.hostName written into the
	// generated local.nix when --hostname is not supplied.
	DefaultHostname = "nixos"

	// DefaultUser is the SSH user nixos-anywhere connects as. Fresh
	// cloud images expose root + a key; non-root callers must have
	// password-less sudo.
	DefaultUser = "root"

	bundledRoot = "bootstrap/flake"
)

// Config describes one infect target.
type Config struct {
	IP       string
	SSHKey   string
	Flake    string
	Hostname string
	User     string
}

// Validate checks required fields and that the SSH key file exists.
func (c Config) Validate() error {
	if strings.TrimSpace(c.IP) == "" {
		return errors.New("ip is required")
	}
	if strings.TrimSpace(c.SSHKey) == "" {
		return errors.New("--ssh-key is required")
	}
	if _, err := os.Stat(c.SSHKey); err != nil {
		return fmt.Errorf("ssh key %q: %w", c.SSHKey, err)
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.Hostname == "" {
		c.Hostname = DefaultHostname
	}
	if c.User == "" {
		c.User = DefaultUser
	}
}

// Argv builds the nixos-anywhere argv for c and an already-resolved
// flakeRef of the form "<path-or-url>#<attr>". Pure function.
func Argv(c Config, flakeRef string) []string {
	c.applyDefaults()
	return []string{
		"nix", "run", "github:nix-community/nixos-anywhere", "--",
		"--flake", flakeRef,
		"-i", c.SSHKey,
		"--target-host", c.User + "@" + c.IP,
	}
}

// SmokeArgv builds the post-install ssh smoke check argv. Pure
// function. StrictHostKeyChecking is set to accept-new because
// nixos-anywhere will have rotated the host key.
func SmokeArgv(c Config) []string {
	c.applyDefaults()
	return []string{
		"ssh",
		"-i", c.SSHKey,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		c.User + "@" + c.IP,
		"nixos-version",
	}
}

// ResolveFlake returns the flakeRef (e.g. "path:/tmp/xyz#spore-bootstrap")
// nixos-anywhere should be pointed at, plus a cleanup function the
// caller must defer. When c.Flake is empty the bundled flake is
// staged into a fresh tempdir with a generated local.nix; otherwise
// c.Flake is used verbatim (with FlakeAttr appended when no '#' is
// present) and cleanup is a no-op.
func ResolveFlake(c Config, bundled fs.FS) (string, func(), error) {
	c.applyDefaults()
	if c.Flake == "" {
		pub, err := PublicKey(c.SSHKey)
		if err != nil {
			return "", nil, err
		}
		dir, err := Stage(bundled, "", c.Hostname, []string{pub})
		if err != nil {
			return "", nil, err
		}
		return "path:" + dir + "#" + FlakeAttr, func() { _ = os.RemoveAll(dir) }, nil
	}
	if strings.Contains(c.Flake, "#") {
		return c.Flake, func() {}, nil
	}
	return c.Flake + "#" + FlakeAttr, func() {}, nil
}

// Stage copies the bundled flake tree out of bundled into a fresh
// temp directory under tmpRoot (default os.TempDir when ""), writes a
// generated local.nix carrying hostname + authorizedKeys, and returns
// the staging directory path. Caller owns cleanup.
func Stage(bundled fs.FS, tmpRoot, hostname string, authorizedKeys []string) (string, error) {
	dir, err := os.MkdirTemp(tmpRoot, "spore-bootstrap-flake-")
	if err != nil {
		return "", err
	}
	if err := copyEmbedTree(bundled, bundledRoot, dir); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	local := RenderLocalNix(hostname, authorizedKeys)
	if err := os.WriteFile(filepath.Join(dir, "local.nix"), []byte(local), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func copyEmbedTree(src fs.FS, root, dst string) error {
	return fs.WalkDir(src, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if filepath.Base(p) == "local.nix.example" {
			return nil
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// RenderLocalNix returns the text of the local.nix module the bundled
// flake imports. Pure function. The same key list authorises both
// root (emergency / admin) and the spore operator user (whose login
// shell attaches to the coordinator tmux session and does nothing
// else).
func RenderLocalNix(hostname string, authorizedKeys []string) string {
	var sb strings.Builder
	sb.WriteString("{\n")
	fmt.Fprintf(&sb, "  networking.hostName = %q;\n", hostname)
	for _, user := range []string{"root", "spore"} {
		fmt.Fprintf(&sb, "  users.users.%s.openssh.authorizedKeys.keys = [\n", user)
		for _, k := range authorizedKeys {
			fmt.Fprintf(&sb, "    %q\n", k)
		}
		sb.WriteString("  ];\n")
	}
	sb.WriteString("}\n")
	return sb.String()
}

// PublicKey reads the .pub sibling of a private SSH key path. Most
// operators have one; if not, ssh-keygen -y -f <key> regenerates it.
func PublicKey(privateKeyPath string) (string, error) {
	pub := privateKeyPath + ".pub"
	b, err := os.ReadFile(pub)
	if err != nil {
		return "", fmt.Errorf(
			"public key %q not found: %w (derive with `ssh-keygen -y -f %s > %s`)",
			pub, err, privateKeyPath, pub,
		)
	}
	out := strings.TrimSpace(string(b))
	if out == "" {
		return "", fmt.Errorf("public key %q is empty", pub)
	}
	return out, nil
}

// Run validates c, stages the flake, executes nixos-anywhere with
// stdout / stderr streamed to the supplied writers, and finishes with
// the ssh nixos-version smoke check. The subprocess exit code is
// preserved in the returned error: callers that need to mirror it can
// inspect with errors.As(err, *exec.ExitError).
func Run(ctx context.Context, c Config, bundled fs.FS, stdout, stderr io.Writer) error {
	if err := c.Validate(); err != nil {
		return err
	}
	c.applyDefaults()

	flakeRef, cleanup, err := ResolveFlake(c, bundled)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := runStreaming(ctx, Argv(c, flakeRef), stdout, stderr); err != nil {
		return fmt.Errorf("nixos-anywhere: %w", err)
	}

	fmt.Fprintf(stdout, "[spore] smoke check: ssh %s@%s nixos-version\n", c.User, c.IP)
	if err := runStreaming(ctx, SmokeArgv(c), stdout, stderr); err != nil {
		return fmt.Errorf("smoke check: %w", err)
	}
	return nil
}

func runStreaming(ctx context.Context, argv []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
