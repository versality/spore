package task

import (
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path via a sibling temp file followed by
// rename. The visible state at path is either the old contents or the
// new contents, never a truncated half-write from a crashed process
// or an interleave from a concurrent writer. Use for frontmatter
// rewrites of tasks/<slug>.md and similar single-file documents.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	f, err := os.CreateTemp(dir, "."+base+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
