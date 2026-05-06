// Package matter is the plugin layer for external work sources
// (Linear, GitHub Issues, Jira, ad-hoc CSV, ...). A Matter polls its
// upstream and projects ready tickets onto tasks/<slug>.md files;
// when a task carrying its metadata flips to done, the matter is
// asked to mirror that back upstream.
//
// The package is interface + registry only: adapters live in their
// own subpackages (e.g. internal/matter/linear) and self-register
// from init(). Pure stdlib.
//
// Frontmatter convention. Tasks created by a matter carry three
// extra keys in their frontmatter envelope:
//
//	matter: <name>          # registry key, e.g. "linear"
//	matter_id: <id>         # adapter-defined external id
//	matter_url: <url>       # adapter-defined deep link (optional)
//
// MatterKey, MatterIDKey, and MatterURLKey are the canonical names.
// OnDone receives the parsed Extra map verbatim so adapters can read
// any additional keys they wrote.
package matter

import "context"

// Frontmatter Extra keys reserved for matter wiring. Adapters MUST
// write MatterKey when they create a task; MatterIDKey is strongly
// encouraged so OnDone has a stable handle on the upstream record.
// MatterSortOrderKey is optional: when an adapter writes it, the
// fleet reconciler uses it to break ties between active tasks
// instead of falling back to slug order, so an upstream reorder
// (e.g. a Linear kanban drag) can change which task spawns next
// when the worker cap clips the active set. Lower values spawn first.
const (
	MatterKey          = "matter"
	MatterIDKey        = "matter_id"
	MatterURLKey       = "matter_url"
	MatterSortOrderKey = "matter_sort_order"
)

// Matter is an external work source that creates tasks from upstream
// state and mirrors local task transitions back to it. Implementations
// are registered via Register from an init() function in their own
// package; FromConfig instantiates them from parsed Config entries.
type Matter interface {
	// Name returns the registry key, e.g. "linear" or
	// "github-issues". It must match the [matter.<name>] section
	// the adapter was instantiated from.
	Name() string

	// Sync polls the upstream source and creates or updates
	// tasks/<slug>.md files under projectRoot. Returns the number
	// of task files created and updated by this pass. Implementations
	// must be safe to call repeatedly: re-syncing an unchanged
	// upstream should report 0/0 and touch nothing on disk.
	Sync(ctx context.Context, projectRoot string) (created, updated int, err error)

	// OnDone is invoked after a task carrying this matter's
	// metadata flips to status=done. Meta is the task's frontmatter
	// Extra map (matter, matter_id, matter_url, plus anything else
	// the adapter wrote). Adapters should be idempotent: a re-fired
	// OnDone for an already-closed upstream record is not an error.
	OnDone(ctx context.Context, slug string, meta map[string]string) error
}

// Config is one parsed [matter.<name>] section. Options carries the
// adapter-specific key-value pairs verbatim (TOML scalar values are
// stored as their string form); the adapter's factory is responsible
// for type-checking and defaulting.
//
// "enabled" is lifted out of Options into Enabled so the registry
// can skip disabled entries without the adapter having to care.
type Config struct {
	Name    string
	Enabled bool
	Options map[string]string
}

// Option returns Options[key] with a default when absent or empty.
// Convenience for adapter factories.
func (c Config) Option(key, def string) string {
	if v, ok := c.Options[key]; ok && v != "" {
		return v
	}
	return def
}
