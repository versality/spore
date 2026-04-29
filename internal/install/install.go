// Package install drops the spore skill bodies into a target
// project's .claude/skills/ directory so claude-code in that project
// can discover and run them. The skill source ships with the spore
// binary via embed.FS (see embed.go), so this works under `nix run`
// without a source-tree checkout.
package install

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Result records what a single Install call did.
type Result struct {
	// Written is the list of destination files that were created or
	// updated, in walk order.
	Written []string
	// Skipped is the list of destination files whose contents already
	// matched the embedded copy, so no write was needed.
	Skipped []string
}

// Install copies every embedded skill under <prefix>/<name>/ into
// <root>/.claude/skills/<name>/. Files whose content begins with `#!`
// are written 0755; the rest are 0644. Files named `.gitkeep` are
// dropped (they exist only to keep the source tree tidy).
//
// Install is idempotent: re-running it with no source changes is a
// no-op. If a destination file exists with different content, Install
// overwrites it.
func Install(root string, src embed.FS, prefix string) (Result, error) {
	var res Result
	if root == "" {
		return res, errors.New("install: root is empty")
	}

	err := fs.WalkDir(src, prefix, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(prefix, p)
		if err != nil {
			return err
		}
		if filepath.Base(rel) == ".gitkeep" {
			return nil
		}
		body, err := src.ReadFile(p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		mode := os.FileMode(0o644)
		if bytes.HasPrefix(body, []byte("#!")) {
			mode = 0o755
		}
		dest := filepath.Join(root, ".claude", "skills", rel)
		if existing, err := os.ReadFile(dest); err == nil && bytes.Equal(existing, body) {
			res.Skipped = append(res.Skipped, dest)
			if err := os.Chmod(dest, mode); err != nil {
				return fmt.Errorf("chmod %s: %w", dest, err)
			}
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), err)
		}
		if err := os.WriteFile(dest, body, mode); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
		res.Written = append(res.Written, dest)
		return nil
	})
	if err != nil {
		return res, err
	}
	return res, nil
}
