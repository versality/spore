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
	"time"
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

	DefaultCoordinatorAgent  = "claude"
	DefaultCoordinatorEffort = "high"

	bundledRoot = "bootstrap/flake"
)

// Config describes one infect target.
type Config struct {
	IP                string
	SSHKey            string
	Repo              string
	Flake             string
	Hostname          string
	User              string
	CoordinatorAgent  string
	CoordinatorModel  string
	CoordinatorEffort string
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
	if strings.TrimSpace(c.Repo) != "" {
		info, err := os.Stat(c.Repo)
		if err != nil {
			return fmt.Errorf("repo %q: %w", c.Repo, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("repo %q is not a directory", c.Repo)
		}
	}
	switch c.CoordinatorAgent {
	case "", "claude", "codex":
	default:
		return fmt.Errorf("--coordinator-agent must be claude or codex, got %q", c.CoordinatorAgent)
	}
	switch c.CoordinatorEffort {
	case "", "low", "medium", "high", "xhigh", "very-high", "very_high":
	default:
		return fmt.Errorf("--coordinator-effort must be low, medium, high, xhigh, or very-high, got %q", c.CoordinatorEffort)
	}
	if strings.ContainsAny(c.CoordinatorModel, " \t\r\n") {
		return fmt.Errorf("--coordinator-model must be a single model id without whitespace, got %q", c.CoordinatorModel)
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
	if c.CoordinatorAgent == "" {
		c.CoordinatorAgent = DefaultCoordinatorAgent
	}
	if c.CoordinatorEffort == "" {
		c.CoordinatorEffort = DefaultCoordinatorEffort
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
		"-o", "UserKnownHostsFile=/dev/null",
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
func Run(ctx context.Context, c Config, bundledFlake, bundledHandover fs.FS, stdout, stderr io.Writer) error {
	return run(ctx, c, bundledFlake, bundledHandover, stdout, stderr, runStreaming)
}

type streamRunner func(context.Context, []string, io.Writer, io.Writer) error

func run(ctx context.Context, c Config, bundledFlake, bundledHandover fs.FS, stdout, stderr io.Writer, runner streamRunner) error {
	if err := c.Validate(); err != nil {
		return err
	}
	c.applyDefaults()

	flakeRef, cleanup, err := ResolveFlake(c, bundledFlake)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := runner(ctx, Argv(c, flakeRef), stdout, stderr); err != nil {
		return fmt.Errorf("nixos-anywhere: %w", err)
	}

	fmt.Fprintf(stdout, "[spore] smoke check: ssh %s@%s nixos-version\n", c.User, c.IP)
	if err := runWithRetry(ctx, SmokeArgv(c), stdout, stderr, runner, 24, 5*time.Second); err != nil {
		return fmt.Errorf("smoke check: %w", err)
	}
	if c.Repo != "" {
		if err := Handoff(ctx, c, bundledHandover, stdout, stderr, runner); err != nil {
			return fmt.Errorf("handoff: %w", err)
		}
	}
	return nil
}

func runWithRetry(
	ctx context.Context,
	argv []string,
	stdout, stderr io.Writer,
	runner streamRunner,
	attempts int,
	delay time.Duration,
) error {
	var err error
	for i := 1; i <= attempts; i++ {
		err = runner(ctx, argv, stdout, stderr)
		if err == nil {
			return nil
		}
		if i == attempts {
			break
		}
		fmt.Fprintf(stderr, "[spore] command failed on attempt %d/%d: %v; retrying\n", i, attempts, err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func runStreaming(ctx context.Context, argv []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func Handoff(ctx context.Context, c Config, handover fs.FS, stdout, stderr io.Writer, runner streamRunner) error {
	repo, err := filepath.Abs(c.Repo)
	if err != nil {
		return err
	}
	base := filepath.Base(filepath.Clean(repo))
	if base == "." || base == string(filepath.Separator) {
		return fmt.Errorf("repo %q has no basename", c.Repo)
	}

	handoverDir, cleanup, err := StageHandover(handover, "")
	if err != nil {
		return err
	}
	defer cleanup()

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	remote := "root@" + c.IP
	remoteTmp := "/tmp/spore-handover"

	fmt.Fprintf(stdout, "[spore] installing spore CLI on %s\n", remote)
	if err := runner(ctx, ScpArgv(c, exe, remote+":/tmp/spore"), stdout, stderr); err != nil {
		return fmt.Errorf("copy spore binary: %w", err)
	}
	if err := runner(ctx, RootSSHArgv(c, "install -d -m 0755 /usr/local/bin && install -m 0755 /tmp/spore /usr/local/bin/spore"), stdout, stderr); err != nil {
		return fmt.Errorf("install spore binary: %w", err)
	}

	fmt.Fprintf(stdout, "[spore] copying repo %s to %s:/root/%s\n", repo, remote, base)
	if err := runner(ctx, RsyncRepoArgv(c, repo, remote+":/root/"+base+"/"), stdout, stderr); err != nil {
		return fmt.Errorf("copy repo: %w", err)
	}

	if err := runner(ctx, RootSSHArgv(c, "rm -rf "+shellSingleQuote(remoteTmp)+" && mkdir -p "+shellSingleQuote(remoteTmp)), stdout, stderr); err != nil {
		return fmt.Errorf("prepare handover tmpdir: %w", err)
	}
	handoverSrc := handoverDir + string(filepath.Separator) + "."
	if err := runner(ctx, ScpArgv(c, handoverSrc, remote+":"+remoteTmp+"/"), stdout, stderr); err != nil {
		return fmt.Errorf("copy handover assets: %w", err)
	}
	if err := runner(ctx, RootSSHArgv(c, InstallHandoverScript(c, base, remoteTmp)), stdout, stderr); err != nil {
		return fmt.Errorf("install handover assets: %w", err)
	}

	fmt.Fprintf(stdout, "[spore] handoff ready: ssh -i %s spore@%s\n", c.SSHKey, c.IP)
	return nil
}

func StageHandover(src fs.FS, tmpRoot string) (string, func(), error) {
	dir, err := os.MkdirTemp(tmpRoot, "spore-handover-")
	if err != nil {
		return "", nil, err
	}
	if err := copyEmbedTree(src, "bootstrap/handover", dir); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

func ScpArgv(c Config, src, dst string) []string {
	return []string{
		"scp",
		"-i", c.SSHKey,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=/dev/null",
		"-r",
		src,
		dst,
	}
}

func RootSSHArgv(c Config, remoteCommand string) []string {
	return []string{
		"ssh",
		"-i", c.SSHKey,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		"root@" + c.IP,
		remoteCommand,
	}
}

func RsyncRepoArgv(c Config, repo, dst string) []string {
	repo = filepath.Clean(repo) + string(filepath.Separator)
	args := []string{
		"rsync",
		"-az",
		"--delete",
		"--info=stats1",
		"-e", "ssh -i " + shellSingleQuote(c.SSHKey) + " -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/dev/null",
	}
	for _, pattern := range DefaultRepoExcludes() {
		args = append(args, pattern...)
	}
	return append(args, repo, dst)
}

func DefaultRepoExcludes() [][]string {
	return [][]string{
		{"--include=.env.example"},
		{"--exclude=.env*"},
		{"--exclude=.direnv/"},
		{"--exclude=node_modules/"},
		{"--exclude=vendor/bundle/"},
		{"--exclude=tmp/"},
		{"--exclude=log/"},
		{"--exclude=storage/"},
		{"--exclude=public/assets/"},
		{"--exclude=public/packs/"},
		{"--exclude=.bundle/"},
		{"--exclude=coverage/"},
		{"--exclude=dist/"},
		{"--exclude=build/"},
		{"--exclude=target/"},
		{"--exclude=result"},
		{"--exclude=result-*"},
	}
}

func InstallHandoverScript(c Config, projectBase, remoteTmp string) string {
	projectRoot := "/home/spore/" + projectBase
	rootCopy := "/root/" + projectBase
	coordinatorEnv := strings.Join([]string{
		"SPORE_COORDINATOR_AGENT=/usr/local/bin/spore-coordinator-launch",
		"SPORE_AGENT_BINARY=/usr/local/bin/spore-worker-brief",
		"SPORE_COORDINATOR_PROVIDER=" + c.CoordinatorAgent,
		"SPORE_COORDINATOR_MODEL=" + c.CoordinatorModel,
		"SPORE_COORDINATOR_EFFORT=" + normalizeEffort(c.CoordinatorEffort),
	}, "\n") + "\n"
	firstReconcileEnv := shellEnvArgs([]string{
		"HOME=/home/spore",
		"PATH=/usr/local/bin:/run/current-system/sw/bin:/run/wrappers/bin",
		"SPORE_COORDINATOR_AGENT=/usr/local/bin/spore-coordinator-launch",
		"SPORE_AGENT_BINARY=/usr/local/bin/spore-worker-brief",
		"SPORE_COORDINATOR_PROVIDER=" + c.CoordinatorAgent,
		"SPORE_COORDINATOR_MODEL=" + c.CoordinatorModel,
		"SPORE_COORDINATOR_EFFORT=" + normalizeEffort(c.CoordinatorEffort),
	})
	return strings.Join([]string{
		"set -e",
		"install -d -m 0755 /usr/local/bin",
		"install -d -m 0755 /etc/spore",
		"install -m 0755 " + shellSingleQuote(remoteTmp+"/spore-attach.sh") + " /usr/local/bin/spore-attach",
		"install -m 0755 " + shellSingleQuote(remoteTmp+"/greet-coordinator.sh") + " /usr/local/bin/spore-greet-coordinator",
		"install -m 0755 " + shellSingleQuote(remoteTmp+"/greet-worker.sh") + " /usr/local/bin/spore-greet-worker",
		"install -m 0755 " + shellSingleQuote(remoteTmp+"/spore-coordinator-launch.sh") + " /usr/local/bin/spore-coordinator-launch",
		"install -m 0755 " + shellSingleQuote(remoteTmp+"/spore-worker-brief.sh") + " /usr/local/bin/spore-worker-brief",
		"install -m 0755 " + shellSingleQuote(remoteTmp+"/spore-fleet-tick.sh") + " /usr/local/bin/spore-fleet-tick",
		"install -d -o spore -g users -m 0755 /home/spore/.claude/hooks /home/spore/.config/systemd/user /home/spore/.local/state/spore",
		"install -m 0755 " + shellSingleQuote(remoteTmp+"/hooks/block-bg-bash.pl") + " /home/spore/.claude/hooks/block-bg-bash.pl",
		"install -m 0755 " + shellSingleQuote(remoteTmp+"/hooks/load-state-md.pl") + " /home/spore/.claude/hooks/load-state-md.pl",
		"install -m 0644 " + shellSingleQuote(remoteTmp+"/settings.json") + " /home/spore/.claude/settings.json",
		"install -m 0644 " + shellSingleQuote(remoteTmp+"/systemd/spore-fleet-reconcile.service") + " /home/spore/.config/systemd/user/spore-fleet-reconcile.service",
		"install -m 0644 " + shellSingleQuote(remoteTmp+"/systemd/spore-fleet-reconcile.timer") + " /home/spore/.config/systemd/user/spore-fleet-reconcile.timer",
		"cat > /etc/spore/coordinator.env <<'EOF'\n" + coordinatorEnv + "EOF",
		"rm -rf " + shellSingleQuote(projectRoot),
		"mv " + shellSingleQuote(rootCopy) + " " + shellSingleQuote(projectRoot),
		"install -d -o spore -g users -m 0755 " + shellSingleQuote(projectRoot+"/tasks"),
		"cat > /home/spore/.bashrc <<'EOF'\nexport PATH=/usr/local/bin:/run/current-system/sw/bin:/run/wrappers/bin:$PATH\nif [ -r /etc/spore/coordinator.env ]; then\n  set -a\n  . /etc/spore/coordinator.env\n  set +a\nfi\nEOF",
		"chown -R spore:users " + shellSingleQuote(projectRoot) + " /home/spore/.claude /home/spore/.config /home/spore/.local /home/spore/.bashrc",
		"loginctl enable-linger spore",
		"runuser -u spore -- env " + firstReconcileEnv + " bash -lc " + shellSingleQuote("cd "+shellSingleQuote(projectRoot)+" && spore fleet enable && spore fleet reconcile"),
		"systemctl daemon-reload",
		"systemctl restart spore-coordinator.timer",
		"systemctl restart spore-coordinator.service",
	}, "\n")
}

func normalizeEffort(effort string) string {
	if effort == "very-high" || effort == "very_high" {
		return "xhigh"
	}
	return effort
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellEnvArgs(assignments []string) string {
	quoted := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		quoted = append(quoted, shellSingleQuote(assignment))
	}
	return strings.Join(quoted, " ")
}
