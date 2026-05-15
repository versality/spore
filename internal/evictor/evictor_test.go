package evictor

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestShouldEvict(t *testing.T) {
	now := time.Date(2026, 5, 15, 18, 0, 0, 0, time.UTC)
	threshold := 10 * time.Minute

	cases := []struct {
		name string
		in   Inputs
		want bool
	}{
		{
			name: "all-three-hold",
			in: Inputs{
				SessionPresent:  true,
				Idle:            11 * time.Minute,
				IdleKnown:       true,
				UnreadInbox:     0,
				LastCommit:      now.Add(-11 * time.Minute),
				LastCommitKnown: true,
			},
			want: true,
		},
		{
			name: "no-session-leave-alone",
			in: Inputs{
				SessionPresent: false,
				Idle:           11 * time.Minute,
				IdleKnown:      true,
				UnreadInbox:    0,
			},
			want: false,
		},
		{
			name: "idle-unknown-leave-alone",
			in: Inputs{
				SessionPresent: true,
				IdleKnown:      false,
				UnreadInbox:    0,
			},
			want: false,
		},
		{
			name: "still-active",
			in: Inputs{
				SessionPresent: true,
				Idle:           1 * time.Minute,
				IdleKnown:      true,
				UnreadInbox:    0,
			},
			want: false,
		},
		{
			name: "unread-inbox-pending",
			in: Inputs{
				SessionPresent: true,
				Idle:           20 * time.Minute,
				IdleKnown:      true,
				UnreadInbox:    1,
			},
			want: false,
		},
		{
			name: "recent-commit-progress",
			in: Inputs{
				SessionPresent:  true,
				Idle:            20 * time.Minute,
				IdleKnown:       true,
				UnreadInbox:     0,
				LastCommit:      now.Add(-2 * time.Minute),
				LastCommitKnown: true,
			},
			want: false,
		},
		{
			name: "missing-branch-counts-as-no-progress",
			in: Inputs{
				SessionPresent:  true,
				Idle:            20 * time.Minute,
				IdleKnown:       true,
				UnreadInbox:     0,
				LastCommitKnown: false,
			},
			want: true,
		},
		{
			name: "boundary-idle-equals-threshold",
			in: Inputs{
				SessionPresent:  true,
				Idle:            threshold,
				IdleKnown:       true,
				UnreadInbox:     0,
				LastCommitKnown: false,
			},
			want: true,
		},
		{
			name: "boundary-idle-just-below-threshold",
			in: Inputs{
				SessionPresent:  true,
				Idle:            threshold - time.Nanosecond,
				IdleKnown:       true,
				UnreadInbox:     0,
				LastCommitKnown: false,
			},
			want: false,
		},
		{
			name: "boundary-commit-equals-threshold",
			in: Inputs{
				SessionPresent:  true,
				Idle:            threshold,
				IdleKnown:       true,
				UnreadInbox:     0,
				LastCommit:      now.Add(-threshold),
				LastCommitKnown: true,
			},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ShouldEvict(now, threshold, tc.in)
			if got != tc.want {
				t.Fatalf("ShouldEvict = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDisabled(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"1", false},
		{"true", false},
		{"on", false},
		{"yes", false},
		{"0", true},
		{"false", true},
		{"off", true},
		{"no", true},
		{"FALSE", true},
		{" 0 ", true},
	}
	for _, tc := range cases {
		t.Run(tc.val, func(t *testing.T) {
			t.Setenv(KillSwitchEnv, tc.val)
			if got := Disabled(); got != tc.want {
				t.Fatalf("Disabled(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}

func TestResolveThreshold(t *testing.T) {
	t.Setenv(IdleSecsEnv, "")
	if got := ResolveThreshold(); got != DefaultIdleThreshold {
		t.Fatalf("unset -> %v, want default %v", got, DefaultIdleThreshold)
	}
	t.Setenv(IdleSecsEnv, "30")
	if got := ResolveThreshold(); got != 30*time.Second {
		t.Fatalf("30 -> %v, want 30s", got)
	}
	t.Setenv(IdleSecsEnv, "0")
	if got := ResolveThreshold(); got != 0 {
		t.Fatalf("0 -> %v, want 0", got)
	}
	t.Setenv(IdleSecsEnv, "garbage")
	if got := ResolveThreshold(); got != DefaultIdleThreshold {
		t.Fatalf("garbage -> %v, want default %v", got, DefaultIdleThreshold)
	}
}

func TestWriteReport(t *testing.T) {
	rep := Report{
		Slugs: []string{"a", "b", "c"},
		Decisions: []Decision{
			{Slug: "a", Evicted: true},
			{Slug: "b", Reason: "no live tmux session"},
			{Slug: "c", Err: errString("git: probe failed")},
		},
	}
	var buf bytes.Buffer
	WriteReport(&buf, rep)
	out := buf.String()
	for _, want := range []string{
		"a evicted",
		"b kept (no live tmux session)",
		"c: git: probe failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q\n%s", want, out)
		}
	}
}

func TestWriteReportDisabled(t *testing.T) {
	var buf bytes.Buffer
	WriteReport(&buf, Report{Disabled: true})
	if !strings.Contains(buf.String(), "disabled") {
		t.Fatalf("disabled banner missing: %q", buf.String())
	}
}

type errString string

func (e errString) Error() string { return string(e) }
