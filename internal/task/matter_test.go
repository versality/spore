package task

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/versality/spore/internal/matter"
)

type recordingDoneMatter struct {
	name    string
	doneErr error
	calls   atomic.Int32
	lastID  string
}

func (r *recordingDoneMatter) Name() string { return r.name }
func (r *recordingDoneMatter) Sync(ctx context.Context, projectRoot string) (int, int, error) {
	return 0, 0, nil
}
func (r *recordingDoneMatter) OnDone(ctx context.Context, slug string, meta map[string]string) error {
	r.calls.Add(1)
	r.lastID = meta[matter.MatterIDKey]
	return r.doneErr
}

// withMatter scopes a matter registration to the test, restoring the
// global registry on cleanup.
func withMatter(t *testing.T, name string, factory matter.Factory) {
	t.Helper()
	matter.ResetForTest()
	matter.Register(name, factory)
	t.Cleanup(matter.ResetForTest)
}

// matterTaskDirs builds project/tasks under t.TempDir() so projectRoot
// (= filepath.Dir(tasksDir)) points at a real directory we control.
func matterTaskDirs(t *testing.T) (project, tasksDir string) {
	t.Helper()
	project = t.TempDir()
	tasksDir = filepath.Join(project, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return project, tasksDir
}

func TestDoneFiresOnDoneForTaggedTask(t *testing.T) {
	rec := &recordingDoneMatter{name: "fake"}
	withMatter(t, "fake", func(c matter.Config) (matter.Matter, error) { return rec, nil })

	project, tasksDir := matterTaskDirs(t)
	if err := os.WriteFile(filepath.Join(project, "spore.toml"),
		[]byte("[matter.fake]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(tasksDir, "x.md")
	body := "---\nstatus: active\nslug: x\ntitle: X\nmatter: fake\nmatter_id: FAKE-7\n---\nbody\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Done(tasksDir, "x", true); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if got := rec.calls.Load(); got != 1 {
		t.Errorf("OnDone calls = %d, want 1", got)
	}
	if rec.lastID != "FAKE-7" {
		t.Errorf("OnDone meta[matter_id] = %q, want FAKE-7", rec.lastID)
	}
	if readStatus(t, taskPath) != "done" {
		t.Errorf("status should be done")
	}
}

func TestDoneSkipsWhenMatterMetaMissing(t *testing.T) {
	matter.ResetForTest()
	t.Cleanup(matter.ResetForTest)

	_, tasksDir := matterTaskDirs(t)
	taskPath := filepath.Join(tasksDir, "x.md")
	body := "---\nstatus: active\nslug: x\ntitle: X\n---\nbody\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Done(tasksDir, "x", true); err != nil {
		t.Fatalf("Done with no matter meta: %v", err)
	}
	if readStatus(t, taskPath) != "done" {
		t.Errorf("status should be done")
	}
}

func TestDoneSkipsWhenMatterDisabled(t *testing.T) {
	rec := &recordingDoneMatter{name: "fake"}
	withMatter(t, "fake", func(c matter.Config) (matter.Matter, error) { return rec, nil })

	project, tasksDir := matterTaskDirs(t)
	if err := os.WriteFile(filepath.Join(project, "spore.toml"),
		[]byte("[matter.fake]\nenabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(tasksDir, "x.md")
	body := "---\nstatus: active\nslug: x\ntitle: X\nmatter: fake\nmatter_id: FAKE-7\n---\nbody\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Done(tasksDir, "x", true); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if got := rec.calls.Load(); got != 0 {
		t.Errorf("OnDone should not fire when matter disabled, got %d calls", got)
	}
}

func TestDoneSwallowsOnDoneError(t *testing.T) {
	rec := &recordingDoneMatter{name: "fake", doneErr: errors.New("upstream rejected")}
	withMatter(t, "fake", func(c matter.Config) (matter.Matter, error) { return rec, nil })

	project, tasksDir := matterTaskDirs(t)
	if err := os.WriteFile(filepath.Join(project, "spore.toml"),
		[]byte("[matter.fake]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(tasksDir, "x.md")
	body := "---\nstatus: active\nslug: x\ntitle: X\nmatter: fake\nmatter_id: FAKE-9\n---\nbody\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Done(tasksDir, "x", true); err != nil {
		t.Fatalf("Done should not surface OnDone error: %v", err)
	}
	if readStatus(t, taskPath) != "done" {
		t.Errorf("status should be done despite OnDone error")
	}
	if got := rec.calls.Load(); got != 1 {
		t.Errorf("OnDone calls = %d, want 1", got)
	}
}
