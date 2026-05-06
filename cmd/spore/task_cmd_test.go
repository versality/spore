package main

import (
	"strings"
	"testing"
)

func TestRunTaskMergeForceMergeRedRequiresReason(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"flag with no follow-up", []string{"demo", "--force-merge-red"}},
		{"flag followed by empty string", []string{"demo", "--force-merge-red", ""}},
		{"flag with empty equals", []string{"demo", "--force-merge-red="}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runTaskMerge(tc.args)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "requires a <reason>") {
				t.Errorf("error = %q should mention requires a <reason>", err)
			}
		})
	}
}

func TestRunTaskMergeUnknownFlag(t *testing.T) {
	err := runTaskMerge([]string{"demo", "--bogus"})
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("error = %q should mention unknown flag", err)
	}
}

func TestRunTaskMergeMissingSlug(t *testing.T) {
	err := runTaskMerge(nil)
	if err == nil {
		t.Fatal("expected error for missing slug, got nil")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("error = %q should be a usage hint", err)
	}
}
