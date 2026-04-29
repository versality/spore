package hooks

// Stop is the no-op scaffold for the Stop hook. Spore intentionally
// ships nothing here so downstream consumers can wire their own
// behavior (auto-commit, replenish, token-monitor, ...) without
// unbinding a default. Returning a zero Response tells claude-code
// to take no action.
func Stop(req Request) Response {
	return Response{}
}
