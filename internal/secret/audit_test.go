package secret

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAuditFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	manifest := `let
  hostA = "age1aaa";
  hostB = "age1bbb";
in {
  "alpha.age".publicKeys = [ hostA hostB ];
  "beta.age".publicKeys = [ hostA ];
  "ghost.age".publicKeys = [ hostA ];
}
`
	writeAuditFile(t, filepath.Join(root, "secrets/secrets.nix"), manifest)
	writeAuditFile(t, filepath.Join(root, "secrets/alpha.age"), "ciphertext\n")
	writeAuditFile(t, filepath.Join(root, "secrets/beta.age"), "ciphertext\n")
	writeAuditFile(t, filepath.Join(root, "secrets/orphan.age"), "ciphertext\n")
	writeAuditFile(t, filepath.Join(root, "nix/features/svc.nix"), `{
  age.secrets.alpha = { file = ../../secrets/alpha.age; };
  systemd.services.foo.script = "echo ${config.age.secrets.alpha.path}";
}
`)
	writeAuditFile(t, filepath.Join(root, "bash/wrapper.sh"), "echo beta\n")
	writeAuditFile(t, filepath.Join(root, "templates/example.nix"), `# alpha mention should not count`+"\n")
	writeAuditFile(t, filepath.Join(root, "secrets/README.md"), "alpha mention should not count\n")
	return root
}

func findingByName(t *testing.T, r AuditResult, name string) Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.File == name {
			return f
		}
	}
	t.Fatalf("no finding for %q", name)
	return Finding{}
}

func TestAuditFlagsOrphanGhostUnconsumed(t *testing.T) {
	root := setupRepo(t)
	r, err := Audit(AuditConfig{Repo: root})
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if r.Clean {
		t.Fatalf("expected dirty result, got clean: %+v", r.Findings)
	}

	alpha := findingByName(t, r, "alpha.age")
	if !alpha.Registered || !alpha.OnDisk {
		t.Fatalf("alpha should be registered+on-disk: %+v", alpha)
	}
	if len(alpha.Consumers) != 1 || !strings.HasSuffix(alpha.Consumers[0], "svc.nix") {
		t.Fatalf("alpha consumers: %+v", alpha.Consumers)
	}

	beta := findingByName(t, r, "beta.age")
	if !beta.Registered || !beta.OnDisk {
		t.Fatalf("beta should be registered+on-disk: %+v", beta)
	}
	if len(beta.Consumers) != 1 || !strings.HasSuffix(beta.Consumers[0], "wrapper.sh") {
		t.Fatalf("beta consumers: %+v", beta.Consumers)
	}

	orphan := findingByName(t, r, "orphan.age")
	if orphan.Registered {
		t.Fatalf("orphan should not be registered: %+v", orphan)
	}
	if !orphan.OnDisk {
		t.Fatalf("orphan should be on disk: %+v", orphan)
	}

	ghost := findingByName(t, r, "ghost.age")
	if !ghost.Registered {
		t.Fatalf("ghost should be registered: %+v", ghost)
	}
	if ghost.OnDisk {
		t.Fatalf("ghost should not be on disk: %+v", ghost)
	}
	if len(ghost.Consumers) != 0 {
		t.Fatalf("ghost should be unconsumed: %+v", ghost.Consumers)
	}
}

func TestAuditCleanWhenAllResolved(t *testing.T) {
	root := t.TempDir()
	manifest := `{ "only.age".publicKeys = [ "age1xxx" ]; }` + "\n"
	writeAuditFile(t, filepath.Join(root, "secrets/secrets.nix"), manifest)
	writeAuditFile(t, filepath.Join(root, "secrets/only.age"), "c\n")
	writeAuditFile(t, filepath.Join(root, "nix/features/use.nix"), `age.secrets.only.path`+"\n")
	r, err := Audit(AuditConfig{Repo: root})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Clean {
		t.Fatalf("expected clean: %+v", r.Findings)
	}
	if len(r.Findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(r.Findings))
	}
}

func TestAuditExcludesSecretsAndTemplatesFromConsumers(t *testing.T) {
	root := t.TempDir()
	manifest := `{ "x.age".publicKeys = [ "age1xxx" ]; }` + "\n"
	writeAuditFile(t, filepath.Join(root, "secrets/secrets.nix"), manifest)
	writeAuditFile(t, filepath.Join(root, "secrets/x.age"), "c\n")
	writeAuditFile(t, filepath.Join(root, "templates/proto.nix"), "x reference\n")
	r, err := Audit(AuditConfig{Repo: root})
	if err != nil {
		t.Fatal(err)
	}
	x := findingByName(t, r, "x.age")
	if len(x.Consumers) != 0 {
		t.Fatalf("templates/ should not produce consumers: %+v", x.Consumers)
	}
	if r.Clean {
		t.Fatalf("expected dirty (no consumer)")
	}
}

func TestAuditMissingManifestErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := Audit(AuditConfig{Repo: root}); err == nil {
		t.Fatal("expected error when manifest is missing")
	}
}

func TestWriteAuditTable(t *testing.T) {
	r := AuditResult{
		Findings: []Finding{
			{File: "alpha.age", Registered: true, OnDisk: true, Consumers: []string{"nix/a.nix"}},
			{File: "ghost.age", Registered: true, OnDisk: false},
		},
	}
	var buf bytes.Buffer
	WriteAuditTable(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "FILE") || !strings.Contains(out, "REGISTERED") {
		t.Fatalf("missing header: %s", out)
	}
	if !strings.Contains(out, "alpha.age") || !strings.Contains(out, "yes") {
		t.Fatalf("missing alpha row: %s", out)
	}
	if !strings.Contains(out, "ghost.age") || !strings.Contains(out, "(none)") {
		t.Fatalf("missing ghost row: %s", out)
	}
}
