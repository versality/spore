package task

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Pick presents an interactive picker (rofi or fzf) over non-done
// tasks in tasksDir and returns the selected slug. Returns an error
// when no picker is available or the user cancels.
func Pick(tasksDir string) (string, error) {
	metas, err := List(tasksDir)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, m := range metas {
		if m.Status == "done" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", m.Slug, m.Status, m.Title))
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("no tasks to pick from")
	}
	input := strings.Join(lines, "\n") + "\n"

	picker, pickerArgs, err := detectPicker()
	if err != nil {
		return "", err
	}

	cmd := exec.Command(picker, pickerArgs...)
	cmd.Stdin = bytes.NewBufferString(input)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("picker cancelled or failed: %w", err)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", fmt.Errorf("no selection")
	}
	slug, _, _ := strings.Cut(line, "\t")
	return slug, nil
}

func detectPicker() (string, []string, error) {
	hasDisplay := os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	if hasDisplay {
		if p, err := exec.LookPath("rofi"); err == nil {
			return p, []string{"-dmenu", "-p", "task"}, nil
		}
	}
	if p, err := exec.LookPath("fzf"); err == nil {
		return p, []string{"--prompt", "task> "}, nil
	}
	return "", nil, fmt.Errorf("no picker available (need rofi or fzf)")
}
