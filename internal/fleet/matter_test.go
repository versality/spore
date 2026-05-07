package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/versality/spore/internal/matter"
)

type recordingMatter struct {
	name      string
	created   int
	updated   int
	syncErr   error
	spawnErr  error
	calls     atomic.Int32
	spawnHits atomic.Int32
	lastSpawn atomic.Value // string
}

func (r *recordingMatter) Name() string { return r.name }
func (r *recordingMatter) Sync(ctx context.Context, projectRoot string) (int, int, error) {
	r.calls.Add(1)
	return r.created, r.updated, r.syncErr
}
func (r *recordingMatter) OnSpawn(ctx context.Context, slug string, meta map[string]string) error {
	r.spawnHits.Add(1)
	r.lastSpawn.Store(slug)
	return r.spawnErr
}
func (r *recordingMatter) OnDone(ctx context.Context, slug string, meta map[string]string) error {
	return nil
}

// installMatter swaps the global registry for the duration of the
// test and registers `factory` under `name`. The registry is
// restored on cleanup so other tests aren't polluted.
func installMatter(t *testing.T, name string, factory matter.Factory) {
	t.Helper()
	matter.ResetForTest()
	matter.Register(name, factory)
	t.Cleanup(matter.ResetForTest)
}

func writeSporeTOML(t *testing.T, projectRoot, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(projectRoot, "spore.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// syncMatters is the prelude pass Reconcile runs against the matter
// plugin layer. Testing it in isolation lets us verify the contract
// (created/updated counters, captured errors, no-op when nothing is
// configured) without depending on tmux/git for the full pass.

func TestSyncMattersHappyPath(t *testing.T) {
	dirs := newTestDirs(t)
	rec := &recordingMatter{name: "fake", created: 2, updated: 1}
	installMatter(t, "fake", func(c matter.Config) (matter.Matter, error) { return rec, nil })
	writeSporeTOML(t, dirs.project, "[matter.fake]\nenabled = true\n")

	res := syncMatters(dirs.project)
	if rec.calls.Load() != 1 {
		t.Errorf("Sync calls = %d, want 1", rec.calls.Load())
	}
	if len(res) != 1 {
		t.Fatalf("results = %v", res)
	}
	got := res[0]
	if got.Name != "fake" || got.Created != 2 || got.Updated != 1 || got.Err != nil {
		t.Errorf("result = %+v", got)
	}
}

func TestSyncMattersErrorIsCaptured(t *testing.T) {
	dirs := newTestDirs(t)
	boom := errors.New("upstream down")
	rec := &recordingMatter{name: "fake", syncErr: boom}
	installMatter(t, "fake", func(c matter.Config) (matter.Matter, error) { return rec, nil })
	writeSporeTOML(t, dirs.project, "[matter.fake]\nenabled = true\n")

	res := syncMatters(dirs.project)
	if len(res) != 1 || !errors.Is(res[0].Err, boom) {
		t.Errorf("result = %+v", res)
	}
}

func TestSyncMattersNoConfig(t *testing.T) {
	dirs := newTestDirs(t)
	if got := syncMatters(dirs.project); got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestSyncMattersDisabledSkipped(t *testing.T) {
	dirs := newTestDirs(t)
	rec := &recordingMatter{name: "fake"}
	installMatter(t, "fake", func(c matter.Config) (matter.Matter, error) { return rec, nil })
	writeSporeTOML(t, dirs.project, "[matter.fake]\nenabled = false\n")

	if got := syncMatters(dirs.project); got != nil {
		t.Errorf("disabled matter should produce no result, got %v", got)
	}
	if rec.calls.Load() != 0 {
		t.Errorf("disabled matter Sync should not be called, got %d", rec.calls.Load())
	}
}

func TestReconcileFiresOnSpawnAfterTaskEnsure(t *testing.T) {
	requireToolchain(t)

	dirs := newTestDirs(t)
	gitInit(t, dirs.project)
	mustEnable(t)
	t.Setenv("SPORE_AGENT_BINARY", "sleep 30")

	rec := &recordingMatter{name: "fake"}
	installMatter(t, "fake", func(c matter.Config) (matter.Matter, error) { return rec, nil })
	writeSporeTOML(t, dirs.project, "[matter.fake]\nenabled = true\n")

	// MCOM-85: the rover-claim signal must fire when a worker
	// session is created, not when the task is projected. Tag the
	// task with the matter id so NotifyMatterSpawn dispatches.
	writeTaskWithExtras(t, dirs.tasks, "alpha", "active", map[string]string{
		matter.MatterKey:   "fake",
		matter.MatterIDKey: "FAKE-1",
	})
	t.Cleanup(func() { killSporeSessions(dirs.project) })

	r, err := Reconcile(Config{
		TasksDir:    dirs.tasks,
		ProjectRoot: dirs.project,
		MaxWorkers:  3,
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if got, want := r.Spawned, []string{"alpha"}; !equalSlices(got, want) {
		t.Fatalf("Spawned = %v, want %v", got, want)
	}
	if got := rec.spawnHits.Load(); got != 1 {
		t.Errorf("OnSpawn calls after first Reconcile = %d, want 1", got)
	}
	if got := rec.lastSpawn.Load(); got != "alpha" {
		t.Errorf("OnSpawn last slug = %v, want alpha", got)
	}

	// A second Reconcile pass keeps the existing session: no new
	// spawn means no new OnSpawn fire.
	if _, err := Reconcile(Config{
		TasksDir:    dirs.tasks,
		ProjectRoot: dirs.project,
		MaxWorkers:  3,
	}); err != nil {
		t.Fatalf("Reconcile pass 2: %v", err)
	}
	if got := rec.spawnHits.Load(); got != 1 {
		t.Errorf("OnSpawn calls after second Reconcile = %d, want 1 (no respawn)", got)
	}
}

func TestSyncMattersUnknownAdapterCapturedAsError(t *testing.T) {
	dirs := newTestDirs(t)
	matter.ResetForTest()
	t.Cleanup(matter.ResetForTest)
	writeSporeTOML(t, dirs.project, "[matter.ghost]\nenabled = true\n")

	res := syncMatters(dirs.project)
	if len(res) != 1 || res[0].Err == nil {
		t.Fatalf("want error result for unknown adapter, got %v", res)
	}
}
