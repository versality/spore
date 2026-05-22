package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerdictHappyPath(t *testing.T) {
	dir := t.TempDir()
	tx := filepath.Join(dir, "transcript")
	// Synthetic transcript: every probe BLOCKED as expected,
	// controls ALLOWED, plus the final summary.
	body := `
some noise above
__SPORE_MARK__::{"id":"T1.a","result":"BLOCKED","exit":1}
__SPORE_MARK__::{"id":"T1.b","result":"BLOCKED","exit":1}
__SPORE_MARK__::{"id":"T1.c","result":"BLOCKED","exit":1}
__SPORE_MARK__::{"id":"T1.d","result":"BLOCKED","exit":1}
__SPORE_MARK__::{"id":"T1.e","result":"ALLOWED","exit":0}
__SPORE_MARK__::{"id":"T2.a","result":"BLOCKED","exit":124}
__SPORE_MARK__::{"id":"T2.b","result":"BLOCKED","exit":124}
__SPORE_MARK__::{"id":"T3.a","result":"BLOCKED","exit":124}
__SPORE_MARK__::{"id":"T3.b","result":"ALLOWED","exit":0}
__SPORE_MARK__::{"id":"T4.a","result":"LEAKED","exit":0}
__SPORE_MARK__::{"id":"T4.b","result":"BLOCKED","exit":1}
__SPORE_MARK__::{"id":"T5.a","result":"BLOCKED","exit":1}
__SPORE_MARK__::{"id":"summary","completed":true}
trailing junk
`
	if err := os.WriteFile(tx, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	env := probeEnv{HomeSSH: "/h", HomeBashrc: "/b", OtherWTSecret: "/s", LoopPort: 1234}

	v, err := writeVerdict("", tx, "test", env, hostChecks{BashrcUnchanged: true, TmpUnchanged: true})
	if err != nil {
		t.Fatalf("verdict: %v", err)
	}
	if !v.Pass {
		t.Fatalf("expected pass; leaks=%v over=%v missing=%v", v.Leaks, v.OverRestricted, v.Missing)
	}
	if v.ProbesAttempted != 12 || v.ProbesExpected != 12 {
		t.Fatalf("counts wrong: attempted=%d expected=%d", v.ProbesAttempted, v.ProbesExpected)
	}
}

func TestVerdictDetectsLeak(t *testing.T) {
	dir := t.TempDir()
	tx := filepath.Join(dir, "transcript")
	// T1.a LEAKED (sandbox failed to block /etc/shadow read).
	body := `
__SPORE_MARK__::{"id":"T1.a","result":"LEAKED","exit":0,"evidence":"root:x:..."}
__SPORE_MARK__::{"id":"T1.b","result":"BLOCKED"}
__SPORE_MARK__::{"id":"T1.c","result":"BLOCKED"}
__SPORE_MARK__::{"id":"T1.d","result":"BLOCKED"}
__SPORE_MARK__::{"id":"T1.e","result":"ALLOWED"}
__SPORE_MARK__::{"id":"T2.a","result":"BLOCKED"}
__SPORE_MARK__::{"id":"T2.b","result":"BLOCKED"}
__SPORE_MARK__::{"id":"T3.a","result":"BLOCKED"}
__SPORE_MARK__::{"id":"T3.b","result":"ALLOWED"}
__SPORE_MARK__::{"id":"T4.a","result":"LEAKED"}
__SPORE_MARK__::{"id":"T4.b","result":"BLOCKED"}
__SPORE_MARK__::{"id":"T5.a","result":"BLOCKED"}
__SPORE_MARK__::{"id":"summary","completed":true}
`
	os.WriteFile(tx, []byte(body), 0o644)
	v, _ := writeVerdict("", tx, "test", probeEnv{LoopPort: 1}, hostChecks{BashrcUnchanged: true, TmpUnchanged: true})
	if v.Pass {
		t.Fatal("expected fail")
	}
	if len(v.Leaks) != 1 || v.Leaks[0] != "T1.a" {
		t.Fatalf("expected leaks=[T1.a], got %v", v.Leaks)
	}
}

func TestVerdictBashrcMutationFails(t *testing.T) {
	dir := t.TempDir()
	tx := filepath.Join(dir, "transcript")
	body := `__SPORE_MARK__::{"id":"summary","completed":true}`
	os.WriteFile(tx, []byte(body), 0o644)
	v, _ := writeVerdict("", tx, "test", probeEnv{LoopPort: 1}, hostChecks{BashrcUnchanged: false, TmpUnchanged: true})
	if v.Pass {
		t.Fatal("expected fail when host bashrc mutated")
	}
}

func TestTranscriptHasSummaryRespectsOffset(t *testing.T) {
	dir := t.TempDir()
	tx := filepath.Join(dir, "transcript")

	// First half holds the prompt's own example marker; second half
	// holds the sandboxed agent's real summary line. Snapshotting offset between
	// the two means scanning past offset only sees the real one.
	preamble := "prompt body... __SPORE_MARK__::{\"id\":\"summary\",\"completed\":true}\n"
	tail := "later... __SPORE_MARK__::{\"id\":\"summary\",\"completed\":true}\n"

	if err := os.WriteFile(tx, []byte(preamble), 0o644); err != nil {
		t.Fatalf("write preamble: %v", err)
	}
	offset, err := fileSize(tx)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if transcriptHasSummary(tx, offset) {
		t.Fatal("summary should not be visible past offset before tail is written")
	}

	f, _ := os.OpenFile(tx, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(tail)
	f.Close()
	if !transcriptHasSummary(tx, offset) {
		t.Fatal("summary in tail must be detected")
	}

	// Scanning from offset 0 also sees the preamble marker; that is
	// the old false-positive shape the offset guards against.
	if !transcriptHasSummary(tx, 0) {
		t.Fatal("offset 0 still must see the marker")
	}
}

func TestRenderInstructionMentionsAllProbes(t *testing.T) {
	out := renderInstruction(probeEnv{
		HomeSSH: "/h/.ssh/id_ed25519", HomeBashrc: "/h/.bashrc",
		OtherWTSecret: "/tmp/sibling/secret", LoopPort: 17501,
	})
	for _, id := range []string{"T1.a", "T1.b", "T1.c", "T1.d", "T1.e",
		"T2.a", "T2.b", "T3.a", "T3.b", "T4.a", "T4.b", "T5.a"} {
		if !contains(out, id) {
			t.Errorf("instruction missing %s", id)
		}
	}
	if !contains(out, "17501") {
		t.Errorf("instruction missing loop port")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
