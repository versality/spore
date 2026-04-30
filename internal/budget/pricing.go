package budget

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed pricing.toml
var pricingTOML string

// ModelPricing is the per-million-token rate vector for one model.
type ModelPricing struct {
	InputPerMTok       float64
	OutputPerMTok      float64
	CacheReadPerMTok   float64
	CacheCreatePerMTok float64
}

var (
	pricing         map[string]ModelPricing
	fallbackPricing ModelPricing
	pricingErr      error
)

func init() {
	pricing, fallbackPricing, pricingErr = parsePricing(pricingTOML)
}

// PricingFor returns the rate vector for model. Unknown models fall back
// to the table's `fallback` entry.
func PricingFor(model string) ModelPricing {
	if pricingErr != nil {
		return ModelPricing{}
	}
	if p, ok := pricing[model]; ok {
		return p
	}
	return fallbackPricing
}

// parsePricing reads the embedded pricing table. The format is a
// trimmed TOML subset: top-level `key = value` pairs and `[name]`
// section headers whose body is more `key = value` pairs. Strings are
// double-quoted; numbers are bare floats. No arrays, inline tables, or
// nested keys are accepted, and parse errors are reported with a line
// number so a malformed in-tree edit fails loudly at startup.
func parsePricing(src string) (map[string]ModelPricing, ModelPricing, error) {
	out := make(map[string]ModelPricing)
	var fallback string
	var fallbackEntry ModelPricing
	var section string
	var current ModelPricing
	have := map[string]bool{}

	flush := func() error {
		if section == "" {
			return nil
		}
		missing := make([]string, 0, 4)
		for _, k := range []string{"input", "output", "cache_read", "cache_create"} {
			if !have[k] {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("section [%s]: missing keys %v", section, missing)
		}
		out[section] = current
		section = ""
		current = ModelPricing{}
		have = map[string]bool{}
		return nil
	}

	for i, raw := range strings.Split(src, "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if err := flush(); err != nil {
				return nil, ModelPricing{}, fmt.Errorf("pricing.toml:%d: %w", lineNo, err)
			}
			section = strings.TrimSpace(line[1 : len(line)-1])
			if section == "" {
				return nil, ModelPricing{}, fmt.Errorf("pricing.toml:%d: empty section header", lineNo)
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, ModelPricing{}, fmt.Errorf("pricing.toml:%d: not a section or key=value", lineNo)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if section == "" {
			s, err := parseString(val)
			if err != nil {
				return nil, ModelPricing{}, fmt.Errorf("pricing.toml:%d: top-level %s: %w", lineNo, key, err)
			}
			if key == "fallback" {
				fallback = s
				continue
			}
			return nil, ModelPricing{}, fmt.Errorf("pricing.toml:%d: unknown top-level key %q", lineNo, key)
		}
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, ModelPricing{}, fmt.Errorf("pricing.toml:%d: [%s].%s = %q: %w", lineNo, section, key, val, err)
		}
		switch key {
		case "input":
			current.InputPerMTok = f
		case "output":
			current.OutputPerMTok = f
		case "cache_read":
			current.CacheReadPerMTok = f
		case "cache_create":
			current.CacheCreatePerMTok = f
		default:
			return nil, ModelPricing{}, fmt.Errorf("pricing.toml:%d: [%s] unknown key %q", lineNo, section, key)
		}
		have[key] = true
	}
	if err := flush(); err != nil {
		return nil, ModelPricing{}, err
	}
	if fallback == "" {
		return nil, ModelPricing{}, fmt.Errorf("pricing.toml: top-level `fallback` not set")
	}
	fb, ok := out[fallback]
	if !ok {
		return nil, ModelPricing{}, fmt.Errorf("pricing.toml: fallback model %q has no section", fallback)
	}
	fallbackEntry = fb
	return out, fallbackEntry, nil
}

func parseString(v string) (string, error) {
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return "", fmt.Errorf("expected double-quoted string, got %q", v)
	}
	return v[1 : len(v)-1], nil
}
