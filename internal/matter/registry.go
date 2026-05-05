package matter

import (
	"fmt"
	"sort"
	"sync"
)

// Factory builds a Matter from a parsed Config. Adapters register a
// factory under their canonical name from an init() function:
//
//	func init() {
//	    matter.Register("linear", func(c matter.Config) (matter.Matter, error) {
//	        return newLinear(c)
//	    })
//	}
type Factory func(Config) (Matter, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds a factory under name. A second registration with the
// same name panics: registration is a process-startup concern and a
// duplicate is a programming error, not a runtime condition. Empty
// names panic for the same reason.
func Register(name string, factory Factory) {
	if name == "" {
		panic("matter.Register: empty name")
	}
	if factory == nil {
		panic("matter.Register: nil factory for " + name)
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("matter.Register: duplicate name " + name)
	}
	registry[name] = factory
}

// Registered returns the sorted list of registered matter names.
// Useful for diagnostics and for `spore matter status`-shaped CLIs.
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ResetForTest clears the registry. Tests in this and downstream
// packages call it from t.Cleanup to keep adapter registrations
// scoped to a single test. The package never calls it itself:
// Register is one-shot per process in production code paths.
func ResetForTest() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[string]Factory{}
}

// reset is the in-package alias used by this package's own tests.
func reset() { ResetForTest() }

// FromConfig instantiates the enabled subset of configs against the
// registry. Disabled entries are skipped silently. An entry with no
// registered factory is an error: misconfiguration should surface
// loudly rather than be ignored.
//
// Order of returned matters mirrors the input order so callers can
// reason about Sync sequencing.
func FromConfig(configs []Config) ([]Matter, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	var out []Matter
	for _, c := range configs {
		if !c.Enabled {
			continue
		}
		f, ok := registry[c.Name]
		if !ok {
			return nil, fmt.Errorf("matter %q: no adapter registered (have: %v)", c.Name, registeredNamesLocked())
		}
		m, err := f(c)
		if err != nil {
			return nil, fmt.Errorf("matter %q: %w", c.Name, err)
		}
		out = append(out, m)
	}
	return out, nil
}

func registeredNamesLocked() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
