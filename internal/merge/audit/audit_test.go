package audit

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// fakeGit implements Git over in-memory state. Each path has a HEAD,
// index, and worktree blob; LogScan returns the canned commit chain.
type fakeGit struct {
	paths    []string // returned by ListPaths verbatim
	head     map[string]string
	index    map[string]string
	worktree map[string]string
	logs     map[string][]LogEntry
	// blobByCommit[commit][path] = blob
	blobByCommit map[string]map[string]string
	statuses     map[string]string
}

func (f fakeGit) ListPaths(_ []string) ([]string, error) { return f.paths, nil }

func (f fakeGit) BlobAtRef(ref, path string) string {
	if ref == "HEAD" {
		if v, ok := f.head[path]; ok {
			return v
		}
		return "-"
	}
	if m, ok := f.blobByCommit[ref]; ok {
		if v, ok := m[path]; ok {
			return v
		}
	}
	return "-"
}

func (f fakeGit) BlobAtIndex(path string) string {
	if v, ok := f.index[path]; ok {
		return v
	}
	return "-"
}

func (f fakeGit) BlobAtWorktree(path string) string {
	if v, ok := f.worktree[path]; ok {
		return v
	}
	return "-"
}

func (f fakeGit) LogScan(path string, _ int, _ bool) []LogEntry {
	return f.logs[path]
}

func (f fakeGit) StatusShort(path string) string { return f.statuses[path] }

func TestRun_NoDriftReportsNothing(t *testing.T) {
	g := fakeGit{
		paths:    []string{"a", "b"},
		head:     map[string]string{"a": "X", "b": "Y"},
		index:    map[string]string{"a": "X", "b": "Y"},
		worktree: map[string]string{"a": "X", "b": "Y"},
	}
	drifts, err := Run(g, Config{Pathspecs: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 0 {
		t.Errorf("expected no drift, got %v", drifts)
	}
}

func TestRun_DirtyMainProbe_WorktreeDiverges(t *testing.T) {
	g := fakeGit{
		paths:    []string{"justfile"},
		head:     map[string]string{"justfile": "H"},
		index:    map[string]string{"justfile": "H"},
		worktree: map[string]string{"justfile": "W"},
		statuses: map[string]string{"justfile": " M justfile"},
		logs: map[string][]LogEntry{
			"justfile": {
				{Commit: "c1", Meta: "abcd1234 add justfile"},
			},
		},
		blobByCommit: map[string]map[string]string{
			"c1": {"justfile": "H"},
		},
	}
	drifts, err := Run(g, Config{Pathspecs: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 1 {
		t.Fatalf("expected 1 drift, got %d", len(drifts))
	}
	d := drifts[0]
	if d.Path != "justfile" || d.HEAD != "H" || d.Index != "H" || d.Worktree != "W" {
		t.Errorf("drift = %+v", d)
	}
	if d.HEADOwner != "abcd1234 add justfile" {
		t.Errorf("HEADOwner = %q", d.HEADOwner)
	}
	if d.WorktreeOwner != "unknown" {
		t.Errorf("worktree owner unknown, got %q", d.WorktreeOwner)
	}
	if d.Status != " M justfile" {
		t.Errorf("status = %q", d.Status)
	}
}

func TestRun_MissingEvidenceReportedAsDash(t *testing.T) {
	// File exists in HEAD but is missing on disk + absent from index.
	g := fakeGit{
		paths:    []string{"docs/example-lifecycle.md"},
		head:     map[string]string{"docs/example-lifecycle.md": "B1"},
		index:    map[string]string{}, // -> "-"
		worktree: map[string]string{}, // -> "-"
		logs: map[string][]LogEntry{
			"docs/example-lifecycle.md": {{Commit: "c1", Meta: "1111 add doc"}},
		},
		blobByCommit: map[string]map[string]string{"c1": {"docs/example-lifecycle.md": "B1"}},
	}
	drifts, err := Run(g, Config{Pathspecs: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 1 {
		t.Fatalf("expected 1 drift, got %d", len(drifts))
	}
	d := drifts[0]
	if d.Index != "-" || d.Worktree != "-" {
		t.Errorf("expected '-' for missing index/worktree, got %+v", d)
	}
	if d.IndexOwner != "-" || d.WorktreeOwner != "-" {
		t.Errorf("expected '-' owners for missing blobs, got %+v", d)
	}
	if d.HEADOwner != "1111 add doc" {
		t.Errorf("HEADOwner = %q", d.HEADOwner)
	}
}

func TestRun_CommitCountMismatch_FallsBackToMergeCommit(t *testing.T) {
	// Worktree blob matches an older blob from a merge commit only;
	// no-merges scan misses, all-scan finds.
	g := fakeGit{
		paths:    []string{"x"},
		head:     map[string]string{"x": "H"},
		index:    map[string]string{"x": "H"},
		worktree: map[string]string{"x": "OLD"},
		logs: map[string][]LogEntry{
			"x": {
				{Commit: "head", Meta: "head H"},
				{Commit: "merge", Meta: "merge OLD"},
			},
		},
		blobByCommit: map[string]map[string]string{
			"head":  {"x": "H"},
			"merge": {"x": "OLD"},
		},
	}
	// Adapter that returns nothing in no-merges pass and the full
	// chain in the all pass; verifies the two-pass fallback in
	// ownerForBlob.
	twoPass := twoPassGit{inner: g}
	drifts, err := Run(twoPass, Config{Pathspecs: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	if len(drifts) != 1 {
		t.Fatalf("expected 1 drift, got %d", len(drifts))
	}
	if drifts[0].WorktreeOwner != "merge OLD" {
		t.Errorf("WorktreeOwner = %q, want merge OLD", drifts[0].WorktreeOwner)
	}
}

type twoPassGit struct{ inner fakeGit }

func (t twoPassGit) ListPaths(p []string) ([]string, error) { return t.inner.ListPaths(p) }
func (t twoPassGit) BlobAtRef(ref, path string) string      { return t.inner.BlobAtRef(ref, path) }
func (t twoPassGit) BlobAtIndex(path string) string         { return t.inner.BlobAtIndex(path) }
func (t twoPassGit) BlobAtWorktree(path string) string      { return t.inner.BlobAtWorktree(path) }
func (t twoPassGit) StatusShort(path string) string         { return t.inner.StatusShort(path) }
func (t twoPassGit) LogScan(path string, lim int, noMerges bool) []LogEntry {
	if noMerges {
		// only the head commit, which has the H blob
		return []LogEntry{{Commit: "head", Meta: "head H"}}
	}
	return t.inner.logs[path]
}

func TestRun_DriftSortedByPath(t *testing.T) {
	g := fakeGit{
		paths:    []string{"z", "a", "m"},
		head:     map[string]string{"z": "Z", "a": "A", "m": "M"},
		index:    map[string]string{"z": "z'", "a": "a'", "m": "m'"},
		worktree: map[string]string{"z": "Z", "a": "A", "m": "M"},
	}
	drifts, err := Run(g, Config{Pathspecs: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(drifts))
	for i, d := range drifts {
		got[i] = d.Path
	}
	want := []string{"a", "m", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %v, want %v (sorted)", got, want)
	}
}

func TestFormatReport_RendersExpectedShape(t *testing.T) {
	d := Drift{
		Path: "a", HEAD: "H", Index: "I", Worktree: "W",
		HEADOwner: "abcd a", IndexOwner: "", WorktreeOwner: "wxyz w",
		Status: " M a",
	}
	var buf bytes.Buffer
	if !FormatReport(&buf, []Drift{d}) {
		t.Fatal("FormatReport must return true when drift exists")
	}
	out := buf.String()
	if !strings.Contains(out, "path=a HEAD=H index=I worktree=W") {
		t.Errorf("missing leading fields: %s", out)
	}
	if !strings.Contains(out, "indexOwner=unknown") {
		t.Errorf("empty owner should print as unknown: %s", out)
	}
	if !strings.Contains(out, "status= M a") {
		t.Errorf("status missing: %s", out)
	}
}

func TestFormatReport_EmptyDriftList(t *testing.T) {
	var buf bytes.Buffer
	if FormatReport(&buf, nil) {
		t.Fatal("expected false on empty drift list")
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output, got %q", buf.String())
	}
}

func TestRun_EmptyPathspecsIsNoOp(t *testing.T) {
	g := fakeGit{paths: nil}
	if _, err := Run(g, Config{}); err != nil {
		t.Fatal(err)
	}
}
