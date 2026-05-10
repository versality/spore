package secret

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type AuditConfig struct {
	Repo        string
	SecretsDir  string
	NixGlobs    []string
	ExtraGlobs  []string
	ExcludeDirs []string
	Stdout      io.Writer
}

type Finding struct {
	File       string   `json:"file"`
	Registered bool     `json:"registered"`
	OnDisk     bool     `json:"on_disk"`
	Consumers  []string `json:"consumers"`
}

type AuditResult struct {
	Findings []Finding `json:"findings"`
	Clean    bool      `json:"clean"`
}

var declaredRe = regexp.MustCompile(`"([^"]+\.age)"\.publicKeys`)

func Audit(cfg AuditConfig) (AuditResult, error) {
	repo := cfg.Repo
	if repo == "" {
		repo = "."
	}
	repoAbs, err := filepath.Abs(repo)
	if err != nil {
		return AuditResult{}, fmt.Errorf("resolve repo: %w", err)
	}
	secretsDir := cfg.SecretsDir
	if secretsDir == "" {
		secretsDir = "secrets"
	}
	secretsAbs := filepath.Join(repoAbs, secretsDir)
	manifestPath := filepath.Join(secretsAbs, "secrets.nix")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return AuditResult{}, fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}

	declared := map[string]bool{}
	for _, m := range declaredRe.FindAllStringSubmatch(string(manifestBytes), -1) {
		declared[m[1]] = true
	}

	onDisk := map[string]bool{}
	entries, err := os.ReadDir(secretsAbs)
	if err != nil {
		return AuditResult{}, fmt.Errorf("read secrets dir %s: %w", secretsAbs, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".age") {
			onDisk[name] = true
		}
	}

	all := map[string]bool{}
	for n := range declared {
		all[n] = true
	}
	for n := range onDisk {
		all[n] = true
	}
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)

	scanRoots := cfg.NixGlobs
	if len(scanRoots) == 0 {
		scanRoots = []string{"nix", "bash"}
	}
	excludeDirs := cfg.ExcludeDirs
	if excludeDirs == nil {
		excludeDirs = []string{secretsDir, "templates"}
	}
	scanFiles, err := collectScanFiles(repoAbs, scanRoots, excludeDirs)
	if err != nil {
		return AuditResult{}, err
	}

	result := AuditResult{Clean: true}
	for _, f := range names {
		base := strings.TrimSuffix(f, ".age")
		nameRe, err := regexp.Compile(`\b` + regexp.QuoteMeta(base) + `\b`)
		if err != nil {
			return AuditResult{}, fmt.Errorf("compile name regex for %s: %w", base, err)
		}
		consumers := matchingFiles(scanFiles, repoAbs, nameRe)
		fnd := Finding{
			File:       f,
			Registered: declared[f],
			OnDisk:     onDisk[f],
			Consumers:  consumers,
		}
		if !fnd.Registered || !fnd.OnDisk || len(fnd.Consumers) == 0 {
			result.Clean = false
		}
		result.Findings = append(result.Findings, fnd)
	}

	return result, nil
}

func collectScanFiles(repo string, roots, excludeDirs []string) ([]string, error) {
	exclude := map[string]bool{}
	for _, d := range excludeDirs {
		exclude[filepath.Clean(d)] = true
	}
	var files []string
	for _, root := range roots {
		rootAbs := filepath.Join(repo, root)
		st, err := os.Stat(rootAbs)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat %s: %w", rootAbs, err)
		}
		if !st.IsDir() {
			continue
		}
		err = filepath.WalkDir(rootAbs, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, rerr := filepath.Rel(repo, path)
			if rerr != nil {
				return rerr
			}
			if d.IsDir() {
				if exclude[filepath.Clean(rel)] {
					return filepath.SkipDir
				}
				return nil
			}
			if root == "nix" && !strings.HasSuffix(path, ".nix") {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", rootAbs, err)
		}
	}
	return files, nil
}

func matchingFiles(files []string, repo string, re *regexp.Regexp) []string {
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if re.Match(b) {
			rel, err := filepath.Rel(repo, f)
			if err != nil {
				rel = f
			}
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

func WriteAuditTable(w io.Writer, r AuditResult) {
	fmt.Fprintf(w, "%-42s  %-10s  %-10s  %s\n", "FILE", "REGISTERED", "ONDISK", "CONSUMERS")
	fmt.Fprintf(w, "%-42s  %-10s  %-10s  %s\n", "----", "----------", "------", "---------")
	for _, f := range r.Findings {
		reg := "no"
		if f.Registered {
			reg = "yes"
		}
		disk := "no"
		if f.OnDisk {
			disk = "yes"
		}
		consumers := "(none)"
		if len(f.Consumers) > 0 {
			consumers = strings.Join(f.Consumers, ",")
		}
		fmt.Fprintf(w, "%-42s  %-10s  %-10s  %s\n", f.File, reg, disk, consumers)
	}
}
