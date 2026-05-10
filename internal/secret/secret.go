// Package secret implements the secret-add flow: open a tmux popup so
// the operator can paste a value, encrypt it under one or more age
// recipients via the system "age" binary, and write the ciphertext
// atomically to a target path. The agent invoking the parent process
// never sees the plaintext - the popup runs interactively in the
// operator's tmux session and the bytes only ever land in tmpfs.
//
// This package is the lifted equivalent of the legacy
// nix-config/harness/secret-add.sh. The bash version baked the
// nix-config tier semantics (parsing secrets/secrets.nix to pick
// recipient keys per host class) into one script; spore stays
// consumer-agnostic and accepts recipient keys directly. Tier
// resolution belongs in the consumer's wrapper.
package secret

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Config drives Add. Recipients are merged with RecipientsFile entries
// and de-duped before encryption; at least one is required.
type Config struct {
	// Recipients is the list of raw age public keys (age1...) to encrypt
	// under. Combined with RecipientsFile entries; duplicates are dropped.
	Recipients []string

	// RecipientsFile points at a file with one age public key per line.
	// '#' starts a line comment; blank lines are ignored. Empty path
	// disables file loading.
	RecipientsFile string

	// Out is the destination .age path. The parent directory must exist.
	Out string

	// Label is shown in the tmux popup title (purely cosmetic).
	Label string

	// PopupRunner is a test seam. nil means use the default tmux popup,
	// which requires $TMUX to be set. The runner must arrange for the
	// operator-pasted bytes to land in scratchPath.
	PopupRunner func(scratchPath, label string) error

	// AgeRunner is a test seam for the encrypt step. nil means exec the
	// system "age" binary on $PATH.
	AgeRunner func(plaintext []byte, recipients []string, outPath string) error

	// Stderr receives the success line ("stored: name (N bytes)").
	// Set to io.Discard to suppress; nil also suppresses.
	Stderr io.Writer
}

// Add runs the secret-add flow: resolve recipients, validate the
// destination, open the popup, read the operator paste from the
// tmpfs scratch file, encrypt under all recipients, and write the
// ciphertext to Out. The scratch file is removed before return.
func Add(cfg Config) error {
	recipients, err := resolveRecipients(cfg.Recipients, cfg.RecipientsFile)
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		return errors.New("no recipient keys (use --recipient or --recipients-file)")
	}

	if strings.TrimSpace(cfg.Out) == "" {
		return errors.New("--out is required")
	}
	outAbs, err := filepath.Abs(cfg.Out)
	if err != nil {
		return fmt.Errorf("resolve --out: %w", err)
	}
	parent := filepath.Dir(outAbs)
	st, err := os.Stat(parent)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("--out parent dir does not exist: %s", parent)
	}

	scratchPath, cleanup, err := makeScratch()
	if err != nil {
		return err
	}
	defer cleanup()

	runPopup := cfg.PopupRunner
	if runPopup == nil {
		runPopup = defaultPopup
	}
	if err := runPopup(scratchPath, cfg.Label); err != nil {
		return fmt.Errorf("popup: %w", err)
	}

	plaintext, err := os.ReadFile(scratchPath)
	if err != nil {
		return fmt.Errorf("read scratch: %w", err)
	}
	plaintext = bytes.TrimRight(plaintext, "\n")
	if len(plaintext) == 0 {
		return errors.New("operator paste was empty")
	}

	runAge := cfg.AgeRunner
	if runAge == nil {
		runAge = ageBinaryEncrypt
	}
	if err := runAge(plaintext, recipients, outAbs); err != nil {
		return fmt.Errorf("age encrypt: %w", err)
	}

	outSt, err := os.Stat(outAbs)
	if err != nil {
		return fmt.Errorf("stat out: %w", err)
	}
	if outSt.Size() == 0 {
		return errors.New("age produced empty output")
	}
	if cfg.Stderr != nil {
		fmt.Fprintf(cfg.Stderr, "stored: %s (%d bytes)\n", filepath.Base(outAbs), outSt.Size())
	}
	return nil
}

func resolveRecipients(direct []string, file string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	add := func(k string) {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, k)
	}
	for _, k := range direct {
		add(k)
	}
	if file == "" {
		return out, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read recipients file: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		add(line)
	}
	return out, nil
}

func makeScratch() (string, func(), error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "spore-secret-*.txt")
	if err != nil {
		return "", func() {}, fmt.Errorf("create scratch: %w", err)
	}
	path := f.Name()
	if cerr := f.Close(); cerr != nil {
		os.Remove(path)
		return "", func() {}, fmt.Errorf("close scratch: %w", cerr)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		os.Remove(path)
		return "", func() {}, fmt.Errorf("chmod scratch: %w", err)
	}
	cleanup := func() { _ = os.Remove(path) }
	return path, cleanup, nil
}

func defaultPopup(scratchPath, label string) error {
	if os.Getenv("TMUX") == "" {
		return errors.New("$TMUX is unset; secret add needs a tmux session for the popup")
	}
	title := " secret "
	if strings.TrimSpace(label) != "" {
		title = " " + label + " "
	}
	inner := fmt.Sprintf(`
echo ''
echo '  Paste the value, then press Ctrl-D.'
echo ''
cat > %q
echo ''
echo '  Saved. Closing...'
sleep 0.5
`, scratchPath)
	cmd := exec.Command("tmux", "display-popup", "-w", "60", "-h", "10", "-T", title, "-E", "bash", "-c", inner)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func ageBinaryEncrypt(plaintext []byte, recipients []string, outPath string) error {
	if _, err := exec.LookPath("age"); err != nil {
		return fmt.Errorf("age binary not on $PATH: %w", err)
	}
	args := []string{"-o", outPath}
	for _, r := range recipients {
		args = append(args, "-r", r)
	}
	cmd := exec.Command("age", args...)
	cmd.Stdin = bytes.NewReader(plaintext)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}
