package coordinator

import (
	"path/filepath"
	"testing"
)

func TestStateDir(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "explicit override wins",
			env:  map[string]string{"SPORE_COORDINATOR_STATE_DIR": "/srv/state", "XDG_STATE_HOME": "/xdg", "HOME": "/home/u"},
			want: "/srv/state",
		},
		{
			name: "xdg fallback",
			env:  map[string]string{"SPORE_COORDINATOR_STATE_DIR": "", "XDG_STATE_HOME": "/xdg", "HOME": "/home/u"},
			want: "/xdg/spore/coordinator",
		},
		{
			name: "home fallback",
			env:  map[string]string{"SPORE_COORDINATOR_STATE_DIR": "", "XDG_STATE_HOME": "", "HOME": "/home/u"},
			want: "/home/u/.local/state/spore/coordinator",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got := StateDir()
			if got != tc.want {
				t.Fatalf("StateDir = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatePath(t *testing.T) {
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", "/srv/state")
	got := StatePath("worker-watch.json")
	want := filepath.Join("/srv/state", "worker-watch.json")
	if got != want {
		t.Fatalf("StatePath = %q, want %q", got, want)
	}
}
