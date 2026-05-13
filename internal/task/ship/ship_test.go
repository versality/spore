package ship

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/gh"
	"github.com/versality/spore/internal/task/consumerclaim"
	"github.com/versality/spore/internal/task/cutover"
	"github.com/versality/spore/internal/task/frontmatter"
)

type fakeGH struct {
	prs       []gh.PRState // one per ViewPR call; last value sticks
	viewIdx   int
	viewFound bool
	viewErr   error

	createNumber int
	createErr    error

	mergeErr     error
	mergeCalled  bool
	mergeStrat   string
	mergeDelete  bool
	mergeNumber  int
	createCalled int
}

func (f *fakeGH) ViewPR(string, string) (gh.PRState, bool, error) {
	if f.viewErr != nil {
		return gh.PRState{}, false, f.viewErr
	}
	if len(f.prs) == 0 {
		return gh.PRState{}, f.viewFound, nil
	}
	pr := f.prs[f.viewIdx]
	if f.viewIdx < len(f.prs)-1 {
		f.viewIdx++
	}
	return pr, f.viewFound, nil
}

func (f *fakeGH) CreatePR(string, string, string, string, string) (int, error) {
	f.createCalled++
	return f.createNumber, f.createErr
}

func (f *fakeGH) MergePR(_ string, number int, strategy string, deleteBranch bool) error {
	f.mergeCalled = true
	f.mergeNumber = number
	f.mergeStrat = strategy
	f.mergeDelete = deleteBranch
	return f.mergeErr
}

type gitCall struct {
	args []string
}

type fakeGit struct {
	calls []gitCall
	errs  map[string]error // keyed by first arg ("push", "fetch", ...)
}

func (f *fakeGit) run(_ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, gitCall{args: append([]string(nil), args...)})
	if len(args) > 0 {
		if err, ok := f.errs[args[0]]; ok {
			return []byte("simulated stderr"), err
		}
	}
	return nil, nil
}

func (f *fakeGit) called(verb string) int {
	n := 0
	for _, c := range f.calls {
		if len(c.args) > 0 && c.args[0] == verb {
			n++
		}
	}
	return n
}

type doneCall struct {
	tasksDir, slug string
}

func newDeps(g *fakeGH, gi *fakeGit, justErr error, doneErr error) (Deps, *[]doneCall, *[]time.Duration, *bytes.Buffer) {
	out := &bytes.Buffer{}
	dones := &[]doneCall{}
	sleeps := &[]time.Duration{}
	return Deps{
		GH:           g,
		Git:          gi.run,
		RunJustCheck: func(string) error { return justErr },
		Sleep:        func(d time.Duration) { *sleeps = append(*sleeps, d) },
		Done: func(td, s string) error {
			*dones = append(*dones, doneCall{td, s})
			return doneErr
		},
		PollInterval: time.Millisecond,
		MaxPolls:     5,
		Out:          out,
		ErrOut:       out,
	}, dones, sleeps, out
}

func mergeableGreen(n int) gh.PRState {
	return gh.PRState{Number: n, State: "OPEN", Mergeable: "MERGEABLE",
		Checks: []gh.CheckRun{{Name: "Validate", Conclusion: "SUCCESS", Status: "COMPLETED"}}}
}

func TestRunHappyPath(t *testing.T) {
	g := &fakeGH{prs: []gh.PRState{mergeableGreen(7)}, viewFound: true, createNumber: 7}
	gi := &fakeGit{}
	deps, dones, sleeps, out := newDeps(g, gi, nil, nil)

	if err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if g.createCalled != 1 {
		t.Errorf("CreatePR called %d times, want 1", g.createCalled)
	}
	if !g.mergeCalled {
		t.Fatal("MergePR not called")
	}
	if g.mergeNumber != 7 || g.mergeStrat != "squash" || !g.mergeDelete {
		t.Errorf("MergePR called with (n=%d, strat=%s, delete=%v); want (7, squash, true)", g.mergeNumber, g.mergeStrat, g.mergeDelete)
	}
	if gi.called("push") != 1 || gi.called("fetch") != 1 || gi.called("merge") != 1 || gi.called("branch") != 1 {
		t.Errorf("git verbs: push=%d fetch=%d merge=%d branch=%d", gi.called("push"), gi.called("fetch"), gi.called("merge"), gi.called("branch"))
	}
	if len(*dones) != 1 || (*dones)[0] != (doneCall{"/proj/tasks", "feat"}) {
		t.Errorf("Done calls = %+v", *dones)
	}
	if len(*sleeps) != 0 {
		t.Errorf("sleeps = %v, want none", *sleeps)
	}
	if !strings.Contains(out.String(), "ship: done") {
		t.Errorf("missing done line in output:\n%s", out.String())
	}
}

func TestRunPreflightRed(t *testing.T) {
	g := &fakeGH{viewFound: true}
	gi := &fakeGit{}
	deps, dones, _, _ := newDeps(g, gi, fmt.Errorf("recipe check failed"), nil)

	err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps)
	if err == nil || !strings.Contains(err.Error(), "preflight just check failed") {
		t.Fatalf("err = %v, want preflight failure", err)
	}
	if gi.called("push") != 0 {
		t.Error("push should not have been called")
	}
	if g.createCalled != 0 {
		t.Error("CreatePR should not have been called")
	}
	if len(*dones) != 0 {
		t.Error("Done should not have been called")
	}
}

func TestRunPushFails(t *testing.T) {
	g := &fakeGH{viewFound: true, createNumber: 7}
	gi := &fakeGit{errs: map[string]error{"push": fmt.Errorf("auth denied")}}
	deps, _, _, _ := newDeps(g, gi, nil, nil)

	err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps)
	if err == nil || !strings.Contains(err.Error(), "push wt/feat") {
		t.Fatalf("err = %v, want push failure", err)
	}
	if g.createCalled != 0 {
		t.Error("CreatePR called despite push failure")
	}
}

func TestRunCIRed(t *testing.T) {
	red := gh.PRState{
		Number: 9, State: "OPEN", Mergeable: "MERGEABLE",
		Checks: []gh.CheckRun{
			{Name: "Validate", Conclusion: "FAILURE", Status: "COMPLETED", URL: "https://example/run/9"},
			{Name: "Other", Conclusion: "SUCCESS", Status: "COMPLETED"},
		},
	}
	g := &fakeGH{prs: []gh.PRState{red}, viewFound: true, createNumber: 9}
	gi := &fakeGit{}
	deps, dones, _, _ := newDeps(g, gi, nil, nil)

	err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps)
	if err == nil || !strings.Contains(err.Error(), "failing check") {
		t.Fatalf("err = %v, want CI red", err)
	}
	if !strings.Contains(err.Error(), "https://example/run/9") {
		t.Errorf("missing URL in err: %v", err)
	}
	if g.mergeCalled {
		t.Error("MergePR called despite CI red")
	}
	if len(*dones) != 0 {
		t.Error("Done called despite CI red")
	}
}

func TestRunConflicting(t *testing.T) {
	conflict := gh.PRState{Number: 9, State: "OPEN", Mergeable: "CONFLICTING"}
	g := &fakeGH{prs: []gh.PRState{conflict}, viewFound: true, createNumber: 9}
	gi := &fakeGit{}
	deps, _, _, _ := newDeps(g, gi, nil, nil)

	err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps)
	if err == nil || !strings.Contains(err.Error(), "merge conflicts") {
		t.Fatalf("err = %v, want conflict", err)
	}
}

func TestRunPendingThenGreen(t *testing.T) {
	pending := gh.PRState{Number: 9, State: "OPEN", Mergeable: "UNKNOWN",
		Checks: []gh.CheckRun{{Name: "Validate", Conclusion: "", Status: "IN_PROGRESS"}}}
	green := mergeableGreen(9)
	g := &fakeGH{prs: []gh.PRState{pending, green}, viewFound: true, createNumber: 9}
	gi := &fakeGit{}
	deps, dones, sleeps, _ := newDeps(g, gi, nil, nil)

	if err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(*sleeps) != 1 {
		t.Errorf("sleeps = %d, want 1", len(*sleeps))
	}
	if !g.mergeCalled {
		t.Error("MergePR not called")
	}
	if len(*dones) != 1 {
		t.Error("Done not called")
	}
}

func TestRunTimeout(t *testing.T) {
	pending := gh.PRState{Number: 9, State: "OPEN", Mergeable: "UNKNOWN",
		Checks: []gh.CheckRun{{Name: "Validate", Conclusion: "", Status: "IN_PROGRESS"}}}
	g := &fakeGH{prs: []gh.PRState{pending}, viewFound: true, createNumber: 9}
	gi := &fakeGit{}
	deps, _, sleeps, _ := newDeps(g, gi, nil, nil)

	err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
	if len(*sleeps) != deps.MaxPolls {
		t.Errorf("sleeps = %d, want %d", len(*sleeps), deps.MaxPolls)
	}
}

func TestRunAlreadyMerged(t *testing.T) {
	merged := gh.PRState{Number: 7, State: "MERGED"}
	g := &fakeGH{prs: []gh.PRState{merged}, viewFound: true, createNumber: 7}
	gi := &fakeGit{}
	deps, dones, _, out := newDeps(g, gi, nil, nil)

	if err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if g.mergeCalled {
		t.Error("MergePR called on already-merged PR")
	}
	if !strings.Contains(out.String(), "already merged") {
		t.Errorf("missing already-merged line:\n%s", out.String())
	}
	if len(*dones) != 1 {
		t.Error("Done not called")
	}
}

func TestRunDoneRefuses(t *testing.T) {
	g := &fakeGH{prs: []gh.PRState{mergeableGreen(7)}, viewFound: true, createNumber: 7}
	gi := &fakeGit{}
	deps, _, _, _ := newDeps(g, gi, nil, fmt.Errorf("done refused: 2 consumer claim(s) unresolved"))

	err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps)
	if err == nil || !strings.Contains(err.Error(), "consumer claim") {
		t.Fatalf("err = %v, want Done refusal surfaced", err)
	}
	if !g.mergeCalled {
		t.Error("MergePR should have been called before Done")
	}
}

func TestRunNoPRFound(t *testing.T) {
	g := &fakeGH{viewFound: false, createNumber: 0}
	gi := &fakeGit{}
	deps, _, _, _ := newDeps(g, gi, nil, nil)

	err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps)
	if err == nil || !strings.Contains(err.Error(), "no PR found") {
		t.Fatalf("err = %v, want no-PR error", err)
	}
}

func TestRunMissingSlug(t *testing.T) {
	deps := Deps{}
	if err := Run(Options{TasksDir: "/proj/tasks"}, deps); err == nil || !strings.Contains(err.Error(), "slug required") {
		t.Fatalf("err = %v, want slug required", err)
	}
}

func TestRunMissingTasksDir(t *testing.T) {
	deps := Deps{}
	if err := Run(Options{Slug: "feat"}, deps); err == nil || !strings.Contains(err.Error(), "tasksDir required") {
		t.Fatalf("err = %v, want tasksDir required", err)
	}
}

func TestRunMintsCutoverForUnresolvedClaims(t *testing.T) {
	g := &fakeGH{prs: []gh.PRState{mergeableGreen(7)}, viewFound: true, createNumber: 7}
	gi := &fakeGit{}
	deps, dones, _, out := newDeps(g, gi, nil, nil)

	deps.ReadTaskMeta = func(string, string) (frontmatter.Meta, error) {
		return frontmatter.Meta{
			ConsumerClaims: []string{
				"nix-config:path:modules/foo.sh",
				"nix-config:grep:legacyFn",
				"already-clean:path:gone.sh",
			},
		}, nil
	}
	deps.ConsumerScan = func(claims []consumerclaim.Claim) []consumerclaim.Result {
		out := make([]consumerclaim.Result, len(claims))
		for i, c := range claims {
			if c.Repo == "already-clean" {
				out[i] = consumerclaim.Result{Claim: c, Status: consumerclaim.StatusResolved}
			} else {
				out[i] = consumerclaim.Result{Claim: c, Status: consumerclaim.StatusUnresolved, Detail: "found"}
			}
		}
		return out
	}
	var minted []cutover.Options
	deps.MintCutover = func(opts cutover.Options) (cutover.Result, error) {
		minted = append(minted, opts)
		return cutover.Result{Slug: "consume-spore-" + opts.Feature, Path: "/cons/tasks/x.md"}, nil
	}
	deps.ProjectName = func(string) (string, error) { return "spore", nil }

	if err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(minted) != 2 {
		t.Fatalf("minted %d, want 2", len(minted))
	}
	for _, m := range minted {
		if m.SourceRepo != "spore" || m.SourceSlug != "feat" || m.SourcePR != 7 {
			t.Errorf("mint opts %+v: want source-repo=spore, source-slug=feat, pr=7", m)
		}
		if m.Consumer != "nix-config" {
			t.Errorf("mint Consumer = %q, want nix-config", m.Consumer)
		}
	}
	if !strings.Contains(out.String(), "minted cutover") {
		t.Errorf("missing minted-cutover line:\n%s", out.String())
	}
	if len(*dones) != 1 {
		t.Error("Done not called after mint")
	}
}

func TestRunSkipsMintWhenClaimsResolved(t *testing.T) {
	g := &fakeGH{prs: []gh.PRState{mergeableGreen(7)}, viewFound: true, createNumber: 7}
	gi := &fakeGit{}
	deps, _, _, out := newDeps(g, gi, nil, nil)
	deps.ReadTaskMeta = func(string, string) (frontmatter.Meta, error) {
		return frontmatter.Meta{ConsumerClaims: []string{"nix-config:path:foo.sh"}}, nil
	}
	deps.ConsumerScan = func(claims []consumerclaim.Claim) []consumerclaim.Result {
		return []consumerclaim.Result{{Claim: claims[0], Status: consumerclaim.StatusResolved}}
	}
	called := false
	deps.MintCutover = func(cutover.Options) (cutover.Result, error) {
		called = true
		return cutover.Result{}, nil
	}
	if err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("MintCutover called despite all-resolved claims")
	}
	if !strings.Contains(out.String(), "all resolved") {
		t.Errorf("missing all-resolved line:\n%s", out.String())
	}
}

func TestRunContinuesOnMintFailure(t *testing.T) {
	g := &fakeGH{prs: []gh.PRState{mergeableGreen(7)}, viewFound: true, createNumber: 7}
	gi := &fakeGit{}
	deps, dones, _, out := newDeps(g, gi, nil, nil)
	deps.ReadTaskMeta = func(string, string) (frontmatter.Meta, error) {
		return frontmatter.Meta{ConsumerClaims: []string{"nix-config:path:foo.sh"}}, nil
	}
	deps.ConsumerScan = func(claims []consumerclaim.Claim) []consumerclaim.Result {
		return []consumerclaim.Result{{Claim: claims[0], Status: consumerclaim.StatusUnresolved}}
	}
	deps.MintCutover = func(cutover.Options) (cutover.Result, error) {
		return cutover.Result{}, fmt.Errorf("disk full")
	}
	if err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps); err != nil {
		t.Fatalf("Run: %v, want continuation past mint error", err)
	}
	if !strings.Contains(out.String(), "cutover mint") {
		t.Errorf("missing mint-error line:\n%s", out.String())
	}
	if len(*dones) != 1 {
		t.Error("Done not called after mint failure")
	}
}

func TestRunSkipsMalformedClaim(t *testing.T) {
	g := &fakeGH{prs: []gh.PRState{mergeableGreen(7)}, viewFound: true, createNumber: 7}
	gi := &fakeGit{}
	deps, _, _, out := newDeps(g, gi, nil, nil)
	deps.ReadTaskMeta = func(string, string) (frontmatter.Meta, error) {
		return frontmatter.Meta{ConsumerClaims: []string{"malformed-no-colons", "nix-config:path:ok.sh"}}, nil
	}
	deps.ConsumerScan = func(claims []consumerclaim.Claim) []consumerclaim.Result {
		return []consumerclaim.Result{{Claim: claims[0], Status: consumerclaim.StatusResolved}}
	}
	minted := 0
	deps.MintCutover = func(cutover.Options) (cutover.Result, error) {
		minted++
		return cutover.Result{}, nil
	}
	if err := Run(Options{TasksDir: "/proj/tasks", Slug: "feat"}, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "skipping malformed claim") {
		t.Errorf("missing malformed-skip line:\n%s", out.String())
	}
	if minted != 0 {
		t.Errorf("minted %d, want 0 (sole valid claim was resolved)", minted)
	}
}

func TestProjectRootFromTasksDir(t *testing.T) {
	root, err := projectRootFromTasksDir("/abs/proj/tasks")
	if err != nil {
		t.Fatal(err)
	}
	if root != "/abs/proj" {
		t.Errorf("root = %q, want /abs/proj", root)
	}
}
