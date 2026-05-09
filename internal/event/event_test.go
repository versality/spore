package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func eventDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("SPORE_EVENT_DIR", d)
	t.Setenv("SPORE_EVENT_MAX_BYTES", "")
	return d
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
		ok   bool
	}{
		{"ok info", Event{Source: "s", Level: "info", Kind: "k", Message: "m"}, true},
		{"ok warn", Event{Source: "s", Level: "warn", Kind: "k", Message: "m"}, true},
		{"ok error", Event{Source: "s", Level: "error", Kind: "k", Message: "m"}, true},
		{"missing level", Event{Source: "s", Kind: "k", Message: "m"}, false},
		{"bad level", Event{Source: "s", Level: "debug", Kind: "k", Message: "m"}, false},
		{"missing source", Event{Level: "info", Kind: "k", Message: "m"}, false},
		{"missing kind", Event{Source: "s", Level: "info", Message: "m"}, false},
		{"missing message", Event{Source: "s", Level: "info", Kind: "k"}, false},
		{"bad data json", Event{Source: "s", Level: "info", Kind: "k", Message: "m", Data: []byte("{")}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.ev.Validate()
			if c.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestAppendAndRead(t *testing.T) {
	eventDir(t)

	for i, lvl := range []string{"info", "warn", "error"} {
		ev := &Event{
			Source:  "test",
			Level:   lvl,
			Kind:    "test:case",
			Message: "msg",
			Slug:    "s",
			Data:    json.RawMessage(`{"i":` + itoa(i) + `}`),
		}
		if err := Append(ev); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got, err := Read(Filter{}, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 events, got %d", len(got))
	}
	for i, ev := range got {
		if ev.Source != "test" || ev.Kind != "test:case" || ev.Slug != "s" || ev.Message != "msg" {
			t.Errorf("ev[%d] mismatch: %+v", i, ev)
		}
		if ev.Ts.IsZero() {
			t.Errorf("ev[%d] ts not stamped", i)
		}
		if len(ev.Data) == 0 {
			t.Errorf("ev[%d] data not preserved", i)
		}
	}
}

func TestFilterAND(t *testing.T) {
	eventDir(t)

	mustAppend := func(t *testing.T, ev *Event) {
		t.Helper()
		if err := Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	mustAppend(t, &Event{Source: "skyhelm", Level: "info", Kind: "boot", Slug: "a", Message: "1"})
	mustAppend(t, &Event{Source: "skyhelm", Level: "error", Kind: "boot", Slug: "a", Message: "2"})
	mustAppend(t, &Event{Source: "wt-task", Level: "error", Kind: "merge:fail", Slug: "b", Message: "3"})
	mustAppend(t, &Event{Source: "skyhelm", Level: "warn", Kind: "boot", Slug: "c", Message: "4"})

	// level=error AND source=skyhelm -> just message "2"
	got, err := Read(Filter{Level: "error", Source: "skyhelm"}, 0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].Message != "2" {
		t.Fatalf("want 1 event 'msg=2', got %+v", got)
	}

	// kind=boot AND slug=a -> messages 1 and 2
	got, err = Read(Filter{Kind: "boot", Slug: "a"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events, got %d", len(got))
	}
}

func TestSinceFilter(t *testing.T) {
	eventDir(t)

	old := &Event{Source: "s", Level: "info", Kind: "k", Message: "old", Ts: time.Now().Add(-2 * time.Hour)}
	if err := Append(old); err != nil {
		t.Fatal(err)
	}
	fresh := &Event{Source: "s", Level: "info", Kind: "k", Message: "fresh"}
	if err := Append(fresh); err != nil {
		t.Fatal(err)
	}

	got, err := Read(Filter{Since: time.Now().Add(-5 * time.Minute)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Message != "fresh" {
		t.Fatalf("since filter wrong: got %+v", got)
	}
}

func TestRotation(t *testing.T) {
	d := eventDir(t)
	t.Setenv("SPORE_EVENT_MAX_BYTES", "300")

	// Each line is roughly 100 bytes. Three appends should trigger
	// at least one rotation.
	for i := 0; i < 6; i++ {
		ev := &Event{Source: "rotate", Level: "info", Kind: "rotate:case", Message: "padpadpadpad-" + itoa(i)}
		if err := Append(ev); err != nil {
			t.Fatal(err)
		}
	}

	entries, _ := os.ReadDir(d)
	rotated := 0
	hasCurrent := false
	for _, e := range entries {
		switch {
		case e.Name() == "events.jsonl":
			hasCurrent = true
		case strings.HasPrefix(e.Name(), "events-") && strings.HasSuffix(e.Name(), ".jsonl"):
			rotated++
		}
	}
	if !hasCurrent || rotated < 1 {
		t.Fatalf("rotation not visible: current=%v rotated=%d entries=%v", hasCurrent, rotated, entries)
	}

	// Reader must merge across rotated + current.
	got, err := Read(Filter{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("want 6 events across rotations, got %d", len(got))
	}
	for i, ev := range got {
		if !strings.HasSuffix(ev.Message, "-"+itoa(i)) {
			t.Fatalf("rotation order broken: ev[%d].Message=%q", i, ev.Message)
		}
	}
}

func TestReadLimit(t *testing.T) {
	eventDir(t)
	for i := 0; i < 10; i++ {
		ev := &Event{Source: "s", Level: "info", Kind: "k", Message: itoa(i)}
		if err := Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Read(Filter{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	if got[0].Message != "7" || got[2].Message != "9" {
		t.Fatalf("limit returned wrong tail: %+v", got)
	}
}

func TestFollow(t *testing.T) {
	eventDir(t)

	stop := make(chan struct{})
	var (
		mu   sync.Mutex
		seen []Event
	)
	done := make(chan struct{})
	go func() {
		_ = Follow(stop, 25*time.Millisecond, Filter{Level: "error"}, func(ev Event) {
			mu.Lock()
			seen = append(seen, ev)
			mu.Unlock()
		})
		close(done)
	}()

	// Give the follower a moment to open the file.
	time.Sleep(50 * time.Millisecond)
	for _, lvl := range []string{"info", "error", "info", "error"} {
		if err := Append(&Event{Source: "f", Level: lvl, Kind: "k", Message: "m"}); err != nil {
			t.Fatal(err)
		}
	}

	// Wait for two error events (the follow should drop the info ones).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("want 2 error events via Follow, got %d (%+v)", len(seen), seen)
	}
}

func TestFilesEmptyDir(t *testing.T) {
	d := eventDir(t)
	// no events written
	got, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 files, got %v", got)
	}

	// missing dir is also fine.
	os.RemoveAll(d)
	got, err = Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("missing dir should yield 0 files, got %v", got)
	}
}

func TestDirOverride(t *testing.T) {
	d := eventDir(t)
	cur, err := CurrentPath()
	if err != nil {
		t.Fatal(err)
	}
	if cur != filepath.Join(d, "events.jsonl") {
		t.Fatalf("current path wrong: %s", cur)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
