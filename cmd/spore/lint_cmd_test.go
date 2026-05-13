package main

import (
	"reflect"
	"testing"
)

func TestReorderLintArgsAllowsFlagsAfterName(t *testing.T) {
	got := reorderLintArgs([]string{"claude-drift", "--render-cmd", "printf ok", "--root", "repo"})
	want := []string{"--render-cmd", "printf ok", "--root", "repo", "claude-drift"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestReorderLintArgsLeavesBoolFlagsValueless(t *testing.T) {
	got := reorderLintArgs([]string{"--list"})
	want := []string{"--list"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
