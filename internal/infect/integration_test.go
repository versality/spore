package infect

import (
	"context"
	"os"
	"testing"

	spore "github.com/versality/spore"
)

// TestRunIntegration runs the full nixos-anywhere flow against a real
// target. It is skipped unless SPORE_INFECT_IT_TARGET is set; the
// caller must also point SPORE_INFECT_IT_KEY at a private SSH key
// whose .pub sibling is the post-install root key.
//
// WARNING: the target host will be wiped. Only point this at a fresh
// VM that exists for this test.
func TestRunIntegration(t *testing.T) {
	ip := os.Getenv("SPORE_INFECT_IT_TARGET")
	if ip == "" {
		t.Skipf("set SPORE_INFECT_IT_TARGET=<ip> to run; this WIPES the target")
	}
	key := os.Getenv("SPORE_INFECT_IT_KEY")
	if key == "" {
		t.Skipf("set SPORE_INFECT_IT_KEY=<private-key-path> alongside SPORE_INFECT_IT_TARGET")
	}
	c := Config{IP: ip, SSHKey: key}
	if err := Run(context.Background(), c, spore.BundledFlake, os.Stdout, os.Stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
