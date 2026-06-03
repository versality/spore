package lints

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/versality/spore/internal/sporetoml"
)

type Config struct {
	ByName map[string]LintConfig
}

type LintConfig struct {
	Allowlist         []string
	ConsumersDir      string
	RulesDir          string
	RenderCmd         string
	ConsumersCmd      string
	Limit             int
	Ext               []string
	SkipPath          []string
	RootLineLimit     int
	RootCharLimit     int
	SubdirLineLimit   int
	FlakePath         string
	ScanDirs          []string
	Hosts             []string
	AgentSession      string
	AgentService      string
	AgentServiceAllow []string
	AgentProcesses    []string
	ForbiddenSlugs    map[string]string
	ForbiddenPaths    map[string]string
	SlugAllowlist     []string
}

func LoadProjectConfig(root string) (Config, error) {
	path := filepath.Join(root, "spore.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{ByName: map[string]LintConfig{}}, nil
		}
		return Config{}, fmt.Errorf("lint: read %s: %w", path, err)
	}
	cfg, err := ParseConfig(string(b))
	if err != nil {
		return Config{}, fmt.Errorf("lint: parse %s: %w", path, err)
	}
	return cfg, nil
}

func ParseConfig(content string) (Config, error) {
	cfg := Config{ByName: map[string]LintConfig{}}
	err := sporetoml.ScanSections(content, func(l sporetoml.Line) error {
		name, ok := strings.CutPrefix(l.Section, "lint.")
		if !ok {
			return nil
		}
		current := strings.TrimSpace(name)
		if current == "" {
			return fmt.Errorf("line %d: empty lint name in %q", l.LineNum, l.Section)
		}
		if _, ok := cfg.ByName[current]; !ok {
			cfg.ByName[current] = LintConfig{}
		}
		key, raw, ok := sporetoml.SplitKeyValue(l.Text)
		if !ok {
			return fmt.Errorf("line %d: malformed entry %q", l.LineNum, l.Text)
		}
		lc := cfg.ByName[current]
		if err := setLintConfigValue(&lc, key, raw, l.LineNum); err != nil {
			return err
		}
		cfg.ByName[current] = lc
		return nil
	})
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func MergeConfig(base, override Config) Config {
	if base.ByName == nil {
		base.ByName = map[string]LintConfig{}
	}
	for name, over := range override.ByName {
		base.ByName[name] = mergeLintConfig(base.ByName[name], over)
	}
	return base
}

func ApplyConfig(in []Lint, cfg Config) []Lint {
	out := make([]Lint, 0, len(in))
	for _, l := range in {
		out = append(out, applyLintConfig(l, cfg.ByName[l.Name()]))
	}
	return out
}

func applyLintConfig(l Lint, cfg LintConfig) Lint {
	switch v := l.(type) {
	case EmDash:
		v.Allowlist = append(v.Allowlist, cfg.Allowlist...)
		return v
	case ClaudeDrift:
		if cfg.ConsumersDir != "" {
			v.ConsumersDir = cfg.ConsumersDir
		}
		if cfg.RulesDir != "" {
			v.RulesDir = cfg.RulesDir
		}
		if cfg.RenderCmd != "" {
			v.RenderCmd = cfg.RenderCmd
		}
		if cfg.ConsumersCmd != "" {
			v.ConsumersCmd = cfg.ConsumersCmd
		}
		return v
	case FileSize:
		if cfg.Limit > 0 {
			v.Limit = cfg.Limit
		}
		if len(cfg.Ext) > 0 {
			v.Ext = cfg.Ext
		}
		return v
	case ClaudeTotalSize:
		if cfg.RootLineLimit > 0 {
			v.RootLineLimit = cfg.RootLineLimit
		}
		if cfg.RootCharLimit > 0 {
			v.RootCharLimit = cfg.RootCharLimit
		}
		if cfg.SubdirLineLimit > 0 {
			v.SubdirLineLimit = cfg.SubdirLineLimit
		}
		return v
	case CommentNoise:
		if len(cfg.Ext) > 0 {
			v.Ext = cfg.Ext
		}
		v.SkipPath = append(v.SkipPath, cfg.SkipPath...)
		return v
	case Decoration:
		if len(cfg.Ext) > 0 {
			v.Ext = cfg.Ext
		}
		v.SkipPath = append(v.SkipPath, cfg.SkipPath...)
		return v
	case FlakeInputShadow:
		if cfg.FlakePath != "" {
			v.FlakePath = cfg.FlakePath
		}
		if len(cfg.ScanDirs) > 0 {
			v.ScanDirs = cfg.ScanDirs
		}
		v.SkipPath = append(v.SkipPath, cfg.SkipPath...)
		v.AllowInputs = append(v.AllowInputs, cfg.Allowlist...)
		return v
	case Agenix:
		if len(cfg.ScanDirs) > 0 {
			v.ScanDirs = cfg.ScanDirs
		}
		v.SkipPath = append(v.SkipPath, cfg.SkipPath...)
		return v
	case AgentKillSwitches:
		if len(cfg.ScanDirs) > 0 {
			v.ScanDirs = cfg.ScanDirs
		}
		v.SkipPath = append(v.SkipPath, cfg.SkipPath...)
		if cfg.AgentSession != "" {
			v.AgentSession = cfg.AgentSession
		}
		if cfg.AgentService != "" {
			v.AgentService = cfg.AgentService
		}
		if len(cfg.AgentServiceAllow) > 0 {
			v.AgentServiceAllow = cfg.AgentServiceAllow
		}
		if len(cfg.AgentProcesses) > 0 {
			v.AgentProcesses = cfg.AgentProcesses
		}
		return v
	case UserSkillsParity:
		if len(cfg.Hosts) > 0 {
			v.Hosts = cfg.Hosts
		}
		return v
	case NoCrossRepoTasks:
		if len(cfg.ForbiddenSlugs) > 0 {
			v.ForbiddenSlugs = mergeStringMap(v.ForbiddenSlugs, cfg.ForbiddenSlugs)
		}
		if len(cfg.ForbiddenPaths) > 0 {
			v.ForbiddenPaths = mergeStringMap(v.ForbiddenPaths, cfg.ForbiddenPaths)
		}
		if len(cfg.SlugAllowlist) > 0 {
			if v.SlugAllowlist == nil {
				v.SlugAllowlist = map[string]bool{}
			}
			for _, s := range cfg.SlugAllowlist {
				v.SlugAllowlist[s] = true
			}
		}
		return v
	default:
		return l
	}
}

func mergeStringMap(base, over map[string]string) map[string]string {
	if base == nil {
		base = map[string]string{}
	}
	for k, v := range over {
		base[k] = v
	}
	return base
}

func mergeLintConfig(base, over LintConfig) LintConfig {
	if len(over.Allowlist) > 0 {
		base.Allowlist = over.Allowlist
	}
	if over.ConsumersDir != "" {
		base.ConsumersDir = over.ConsumersDir
	}
	if over.RulesDir != "" {
		base.RulesDir = over.RulesDir
	}
	if over.RenderCmd != "" {
		base.RenderCmd = over.RenderCmd
	}
	if over.ConsumersCmd != "" {
		base.ConsumersCmd = over.ConsumersCmd
	}
	if over.Limit > 0 {
		base.Limit = over.Limit
	}
	if len(over.Ext) > 0 {
		base.Ext = over.Ext
	}
	if len(over.SkipPath) > 0 {
		base.SkipPath = over.SkipPath
	}
	if over.RootLineLimit > 0 {
		base.RootLineLimit = over.RootLineLimit
	}
	if over.RootCharLimit > 0 {
		base.RootCharLimit = over.RootCharLimit
	}
	if over.SubdirLineLimit > 0 {
		base.SubdirLineLimit = over.SubdirLineLimit
	}
	if over.FlakePath != "" {
		base.FlakePath = over.FlakePath
	}
	if len(over.ScanDirs) > 0 {
		base.ScanDirs = over.ScanDirs
	}
	if len(over.Hosts) > 0 {
		base.Hosts = over.Hosts
	}
	if over.AgentSession != "" {
		base.AgentSession = over.AgentSession
	}
	if over.AgentService != "" {
		base.AgentService = over.AgentService
	}
	if len(over.AgentServiceAllow) > 0 {
		base.AgentServiceAllow = over.AgentServiceAllow
	}
	if len(over.AgentProcesses) > 0 {
		base.AgentProcesses = over.AgentProcesses
	}
	if len(over.ForbiddenSlugs) > 0 {
		base.ForbiddenSlugs = over.ForbiddenSlugs
	}
	if len(over.ForbiddenPaths) > 0 {
		base.ForbiddenPaths = over.ForbiddenPaths
	}
	if len(over.SlugAllowlist) > 0 {
		base.SlugAllowlist = over.SlugAllowlist
	}
	return base
}

func setLintConfigValue(cfg *LintConfig, key, raw string, lineNum int) error {
	switch key {
	case "allowlist":
		v, err := parseStringList(raw)
		cfg.Allowlist = v
		return errAt(lineNum, key, err)
	case "consumers_dir":
		cfg.ConsumersDir = sporetoml.StripQuotes(raw)
	case "rules_dir":
		cfg.RulesDir = sporetoml.StripQuotes(raw)
	case "render_cmd":
		cfg.RenderCmd = sporetoml.StripQuotes(raw)
	case "consumers_cmd":
		cfg.ConsumersCmd = sporetoml.StripQuotes(raw)
	case "limit":
		v, err := strconv.Atoi(sporetoml.StripQuotes(raw))
		if err != nil {
			return fmt.Errorf("line %d: %s must be an integer", lineNum, key)
		}
		cfg.Limit = v
	case "ext":
		v, err := parseStringList(raw)
		cfg.Ext = v
		return errAt(lineNum, key, err)
	case "skip_path":
		v, err := parseStringList(raw)
		cfg.SkipPath = v
		return errAt(lineNum, key, err)
	case "root_line_limit":
		v, err := strconv.Atoi(sporetoml.StripQuotes(raw))
		if err != nil {
			return fmt.Errorf("line %d: %s must be an integer", lineNum, key)
		}
		cfg.RootLineLimit = v
	case "root_char_limit":
		v, err := strconv.Atoi(sporetoml.StripQuotes(raw))
		if err != nil {
			return fmt.Errorf("line %d: %s must be an integer", lineNum, key)
		}
		cfg.RootCharLimit = v
	case "subdir_line_limit":
		v, err := strconv.Atoi(sporetoml.StripQuotes(raw))
		if err != nil {
			return fmt.Errorf("line %d: %s must be an integer", lineNum, key)
		}
		cfg.SubdirLineLimit = v
	case "flake_path":
		cfg.FlakePath = sporetoml.StripQuotes(raw)
	case "scan_dirs":
		v, err := parseStringList(raw)
		cfg.ScanDirs = v
		return errAt(lineNum, key, err)
	case "hosts":
		v, err := parseStringList(raw)
		cfg.Hosts = v
		return errAt(lineNum, key, err)
	case "agent_session":
		cfg.AgentSession = sporetoml.StripQuotes(raw)
	case "agent_service":
		cfg.AgentService = sporetoml.StripQuotes(raw)
	case "agent_service_allow":
		v, err := parseStringList(raw)
		cfg.AgentServiceAllow = v
		return errAt(lineNum, key, err)
	case "agent_processes":
		v, err := parseStringList(raw)
		cfg.AgentProcesses = v
		return errAt(lineNum, key, err)
	case "forbidden_slugs":
		m, err := parseStringMap(raw)
		cfg.ForbiddenSlugs = m
		return errAt(lineNum, key, err)
	case "forbidden_paths":
		m, err := parseStringMap(raw)
		cfg.ForbiddenPaths = m
		return errAt(lineNum, key, err)
	case "slug_allowlist":
		v, err := parseStringList(raw)
		cfg.SlugAllowlist = v
		return errAt(lineNum, key, err)
	default:
		return fmt.Errorf("line %d: unknown key %q in [lint.*]", lineNum, key)
	}
	return nil
}

// parseStringMap parses a TOML-ish list of "key=value" pairs into a
// map. Each element is split on the first '=', both sides trimmed.
// Used for lint configs that need labelled maps (e.g. slug-prefix to
// target-project) without a real TOML table parser.
func parseStringMap(raw string) (map[string]string, error) {
	list, err := parseStringList(raw)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, entry := range list {
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			return nil, fmt.Errorf("expected key=value, got %q", entry)
		}
		key := strings.TrimSpace(entry[:eq])
		val := strings.TrimSpace(entry[eq+1:])
		if key == "" {
			return nil, fmt.Errorf("empty key in %q", entry)
		}
		out[key] = val
	}
	return out, nil
}

func errAt(lineNum int, key string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("line %d: %s: %w", lineNum, key, err)
}

func parseStringList(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") {
		if !strings.HasSuffix(raw, "]") {
			return nil, fmt.Errorf("unterminated array")
		}
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
		if raw == "" {
			return nil, nil
		}
		return sporetoml.SplitList(raw), nil
	}
	return sporetoml.SplitList(sporetoml.StripQuotes(raw)), nil
}
