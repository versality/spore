package evidence

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRequired_StringInlineList(t *testing.T) {
	meta := map[string]any{
		"evidence_required": "[commit, test, file]",
	}
	got := Required(meta)
	want := []string{"commit", "test", "file"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRequired_StringScalar(t *testing.T) {
	meta := map[string]any{"evidence_required": "commit"}
	got := Required(meta)
	want := []string{"commit"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRequired_EmptyBracketed(t *testing.T) {
	meta := map[string]any{"evidence_required": "[]"}
	if got := Required(meta); got != nil {
		t.Fatalf("got %v want nil", got)
	}
}

func TestRequired_StringSlice(t *testing.T) {
	meta := map[string]any{
		"evidence_required": []string{"commit", " test ", "", "file"},
	}
	got := Required(meta)
	want := []string{"commit", "test", "file"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRequired_AnySlice(t *testing.T) {
	meta := map[string]any{
		"evidence_required": []any{"commit", 7, "test"},
	}
	got := Required(meta)
	want := []string{"commit", "test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestRequired_Absent(t *testing.T) {
	if got := Required(map[string]any{}); got != nil {
		t.Fatalf("got %v want nil", got)
	}
}

func TestParse_FindsBullets(t *testing.T) {
	body := `# brief

prose

## Evidence

- commit: a1b2c3d shipped the parser
- file: evidence/parse.go contains ParseEvidence
- bogus: not a kind
- test: evidence/parse_test.go covers the parser

## Other section
- commit: ignored-in-other-section
`
	got, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Item{
		{Kind: "commit", Rest: "a1b2c3d shipped the parser"},
		{Kind: "file", Rest: "evidence/parse.go contains ParseEvidence"},
		{Kind: "test", Rest: "evidence/parse_test.go covers the parser"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestParse_NoSection(t *testing.T) {
	got, err := Parse("just prose, no heading\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v want nil", got)
	}
}

func TestParse_HeadingCaseInsensitive(t *testing.T) {
	body := "## evidence\n- commit: a1b2c3d4 ok\n"
	got, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 || got[0].Kind != "commit" {
		t.Fatalf("got %v want one commit bullet", got)
	}
}

func TestVerify_RealImpl(t *testing.T) {
	meta := map[string]any{"evidence_required": "[commit, file, test]"}
	body := `## Evidence
- commit: a1b2c3d4 shipped the package
- file: evidence/verify.go contains Verify
- test: evidence/evidence_test.go covers all 6 verdicts
`
	v, _ := Verify(meta, body)
	if v != RealImpl {
		t.Fatalf("got %s want %s", v, RealImpl)
	}
}

func TestVerify_RationalClose(t *testing.T) {
	meta := map[string]any{"evidence_required": "[commit, doc-link]"}
	body := `Closing this without a code change: spec was wrong, abandon.

## Evidence
- commit: N/A: spec rejected, no code change needed
- doc-link: docs/todo/abandoned.md notes the rejection
`
	v, diags := Verify(meta, body)
	if v != RationalClose {
		t.Fatalf("got %s want %s (diags: %v)", v, RationalClose, diags)
	}
}

func TestVerify_CrossRepo(t *testing.T) {
	meta := map[string]any{"evidence_required": "[commit]"}
	body := `## Evidence
- commit: sibling-repo:abc1234 work landed in sibling repo
`
	v, _ := Verify(meta, body)
	if v != CrossRepo {
		t.Fatalf("got %s want %s", v, CrossRepo)
	}
}

func TestVerify_SuspectHallucination(t *testing.T) {
	meta := map[string]any{"evidence_required": "[commit, test]"}
	body := `Implemented the parser, all green, tests pass.
The change shipped on main.

## Evidence
- file: evidence/parse.go updated
`
	v, diags := Verify(meta, body)
	if v != SuspectHallucination {
		t.Fatalf("got %s want %s (diags: %v)", v, SuspectHallucination, diags)
	}
	if !containsLine(diags, "missing required: commit, test") {
		t.Errorf("expected missing diag, got %v", diags)
	}
}

func TestVerify_BogusEvidence_EmptyRest(t *testing.T) {
	meta := map[string]any{"evidence_required": "[commit]"}
	body := `## Evidence
- commit:
`
	v, _ := Verify(meta, body)
	if v != BogusEvidence {
		t.Fatalf("got %s want %s", v, BogusEvidence)
	}
}

func TestVerify_BogusEvidence_WrongShape(t *testing.T) {
	meta := map[string]any{"evidence_required": "[commit]"}
	body := `## Evidence
- commit: hello world this is not a sha
`
	v, _ := Verify(meta, body)
	if v != BogusEvidence {
		t.Fatalf("got %s want %s", v, BogusEvidence)
	}
}

func TestVerify_Unknown_NoContract(t *testing.T) {
	v, diags := Verify(map[string]any{}, "some body\n")
	if v != Unknown {
		t.Fatalf("got %s want %s", v, Unknown)
	}
	if !containsLine(diags, "no evidence_required declared") {
		t.Errorf("expected diag, got %v", diags)
	}
}

func TestVerify_Unknown_AmbiguousMissing(t *testing.T) {
	meta := map[string]any{"evidence_required": "[commit, test]"}
	// Body is missing both bullets and contains no completion claim.
	body := `Quick note: still figuring out the shape.

## Evidence
`
	v, _ := Verify(meta, body)
	if v != Unknown {
		t.Fatalf("got %s want %s", v, Unknown)
	}
}

func TestBlocks(t *testing.T) {
	cases := map[Verdict]bool{
		RealImpl:             false,
		RationalClose:        false,
		CrossRepo:            false,
		SuspectHallucination: true,
		BogusEvidence:        true,
		Unknown:              true,
	}
	for v, want := range cases {
		if got := Blocks(v); got != want {
			t.Errorf("Blocks(%s) = %v want %v", v, got, want)
		}
	}
}

func TestInSoakWindow(t *testing.T) {
	if !InSoakWindow(ContractStart) {
		t.Error("ContractStart should be in soak window")
	}
	if !InSoakWindow(ContractStart.Add(SoakWindow - time.Second)) {
		t.Error("just before window end should be in soak")
	}
	if InSoakWindow(ContractStart.Add(SoakWindow)) {
		t.Error("exactly at window end should be out of soak")
	}
}

func TestIsKind(t *testing.T) {
	for _, k := range Kinds {
		if !IsKind(k) {
			t.Errorf("IsKind(%q) = false", k)
		}
	}
	if IsKind("not-a-kind") {
		t.Error("IsKind(\"not-a-kind\") = true")
	}
}

func containsLine(diags []string, want string) bool {
	for _, d := range diags {
		if strings.Contains(d, want) {
			return true
		}
	}
	return false
}
