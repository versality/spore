package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/testagent"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(testagent.Run(ctx, testagent.Options{
		Provider: provider(),
		Argv:     os.Args,
	}))
}

func provider() string {
	if value := os.Getenv("SPORE_FAKE_AGENT_PROVIDER"); value != "" {
		return value
	}
	name := filepath.Base(os.Args[0])
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "claude", "codex":
		return name
	default:
		return "fake-agent"
	}
}
