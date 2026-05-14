package scout

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLedger(t *testing.T, dir string, rows []Finding) string {
	t.Helper()
	path := filepath.Join(dir, "scout-findings.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create ledger: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	return path
}

func basicOpts(t *testing.T, ledger string) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{
		LedgerPath:   ledger,
		TasksDir:     filepath.Join(dir, "tasks"),
		Project:      "spore",
		MintedPath:   filepath.Join(dir, "minted.tsv"),
		FalseposPath: filepath.Join(dir, "falsepos.tsv"),
		Max:          10,
		Now:          func() time.Time { return time.Unix(1700000000, 0).UTC() },
	}
}

func TestMintCreatesOneTaskPerCluster(t *testing.T) {
	tmp := t.TempDir()
	rows := []Finding{
		{Ts: "2026-05-14T09:00:00Z", Lint: "emdash", Path: "rules/a.md", Line: 1, Message: "em", Fingerprint: "v1:aaa"},
		{Ts: "2026-05-14T09:00:00Z", Lint: "emdash", Path: "rules/b.md", Line: 2, Message: "em", Fingerprint: "v1:bbb"},
		{Ts: "2026-05-14T09:00:00Z", Lint: "filesize", Path: "rules/a.md", Line: 0, Message: "too big", Fingerprint: "v1:ccc"},
	}
	ledger := writeLedger(t, tmp, rows)
	opts := basicOpts(t, ledger)

	res, err := Mint(opts)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if len(res.Minted) != 2 {
		t.Fatalf("expected 2 healers (emdash+rules, filesize+rules), got %d (%v)", len(res.Minted), res.Minted)
	}
	for _, slug := range res.Minted {
		path := filepath.Join(opts.TasksDir, slug+".md")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("brief missing: %v", err)
		}
		if !strings.Contains(string(body), "scheduler_key: scout:") {
			t.Fatalf("brief missing scheduler_key: %s", body)
		}
		if !strings.Contains(string(body), "source: scout") {
			t.Fatalf("brief missing source: scout: %s", body)
		}
	}
}

func TestMintIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	rows := []Finding{
		{Ts: "2026-05-14T09:00:00Z", Lint: "emdash", Path: "rules/a.md", Line: 1, Message: "em", Fingerprint: "v1:aaa"},
	}
	ledger := writeLedger(t, tmp, rows)
	opts := basicOpts(t, ledger)

	first, err := Mint(opts)
	if err != nil {
		t.Fatalf("first Mint: %v", err)
	}
	if len(first.Minted) != 1 {
		t.Fatalf("first run should mint 1, got %d", len(first.Minted))
	}
	second, err := Mint(opts)
	if err != nil {
		t.Fatalf("second Mint: %v", err)
	}
	if len(second.Minted) != 0 {
		t.Fatalf("second run should mint 0 (dedup via minted.tsv), got %d", len(second.Minted))
	}
	if second.Skipped != 1 {
		t.Fatalf("second run should report skipped=1, got %d", second.Skipped)
	}
}

func TestMintHonorsFalsepos(t *testing.T) {
	tmp := t.TempDir()
	rows := []Finding{
		{Ts: "2026-05-14T09:00:00Z", Lint: "emdash", Path: "rules/a.md", Line: 1, Message: "em", Fingerprint: "v1:aaa"},
		{Ts: "2026-05-14T09:00:00Z", Lint: "emdash", Path: "rules/b.md", Line: 2, Message: "em", Fingerprint: "v1:bbb"},
	}
	ledger := writeLedger(t, tmp, rows)
	opts := basicOpts(t, ledger)
	if err := os.MkdirAll(filepath.Dir(opts.FalseposPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(opts.FalseposPath, []byte("v1:aaa\n# comment line\n"), 0o644); err != nil {
		t.Fatalf("write falsepos: %v", err)
	}

	res, err := Mint(opts)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if res.FalsePos != 1 {
		t.Fatalf("expected FalsePos=1, got %d", res.FalsePos)
	}
	if len(res.Minted) != 1 {
		t.Fatalf("expected 1 healer minted (the non-falsepos), got %d", len(res.Minted))
	}
}

func TestMintCapsAtMax(t *testing.T) {
	tmp := t.TempDir()
	var rows []Finding
	for i := 0; i < 5; i++ {
		rows = append(rows, Finding{
			Ts:          "2026-05-14T09:00:00Z",
			Lint:        "emdash",
			Path:        fmt.Sprintf("dir%d/x.md", i),
			Line:        1,
			Message:     "em",
			Fingerprint: fmt.Sprintf("v1:%02d", i),
		})
	}
	ledger := writeLedger(t, tmp, rows)
	opts := basicOpts(t, ledger)
	opts.Max = 2

	res, err := Mint(opts)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if len(res.Minted) != 2 {
		t.Fatalf("expected 2 minted (capped), got %d", len(res.Minted))
	}
	if res.Capped != 3 {
		t.Fatalf("expected Capped=3, got %d", res.Capped)
	}
}

func TestMintEmptyLedgerNoOp(t *testing.T) {
	tmp := t.TempDir()
	opts := basicOpts(t, filepath.Join(tmp, "nonexistent.jsonl"))
	res, err := Mint(opts)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if len(res.Minted) != 0 {
		t.Fatalf("expected 0 healers on missing ledger, got %d", len(res.Minted))
	}
}

func TestMintDeduplicatesWithinCluster(t *testing.T) {
	tmp := t.TempDir()
	rows := []Finding{
		{Ts: "2026-05-14T09:00:00Z", Lint: "emdash", Path: "rules/a.md", Line: 1, Message: "em", Fingerprint: "v1:aaa"},
		{Ts: "2026-05-14T10:00:00Z", Lint: "emdash", Path: "rules/a.md", Line: 1, Message: "em", Fingerprint: "v1:aaa"},
		{Ts: "2026-05-14T10:00:00Z", Lint: "emdash", Path: "rules/a.md", Line: 5, Message: "em", Fingerprint: "v1:bbb"},
	}
	ledger := writeLedger(t, tmp, rows)
	opts := basicOpts(t, ledger)

	res, err := Mint(opts)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if len(res.Minted) != 1 {
		t.Fatalf("expected 1 healer, got %d", len(res.Minted))
	}
	body, err := os.ReadFile(filepath.Join(opts.TasksDir, res.Minted[0]+".md"))
	if err != nil {
		t.Fatalf("read brief: %v", err)
	}
	// Two unique fingerprints should both appear; the dup is collapsed.
	if !strings.Contains(string(body), "v1:aaa") || !strings.Contains(string(body), "v1:bbb") {
		t.Fatalf("brief missing fingerprints: %s", body)
	}
	if strings.Count(string(body), "v1:aaa") != 1 {
		t.Fatalf("v1:aaa should appear exactly once in fingerprint list:\n%s", body)
	}
}

func TestMintDryRunSkipsWriteAndLedger(t *testing.T) {
	tmp := t.TempDir()
	rows := []Finding{
		{Ts: "2026-05-14T09:00:00Z", Lint: "emdash", Path: "rules/a.md", Line: 1, Message: "em", Fingerprint: "v1:aaa"},
	}
	ledger := writeLedger(t, tmp, rows)
	opts := basicOpts(t, ledger)
	opts.DryRun = true

	res, err := Mint(opts)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if len(res.Minted) != 1 {
		t.Fatalf("dry-run should still report 1 would-mint, got %d", len(res.Minted))
	}
	if _, err := os.Stat(filepath.Join(opts.TasksDir, res.Minted[0]+".md")); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write task brief")
	}
	if _, err := os.Stat(opts.MintedPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write minted.tsv")
	}
}
