package auditversions

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testLockJSON = `{"nodes":{"spore":{"locked":{"rev":"abc123"}}}}`

func TestHostScopeExcludesDevShellAndServiceTools(t *testing.T) {
	binRoot := t.TempDir()
	writeVersionBin(t, binRoot, "spore", "spore 0.6.0")
	versions := `{
		"inputs":{"spore":{"rev":"abc123"}},
		"audit":{"tools":{
			"host":{"desktop-host":{
				"opencode":{"bin":"opencode","expected":"1.14.29"},
				"spore":{"bin":"spore","expected":"0.6.0"}
			}},
			"devShell":{
				"go":{"bin":"go","expected":"1.26.2"},
				"python":{"bin":"python3","expected":"3.13.12"}
			},
			"services":{"service-cluster":{
				"service-helper":{"bin":"service-helper","expected":"0.1.0"},
				"vault-writer":{"bin":"vault-writer","expected":"0.1.0"}
			}}
		}}
	}`

	var out bytes.Buffer
	code, err := Run(Config{
		BinRoot:      binRoot,
		Host:         "desktop-host",
		VersionsJSON: []byte(versions),
		LockJSON:     []byte(testLockJSON),
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out.String())
	}
	got := out.String()
	for _, absent := range []string{"name=go", "name=python", "name=service-helper", "name=vault-writer"} {
		if strings.Contains(got, absent) {
			t.Fatalf("host audit included out-of-scope tool %s:\n%s", absent, got)
		}
	}
	if !strings.Contains(got, "missing name=opencode") {
		t.Fatalf("host audit did not check desktop host tool:\n%s", got)
	}
	if !strings.Contains(got, "ok name=spore") {
		t.Fatalf("host audit did not check present host tool:\n%s", got)
	}
}

func TestDevShellScopeUsesPath(t *testing.T) {
	pathDir := t.TempDir()
	writeVersionBin(t, pathDir, "go", "go version go1.26.2 linux/amd64")
	writeVersionBin(t, pathDir, "python3", "Python 3.13.12")
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", pathDir+string(os.PathListSeparator)+oldPath)
	versions := `{
		"inputs":{"spore":{"rev":"abc123"}},
		"audit":{"tools":{
			"host":{"desktop-host":{}},
			"devShell":{
				"go":{"bin":"go","expected":"1.26.2"},
				"python":{"bin":"python3","expected":"3.13.12"}
			}
		}}
	}`

	var out bytes.Buffer
	code, err := Run(Config{
		BinRoot:      t.TempDir(),
		Host:         "desktop-host",
		CheckDev:     true,
		VersionsJSON: []byte(versions),
		LockJSON:     []byte(testLockJSON),
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("code=%d\n%s", code, out.String())
	}
	got := out.String()
	for _, want := range []string{"devshell-ok name=go", "devshell-ok name=python"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
}

func writeVersionBin(t *testing.T, dir, name, output string) {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\necho " + shellQuote(output) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
