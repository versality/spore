package main

import (
	"flag"
	"reflect"
	"testing"
)

func TestReorderFlagsFirst(t *testing.T) {
	mkFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.Bool("verbose", false, "")
		fs.String("ssh-key", "", "")
		fs.String("flake", "", "")
		return fs
	}

	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "value flag after positional keeps its value glued",
			in:   []string{"1.2.3.4", "--ssh-key", "/k"},
			want: []string{"--ssh-key", "/k", "1.2.3.4"},
		},
		{
			name: "value flag with = stays single token",
			in:   []string{"1.2.3.4", "--ssh-key=/k"},
			want: []string{"--ssh-key=/k", "1.2.3.4"},
		},
		{
			name: "bool flag does not eat next arg",
			in:   []string{"slug", "--verbose"},
			want: []string{"--verbose", "slug"},
		},
		{
			name: "multiple value flags interleaved with positional",
			in:   []string{"ip", "--ssh-key", "/k", "--flake", "./f"},
			want: []string{"--ssh-key", "/k", "--flake", "./f", "ip"},
		},
		{
			name: "double-dash ends flag parsing",
			in:   []string{"--verbose", "--", "--ssh-key", "/k"},
			want: []string{"--verbose", "--", "--ssh-key", "/k"},
		},
		{
			name: "bare dash treated as positional",
			in:   []string{"-", "--verbose"},
			want: []string{"--verbose", "-"},
		},
		{
			name: "unknown flag treated as value flag so flag.Parse errors cleanly",
			in:   []string{"pos", "--unknown", "v"},
			want: []string{"--unknown", "v", "pos"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reorderFlagsFirst(mkFS(), tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("reorderFlagsFirst(%v)\n got: %v\nwant: %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestReorderFlagsFirstParsesInfectShape(t *testing.T) {
	fs := flag.NewFlagSet("infect", flag.ContinueOnError)
	sshKey := fs.String("ssh-key", "", "")
	const wantIP = "10.0.0.1"
	const wantKey = "~/.ssh/id_ed25519"
	args := []string{wantIP, "--ssh-key", wantKey}
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := fs.Arg(0); got != wantIP {
		t.Fatalf("positional: got %q want %q", got, wantIP)
	}
	if *sshKey != wantKey {
		t.Fatalf("ssh-key: got %q want %q", *sshKey, wantKey)
	}
}
