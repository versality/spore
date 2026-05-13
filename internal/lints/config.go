package lints

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	ByName map[string]LintConfig
}

type LintConfig struct {
	Allowlist       []string
	ConsumersDir    string
	RulesDir        string
	RenderCmd       string
	Limit           int
	Ext             []string
	SkipPath        []string
	RootLineLimit   int
	RootCharLimit   int
	SubdirLineLimit int
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
	current := ""
	scanner := bufio.NewScanner(strings.NewReader(content))
	for lineNum := 1; scanner.Scan(); lineNum++ {
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.TrimSpace(line[1 : len(line)-1])
			current = ""
			if name, ok := strings.CutPrefix(section, "lint."); ok {
				current = strings.TrimSpace(name)
				if current == "" {
					return Config{}, fmt.Errorf("line %d: empty lint name in %q", lineNum, section)
				}
				if _, ok := cfg.ByName[current]; !ok {
					cfg.ByName[current] = LintConfig{}
				}
			}
			continue
		}
		if current == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return Config{}, fmt.Errorf("line %d: malformed entry %q", lineNum, line)
		}
		key := strings.TrimSpace(line[:eq])
		raw := strings.TrimSpace(line[eq+1:])
		lc := cfg.ByName[current]
		if err := setLintConfigValue(&lc, key, raw, lineNum); err != nil {
			return Config{}, err
		}
		cfg.ByName[current] = lc
	}
	if err := scanner.Err(); err != nil {
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
	default:
		return l
	}
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
	return base
}

func setLintConfigValue(cfg *LintConfig, key, raw string, lineNum int) error {
	switch key {
	case "allowlist":
		v, err := parseStringList(raw)
		cfg.Allowlist = v
		return errAt(lineNum, key, err)
	case "consumers_dir":
		cfg.ConsumersDir = stripQuotes(raw)
	case "rules_dir":
		cfg.RulesDir = stripQuotes(raw)
	case "render_cmd":
		cfg.RenderCmd = stripQuotes(raw)
	case "limit":
		v, err := strconv.Atoi(stripQuotes(raw))
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
		v, err := strconv.Atoi(stripQuotes(raw))
		if err != nil {
			return fmt.Errorf("line %d: %s must be an integer", lineNum, key)
		}
		cfg.RootLineLimit = v
	case "root_char_limit":
		v, err := strconv.Atoi(stripQuotes(raw))
		if err != nil {
			return fmt.Errorf("line %d: %s must be an integer", lineNum, key)
		}
		cfg.RootCharLimit = v
	case "subdir_line_limit":
		v, err := strconv.Atoi(stripQuotes(raw))
		if err != nil {
			return fmt.Errorf("line %d: %s must be an integer", lineNum, key)
		}
		cfg.SubdirLineLimit = v
	default:
		return fmt.Errorf("line %d: unknown key %q in [lint.*]", lineNum, key)
	}
	return nil
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
		return splitList(raw), nil
	}
	return splitList(stripQuotes(raw)), nil
}

func splitList(raw string) []string {
	var out []string
	var b strings.Builder
	quote := byte(0)
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
				continue
			}
			b.WriteByte(ch)
		case ch == '"' || ch == '\'':
			quote = ch
		case ch == ',':
			addListPart(&out, b.String())
			b.Reset()
		default:
			b.WriteByte(ch)
		}
	}
	addListPart(&out, b.String())
	return out
}

func addListPart(out *[]string, part string) {
	part = strings.TrimSpace(part)
	if part != "" {
		*out = append(*out, part)
	}
}

func stripComment(line string) string {
	inQuote := byte(0)
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case inQuote != 0:
			if ch == inQuote {
				inQuote = 0
			}
		case ch == '"' || ch == '\'':
			inQuote = ch
		case ch == '#':
			return line[:i]
		}
	}
	return line
}

func stripQuotes(v string) string {
	if len(v) >= 2 {
		first, last := v[0], v[len(v)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
