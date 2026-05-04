package infect

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestArgvDefaults(t *testing.T) {
	got := Argv(Config{IP: "203.0.113.7", SSHKey: "/k/id_ed25519"}, "/tmp/stage#spore-bootstrap")
	want := []string{
		"nix", "run", "github:nix-community/nixos-anywhere", "--",
		"--flake", "/tmp/stage#spore-bootstrap",
		"-i", "/k/id_ed25519",
		"--target-host", "root@203.0.113.7",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestArgvCustomUser(t *testing.T) {
	got := Argv(Config{IP: "10.0.0.1", SSHKey: "/k", User: "ubuntu"}, "git+ssh://example/flake#vm")
	wantTail := []string{"--target-host", "ubuntu@10.0.0.1"}
	if !reflect.DeepEqual(got[len(got)-2:], wantTail) {
		t.Fatalf("target-host wrong: got %v", got)
	}
	if got[5] != "git+ssh://example/flake#vm" {
		t.Fatalf("flakeRef wrong: %v", got[5])
	}
}

func TestSmokeArgv(t *testing.T) {
	got := SmokeArgv(Config{IP: "203.0.113.7", SSHKey: "/k/id"})
	want := []string{
		"ssh",
		"-i", "/k/id",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		"root@203.0.113.7",
		"nixos-version",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("smoke argv mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestValidate(t *testing.T) {
	tmp := t.TempDir()
	keyPath := filepath.Join(tmp, "id")
	if err := os.WriteFile(keyPath, []byte("priv"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   Config
		want string
	}{
		{"missing ip", Config{SSHKey: keyPath}, "ip is required"},
		{"missing key", Config{IP: "1.1.1.1"}, "--ssh-key is required"},
		{"absent key", Config{IP: "1.1.1.1", SSHKey: filepath.Join(tmp, "absent")}, "ssh key"},
		{"ok", Config{IP: "1.1.1.1", SSHKey: keyPath}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestRenderLocalNix(t *testing.T) {
	got := RenderLocalNix("box-1", []string{"ssh-ed25519 AAAA op@host", "ssh-rsa BBBB op2@host"})
	want := `{
  networking.hostName = "box-1";
  users.users.root.openssh.authorizedKeys.keys = [
    "ssh-ed25519 AAAA op@host"
    "ssh-rsa BBBB op2@host"
  ];
  users.users.spore.openssh.authorizedKeys.keys = [
    "ssh-ed25519 AAAA op@host"
    "ssh-rsa BBBB op2@host"
  ];
}
`
	if got != want {
		t.Fatalf("local.nix mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestRenderLocalNixEmptyKeys(t *testing.T) {
	got := RenderLocalNix("nixos", nil)
	want := "{\n  networking.hostName = \"nixos\";\n  users.users.root.openssh.authorizedKeys.keys = [\n  ];\n  users.users.spore.openssh.authorizedKeys.keys = [\n  ];\n}\n"
	if got != want {
		t.Fatalf("empty keys mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestPublicKey(t *testing.T) {
	tmp := t.TempDir()
	priv := filepath.Join(tmp, "id_ed25519")
	if err := os.WriteFile(priv, []byte("priv"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PublicKey(priv); err == nil {
		t.Fatalf("expected error when .pub missing")
	}
	if err := os.WriteFile(priv+".pub", []byte("ssh-ed25519 AAAA op\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pub, err := PublicKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if pub != "ssh-ed25519 AAAA op" {
		t.Fatalf("trim: got %q", pub)
	}
}

// fakeBundled mirrors the layout the real embed.FS exposes, so Stage
// and ResolveFlake can be exercised in unit tests without the real
// asset.
func fakeBundled() fstest.MapFS {
	return fstest.MapFS{
		"bootstrap/flake/flake.nix":         {Data: []byte("{}")},
		"bootstrap/flake/configuration.nix": {Data: []byte("{}")},
		"bootstrap/flake/disk-config.nix":   {Data: []byte("{}")},
		"bootstrap/flake/local.nix.example": {Data: []byte("# example")},
		"bootstrap/flake/README.md":         {Data: []byte("# bundled")},
	}
}

func fakeHandover() fstest.MapFS {
	return fstest.MapFS{
		"bootstrap/handover/spore-attach.sh":                       {Data: []byte("#!/bin/sh\n")},
		"bootstrap/handover/greet-coordinator.sh":                  {Data: []byte("#!/bin/sh\n")},
		"bootstrap/handover/greet-worker.sh":                       {Data: []byte("#!/bin/sh\n")},
		"bootstrap/handover/spore-coordinator-launch.sh":           {Data: []byte("#!/bin/sh\n")},
		"bootstrap/handover/spore-worker-brief.sh":                 {Data: []byte("#!/bin/sh\n")},
		"bootstrap/handover/spore-fleet-tick.sh":                   {Data: []byte("#!/bin/sh\n")},
		"bootstrap/handover/hooks/block-bg-bash.pl":                {Data: []byte("#!/usr/bin/env perl\n")},
		"bootstrap/handover/hooks/load-state-md.pl":                {Data: []byte("#!/usr/bin/env perl\n")},
		"bootstrap/handover/settings.json":                         {Data: []byte("{}\n")},
		"bootstrap/handover/systemd/spore-fleet-reconcile.service": {Data: []byte("[Service]\n")},
		"bootstrap/handover/systemd/spore-fleet-reconcile.timer":   {Data: []byte("[Timer]\n")},
	}
}

func TestStage(t *testing.T) {
	tmp := t.TempDir()
	dir, err := Stage(fakeBundled(), tmp, "myhost", []string{"ssh-ed25519 KKKK op"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"flake.nix", "configuration.nix", "disk-config.nix", "README.md", "local.nix"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s in stage dir: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "local.nix.example")); err == nil {
		t.Fatalf("local.nix.example should be skipped during staging")
	}
	got, err := os.ReadFile(filepath.Join(dir, "local.nix"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `networking.hostName = "myhost"`) {
		t.Fatalf("local.nix missing hostname: %s", got)
	}
	if !strings.Contains(string(got), `"ssh-ed25519 KKKK op"`) {
		t.Fatalf("local.nix missing key: %s", got)
	}
}

func TestResolveFlakeBundled(t *testing.T) {
	tmp := t.TempDir()
	priv := filepath.Join(tmp, "id")
	if err := os.WriteFile(priv, []byte("priv"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(priv+".pub", []byte("ssh-ed25519 KKKK op\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Config{IP: "1.1.1.1", SSHKey: priv}
	ref, cleanup, err := ResolveFlake(c, fakeBundled())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !strings.HasSuffix(ref, "#"+FlakeAttr) {
		t.Fatalf("flakeRef should end with bundled attr: %s", ref)
	}
	if !strings.HasPrefix(ref, "path:") {
		t.Fatalf("staged flakeRef should use path: scheme: %s", ref)
	}
	stageDir := strings.TrimPrefix(strings.TrimSuffix(ref, "#"+FlakeAttr), "path:")
	if _, err := os.Stat(filepath.Join(stageDir, "local.nix")); err != nil {
		t.Fatalf("staged local.nix missing: %v", err)
	}
	cleanup()
	if _, err := os.Stat(stageDir); !os.IsNotExist(err) {
		t.Fatalf("cleanup should remove stage dir: %v", err)
	}
}

func TestResolveFlakeUserSupplied(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"./myflake", "./myflake#" + FlakeAttr},
		{"./myflake#myhost", "./myflake#myhost"},
		{"github:owner/repo#vm", "github:owner/repo#vm"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			ref, cleanup, err := ResolveFlake(Config{IP: "1.1.1.1", SSHKey: "/k", Flake: tc.in}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if ref != tc.want {
				t.Fatalf("got %q want %q", ref, tc.want)
			}
		})
	}
}

func TestRunUsesResolvedPathFlakeRef(t *testing.T) {
	tmp := t.TempDir()
	priv := filepath.Join(tmp, "id")
	if err := os.WriteFile(priv, []byte("priv"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(priv+".pub", []byte("ssh-ed25519 KKKK op\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls [][]string
	runner := func(_ context.Context, argv []string, _, _ io.Writer) error {
		calls = append(calls, append([]string(nil), argv...))
		return nil
	}

	err := run(
		context.Background(),
		Config{IP: "203.0.113.7", SSHKey: priv},
		fakeBundled(),
		fakeHandover(),
		io.Discard,
		io.Discard,
		runner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("got %d runner calls, want nixos-anywhere and smoke check", len(calls))
	}

	flakeRef := argAfter(calls[0], "--flake")
	if !strings.HasPrefix(flakeRef, "path:") {
		t.Fatalf("flakeRef should use path: scheme: %s", flakeRef)
	}
	if strings.Contains(flakeRef, "git+file:///tmp") {
		t.Fatalf("flakeRef leaked tmp git ref: %s", flakeRef)
	}
	stageDir := strings.TrimPrefix(strings.TrimSuffix(flakeRef, "#"+FlakeAttr), "path:")
	if filepath.Base(stageDir) == "" || !strings.HasPrefix(filepath.Base(stageDir), "spore-bootstrap-flake-") {
		t.Fatalf("flakeRef should point at the staged flake dir, got %s", flakeRef)
	}
	if got := calls[1][0]; got != "ssh" {
		t.Fatalf("second call should be smoke check ssh, got %v", calls[1])
	}
}

func TestRsyncRepoArgvIncludesGitAndExcludesSecrets(t *testing.T) {
	got := RsyncRepoArgv(
		Config{IP: "203.0.113.7", SSHKey: "/home/me/.ssh/id_ed25519"},
		"/home/me/project",
		"root@203.0.113.7:/root/project/",
	)
	joined := strings.Join(got, "\x00")
	for _, want := range []string{
		"--include=.env.example",
		"--exclude=.env*",
		"--exclude=node_modules/",
		"--exclude=target/",
		"root@203.0.113.7:/root/project/",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rsync argv missing %q: %v", want, got)
		}
	}
	if strings.Contains(joined, ".git") {
		t.Fatalf("rsync argv should not exclude .git: %v", got)
	}
}

func TestRunWithRepoRunsHandoff(t *testing.T) {
	tmp := t.TempDir()
	priv := filepath.Join(tmp, "id")
	if err := os.WriteFile(priv, []byte("priv"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(priv+".pub", []byte("ssh-ed25519 KKKK op\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(tmp, "project")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	var calls [][]string
	runner := func(_ context.Context, argv []string, _, _ io.Writer) error {
		calls = append(calls, append([]string(nil), argv...))
		return nil
	}

	err := run(
		context.Background(),
		Config{
			IP:                "203.0.113.7",
			SSHKey:            priv,
			Repo:              repo,
			CoordinatorAgent:  "codex",
			CoordinatorModel:  "gpt-5.5",
			CoordinatorEffort: "xhigh",
		},
		fakeBundled(),
		fakeHandover(),
		io.Discard,
		io.Discard,
		runner,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 8 {
		t.Fatalf("got %d runner calls, want install + smoke + handoff calls: %v", len(calls), calls)
	}
	if calls[4][0] != "rsync" {
		t.Fatalf("fifth call should copy repo with rsync, got %v", calls[4])
	}
	if calls[6][0] != "scp" || !strings.HasSuffix(calls[6][len(calls[6])-2], string(filepath.Separator)+".") {
		t.Fatalf("handover scp should copy staged contents, got %v", calls[6])
	}
	script := calls[7][len(calls[7])-1]
	for _, want := range []string{
		"SPORE_COORDINATOR_PROVIDER=codex",
		"SPORE_COORDINATOR_MODEL=gpt-5.5",
		"SPORE_COORDINATOR_EFFORT=xhigh",
		"mv '/root/project' '/home/spore/project'",
		"install -d -o spore -g users -m 0755 '/home/spore/project/tasks'",
		"spore fleet enable && spore fleet reconcile",
		"systemctl restart spore-coordinator.service",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("handover script missing %q:\n%s", want, script)
		}
	}
}

func argAfter(argv []string, key string) string {
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == key {
			return argv[i+1]
		}
	}
	return ""
}
