package fleet

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestDeriveLintName(t *testing.T) {
	cases := map[string]string{
		"harness/lint-codex-effort-high-only.sh":  "codex-effort-high-only",
		"harness/check-task-scheduler-context.sh": "task-scheduler-context",
		"harness/block-something.sh":              "something",
		"harness/lint-no-new-bash/run":            "",
		"harness/lint-no-new-bash":                "no-new-bash",
		"harness/check-vulns.sh":                  "vulns",
		"harness/hooks/commit-msg":                "",
		"harness/audit-versions.sh":               "",
		"":                                        "",
	}
	for in, want := range cases {
		got := deriveLintName(in)
		if got != want {
			t.Errorf("deriveLintName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadAllowlist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allow.txt")
	content := "# comment\n\nharness/a.sh\n  harness/b.sh  \n# another\nharness/c.sh\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readAllowlist(path)
	if err != nil {
		t.Fatalf("readAllowlist: %v", err)
	}
	want := []string{"harness/a.sh", "harness/b.sh", "harness/c.sh"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestScanCutoverMintedKeys(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"one.md": "---\nslug: one\nscheduler_key: cutover-automint:foo\n---\nbody\n",
		// non-cutover prefix: ignored
		"two.md": "---\nscheduler_key: bash-mig:harness/x.sh\n---\n",
		// no scheduler_key: ignored
		"three.md": "---\nslug: three\n---\nbody\n",
		// cutover key with whitespace
		"four.md": "---\nscheduler_key:   cutover-automint:bar  \n---\n",
		// not markdown
		"skip.txt": "scheduler_key: cutover-automint:nope\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanCutoverMintedKeys(dir)
	if err != nil {
		t.Fatalf("scanCutoverMintedKeys: %v", err)
	}
	want := map[string]bool{
		"cutover-automint:foo": true,
		"cutover-automint:bar": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestScanCutoverMintedKeys_MissingDir(t *testing.T) {
	got, err := scanCutoverMintedKeys(filepath.Join(t.TempDir(), "no-such"))
	if err != nil {
		t.Fatalf("missing dir should be nil error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty set, got %v", got)
	}
}

type mintCall struct {
	spec MintSpec
}

func newFakeMint() (*[]mintCall, func(MintSpec, io.Writer, io.Writer) error) {
	calls := &[]mintCall{}
	return calls, func(spec MintSpec, _, _ io.Writer) error {
		*calls = append(*calls, mintCall{spec: spec})
		return nil
	}
}

func reasonsByFile(skipped []SkipReason) map[string]string {
	out := map[string]string{}
	for _, s := range skipped {
		out[s.BashFile] = s.Reason
	}
	return out
}

func TestAutoMintCutover_EmptyAllowlist(t *testing.T) {
	calls, fake := newFakeMint()
	res, err := AutoMintCutover(AutoMintCutoverConfig{
		AllowlistPath: "/dev/null", // present in caller; ReadAllowlist seam overrides
		ReadAllowlist: func(string) ([]string, error) { return nil, nil },
		ScanMinted:    func(string) (map[string]bool, error) { return map[string]bool{}, nil },
		ShippedLints:  func() map[string]bool { return map[string]bool{} },
		Mint:          fake,
	})
	if err != nil {
		t.Fatalf("AutoMintCutover: %v", err)
	}
	if len(res.Minted) != 0 || len(*calls) != 0 || len(res.Skipped) != 0 {
		t.Errorf("expected empty result, got minted=%v skipped=%v calls=%v", res.Minted, res.Skipped, *calls)
	}
}

func TestAutoMintCutover_SkipsLintNotShipped(t *testing.T) {
	calls, fake := newFakeMint()
	res, err := AutoMintCutover(AutoMintCutoverConfig{
		AllowlistPath: "x",
		ReadAllowlist: func(string) ([]string, error) {
			return []string{"harness/lint-not-real.sh"}, nil
		},
		ScanMinted:   func(string) (map[string]bool, error) { return map[string]bool{}, nil },
		ShippedLints: func() map[string]bool { return map[string]bool{"some-other": true} },
		Mint:         fake,
	})
	if err != nil {
		t.Fatalf("AutoMintCutover: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("no mint expected, got %v", *calls)
	}
	r := reasonsByFile(res.Skipped)
	if r["harness/lint-not-real.sh"] != "lint-not-shipped" {
		t.Errorf("expected lint-not-shipped skip, got %v", r)
	}
}

func TestAutoMintCutover_SkipsExistingCutoverFile(t *testing.T) {
	calls, fake := newFakeMint()
	res, err := AutoMintCutover(AutoMintCutoverConfig{
		AllowlistPath: "x",
		ReadAllowlist: func(string) ([]string, error) {
			return []string{"harness/lint-foo.sh"}, nil
		},
		ScanMinted:           func(string) (map[string]bool, error) { return map[string]bool{}, nil },
		ScanExistingCutovers: func(string) (map[string]bool, error) { return map[string]bool{"foo": true}, nil },
		ShippedLints:         func() map[string]bool { return map[string]bool{"foo": true} },
		Mint:                 fake,
	})
	if err != nil {
		t.Fatalf("AutoMintCutover: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("no mint expected when consume-spore-lint-foo.md exists, got %v", *calls)
	}
	if reasonsByFile(res.Skipped)["harness/lint-foo.sh"] != "existing-cutover-task" {
		t.Errorf("expected existing-cutover-task skip, got %v", res.Skipped)
	}
}

func TestScanExistingCutoverFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"consume-spore-lint-agenix.md",
		"consume-spore-lint-task-status.md",
		"consume-spore-lints-cutover.md", // plural: ignored
		"consume-spore-lint-.md",         // empty stem: ignored
		"other-task.md",
		"consume-spore-lint-foo.txt", // wrong ext
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanExistingCutoverFiles(dir)
	if err != nil {
		t.Fatalf("scanExistingCutoverFiles: %v", err)
	}
	want := map[string]bool{"agenix": true, "task-status": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAutoMintCutover_SkipsAlreadyMinted(t *testing.T) {
	calls, fake := newFakeMint()
	res, err := AutoMintCutover(AutoMintCutoverConfig{
		AllowlistPath: "x",
		ReadAllowlist: func(string) ([]string, error) {
			return []string{"harness/lint-foo.sh"}, nil
		},
		ScanMinted: func(string) (map[string]bool, error) {
			return map[string]bool{"cutover-automint:foo": true}, nil
		},
		ShippedLints: func() map[string]bool { return map[string]bool{"foo": true} },
		Mint:         fake,
	})
	if err != nil {
		t.Fatalf("AutoMintCutover: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("no mint expected, got %v", *calls)
	}
	if reasonsByFile(res.Skipped)["harness/lint-foo.sh"] != "already-minted" {
		t.Errorf("expected already-minted skip, got %v", res.Skipped)
	}
}

func TestAutoMintCutover_DupInTick(t *testing.T) {
	calls, fake := newFakeMint()
	res, err := AutoMintCutover(AutoMintCutoverConfig{
		AllowlistPath: "x",
		ReadAllowlist: func(string) ([]string, error) {
			return []string{"harness/lint-foo.sh", "harness/check-foo.sh"}, nil
		},
		ScanMinted:   func(string) (map[string]bool, error) { return map[string]bool{}, nil },
		ShippedLints: func() map[string]bool { return map[string]bool{"foo": true} },
		Mint:         fake,
	})
	if err != nil {
		t.Fatalf("AutoMintCutover: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected exactly one mint, got %v", *calls)
	}
	if (*calls)[0].spec.SchedulerKey != "cutover-automint:foo" {
		t.Errorf("unexpected scheduler key: %s", (*calls)[0].spec.SchedulerKey)
	}
	if reasonsByFile(res.Skipped)["harness/check-foo.sh"] != "dup-in-tick" {
		t.Errorf("expected dup-in-tick skip for second row, got %v", res.Skipped)
	}
}

func TestAutoMintCutover_MaxMintsCap(t *testing.T) {
	calls, fake := newFakeMint()
	res, err := AutoMintCutover(AutoMintCutoverConfig{
		AllowlistPath: "x",
		MaxMints:      2,
		ReadAllowlist: func(string) ([]string, error) {
			return []string{"harness/lint-a.sh", "harness/lint-b.sh", "harness/lint-c.sh"}, nil
		},
		ScanMinted: func(string) (map[string]bool, error) { return map[string]bool{}, nil },
		ShippedLints: func() map[string]bool {
			return map[string]bool{"a": true, "b": true, "c": true}
		},
		Mint: fake,
	})
	if err != nil {
		t.Fatalf("AutoMintCutover: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected 2 mints (cap), got %d", len(*calls))
	}
	got := []string{(*calls)[0].spec.LintName, (*calls)[1].spec.LintName}
	sort.Strings(got)
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (allowlist order)", got, want)
	}
	if reasonsByFile(res.Skipped)["harness/lint-c.sh"] != "max-mints-cap" {
		t.Errorf("expected max-mints-cap skip for row c, got %v", res.Skipped)
	}
}

func TestAutoMintCutover_UnparseableBasename(t *testing.T) {
	calls, fake := newFakeMint()
	res, err := AutoMintCutover(AutoMintCutoverConfig{
		AllowlistPath: "x",
		ReadAllowlist: func(string) ([]string, error) {
			return []string{"harness/audit-versions.sh"}, nil
		},
		ScanMinted:   func(string) (map[string]bool, error) { return map[string]bool{}, nil },
		ShippedLints: func() map[string]bool { return map[string]bool{} },
		Mint:         fake,
	})
	if err != nil {
		t.Fatalf("AutoMintCutover: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("no mint expected for unparseable row, got %v", *calls)
	}
	if reasonsByFile(res.Skipped)["harness/audit-versions.sh"] != "unparseable-basename" {
		t.Errorf("expected unparseable-basename skip, got %v", res.Skipped)
	}
}

func TestAutoMintCutover_MintErrorRecorded(t *testing.T) {
	want := errors.New("boom")
	calls := 0
	res, err := AutoMintCutover(AutoMintCutoverConfig{
		AllowlistPath: "x",
		ReadAllowlist: func(string) ([]string, error) {
			return []string{"harness/lint-a.sh", "harness/lint-b.sh", "harness/lint-c.sh"}, nil
		},
		ScanMinted: func(string) (map[string]bool, error) { return map[string]bool{}, nil },
		ShippedLints: func() map[string]bool {
			return map[string]bool{"a": true, "b": true, "c": true}
		},
		Mint: func(spec MintSpec, _, _ io.Writer) error {
			calls++
			if spec.LintName == "b" {
				return want
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("AutoMintCutover: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 mint attempts, got %d", calls)
	}
	if len(res.Minted) != 2 {
		t.Errorf("expected 2 successful mints, got %v", res.Minted)
	}
	if len(res.Errors) != 1 || !errors.Is(res.Errors[0], want) {
		t.Errorf("expected one wrapped boom error, got %v", res.Errors)
	}
}

func TestAutoMintCutover_DryRun(t *testing.T) {
	calls, fake := newFakeMint()
	stdout := &bytes.Buffer{}
	res, err := AutoMintCutover(AutoMintCutoverConfig{
		AllowlistPath: "x",
		DryRun:        true,
		ReadAllowlist: func(string) ([]string, error) {
			return []string{"harness/lint-a.sh"}, nil
		},
		ScanMinted:   func(string) (map[string]bool, error) { return map[string]bool{}, nil },
		ShippedLints: func() map[string]bool { return map[string]bool{"a": true} },
		Mint:         fake,
		Stdout:       stdout,
	})
	if err != nil {
		t.Fatalf("AutoMintCutover: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("no real mint expected under DryRun, got %v", *calls)
	}
	if len(res.Minted) != 1 {
		t.Errorf("expected 1 counted mint under DryRun, got %v", res.Minted)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("DRY mint")) {
		t.Errorf("expected DRY mint log line, got: %s", stdout.String())
	}
}

func TestAutoMintCutover_DefaultsResolved(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "harness"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "harness", "bash-migration-allowlist.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := AutoMintCutover(AutoMintCutoverConfig{
		Repo:         repo,
		ShippedLints: func() map[string]bool { return map[string]bool{} },
		Mint:         func(MintSpec, io.Writer, io.Writer) error { return nil },
	})
	if err != nil {
		t.Fatalf("unexpected error with defaulted paths: %v", err)
	}
}

func TestBriefForCutover_MentionsKeyPieces(t *testing.T) {
	title, body := briefForCutover("task-scheduler-context", "harness/check-task-scheduler-context.sh")
	for _, want := range []string{
		"consume-spore-lint-task-scheduler-context",
		"harness/check-task-scheduler-context.sh",
		"spore lint task-scheduler-context",
		"cutover-automint:task-scheduler-context",
	} {
		if !bytes.Contains([]byte(title+body), []byte(want)) {
			t.Errorf("brief missing %q\ntitle=%s\nbody=%s", want, title, body)
		}
	}
}
