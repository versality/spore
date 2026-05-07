package matter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeMatter struct {
	name     string
	created  int
	updated  int
	syncErr  error
	spawnErr error
	doneErr  error
	calls    struct {
		sync    int
		onSpawn []string
		onDone  []string
	}
}

func (f *fakeMatter) Name() string { return f.name }
func (f *fakeMatter) Sync(ctx context.Context, projectRoot string) (int, int, error) {
	f.calls.sync++
	return f.created, f.updated, f.syncErr
}
func (f *fakeMatter) OnSpawn(ctx context.Context, slug string, meta map[string]string) error {
	f.calls.onSpawn = append(f.calls.onSpawn, slug)
	return f.spawnErr
}
func (f *fakeMatter) OnDone(ctx context.Context, slug string, meta map[string]string) error {
	f.calls.onDone = append(f.calls.onDone, slug)
	return f.doneErr
}

func TestRegisterAndFromConfig(t *testing.T) {
	t.Cleanup(reset)
	reset()

	Register("alpha", func(c Config) (Matter, error) {
		return &fakeMatter{name: c.Name, created: 2, updated: 1}, nil
	})
	Register("beta", func(c Config) (Matter, error) {
		return &fakeMatter{name: c.Name}, nil
	})

	got, err := FromConfig([]Config{
		{Name: "alpha", Enabled: true, Options: map[string]string{"team": "MAR"}},
		{Name: "beta", Enabled: false},
		{Name: "alpha", Enabled: true},
	})
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 matters, got %d", len(got))
	}
	if got[0].Name() != "alpha" || got[1].Name() != "alpha" {
		t.Errorf("want both alphas, got %s, %s", got[0].Name(), got[1].Name())
	}
	names := Registered()
	if !reflect.DeepEqual(names, []string{"alpha", "beta"}) {
		t.Errorf("Registered: %v", names)
	}
}

func TestFromConfigUnknownAdapter(t *testing.T) {
	t.Cleanup(reset)
	reset()
	Register("alpha", func(c Config) (Matter, error) { return &fakeMatter{name: c.Name}, nil })

	_, err := FromConfig([]Config{{Name: "ghost", Enabled: true}})
	if err == nil {
		t.Fatal("want error for missing adapter")
	}
	if !strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "alpha") {
		t.Errorf("error %q should mention both ghost (missing) and alpha (registered)", err)
	}
}

func TestFromConfigFactoryError(t *testing.T) {
	t.Cleanup(reset)
	reset()
	boom := errors.New("init failed")
	Register("alpha", func(c Config) (Matter, error) { return nil, boom })

	_, err := FromConfig([]Config{{Name: "alpha", Enabled: true}})
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("want wrapped boom, got %v", err)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	t.Cleanup(reset)
	reset()
	Register("alpha", func(c Config) (Matter, error) { return &fakeMatter{name: c.Name}, nil })
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("want panic on duplicate Register")
		}
	}()
	Register("alpha", func(c Config) (Matter, error) { return &fakeMatter{name: c.Name}, nil })
}

func TestRegisterEmptyNamePanics(t *testing.T) {
	t.Cleanup(reset)
	reset()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("want panic on empty name")
		}
	}()
	Register("", func(c Config) (Matter, error) { return &fakeMatter{}, nil })
}

func TestConfigOptionDefault(t *testing.T) {
	c := Config{Options: map[string]string{"team": "MAR", "blank": ""}}
	if got := c.Option("team", "fallback"); got != "MAR" {
		t.Errorf("set key: got %q", got)
	}
	if got := c.Option("blank", "fallback"); got != "fallback" {
		t.Errorf("blank value should fall back: got %q", got)
	}
	if got := c.Option("missing", "fallback"); got != "fallback" {
		t.Errorf("missing key: got %q", got)
	}
}
