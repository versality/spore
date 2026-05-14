// Package boot ports harness/skyhelm-boot (bash) to Go. It runs the
// skyhelm cold-boot probes in parallel and emits one structured
// summary that the agent reads in a single tool call. The state.md
// body is inlined between the size header and the first probe section
// so the agent doesn't have to spend a second Read on bytes the
// summary already carries.
//
// Parity with the bash wrapper is the explicit contract: same probe
// set, same section ordering, same silent-on-ok rollup, same SLA cap,
// same `min(2, max(rc))` exit semantics. The one functional addition
// is the inline state.md section.
package boot

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultLineCap = 80
	DefaultByteCap = 8192
	DefaultSLACap  = 3
)

// Config captures the boot wrapper's tunables. Empty fields fall back
// to env / sensible defaults via defaults(). Exec, Now, Hostname, and
// SelfExe are injectable seams the tests use to avoid shelling out.
type Config struct {
	StateDir string
	WTState  string
	Root     string // project root, used to find harness/opencode-worker-liveness.sh
	LineCap  int
	ByteCap  int
	SLACap   int

	Now      func() time.Time
	Hostname func() string
	Exec     func(name string, args ...string) (rc int, out string)
	SelfExe  string
}

// Result bundles the rendered body, the per-probe outcomes (for
// tests), and the capped worst exit code the CLI returns.
type Result struct {
	Body    string
	WorstRC int
	Probes  []ProbeResult
}

func (c Config) defaults() Config {
	if c.StateDir == "" {
		c.StateDir = os.Getenv("SKYHELM_STATE_DIR")
	}
	if c.StateDir == "" {
		home, _ := os.UserHomeDir()
		c.StateDir = filepath.Join(home, ".local", "state", "skyhelm")
	}
	if c.WTState == "" {
		c.WTState = os.Getenv("WT_STATE")
	}
	if c.WTState == "" {
		home, _ := os.UserHomeDir()
		c.WTState = filepath.Join(home, ".local", "state", "wt")
	}
	if c.Root == "" {
		c.Root = os.Getenv("SKYHELM_HARNESS_ROOT")
	}
	if c.Root == "" {
		c.Root, _ = os.Getwd()
	}
	if c.LineCap == 0 {
		c.LineCap = envIntOr("SKYHELM_STATE_LINE_CAP", DefaultLineCap)
	}
	if c.ByteCap == 0 {
		c.ByteCap = envIntOr("SKYHELM_STATE_BYTE_CAP", DefaultByteCap)
	}
	if c.SLACap == 0 {
		c.SLACap = envIntOr("SKYHELM_SLA_PRINT_CAP", DefaultSLACap)
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	if c.Hostname == nil {
		c.Hostname = defaultHostname
	}
	if c.Exec == nil {
		c.Exec = defaultExec
	}
	if c.SelfExe == "" {
		if exe, err := os.Executable(); err == nil {
			c.SelfExe = exe
		} else {
			c.SelfExe = "spore"
		}
	}
	return c
}

// Run executes the boot probes and returns the rendered summary plus
// the worst capped exit code. State.md inspection runs synchronously
// because its body is inlined into the header section; remaining
// probes fan out across goroutines.
func Run(cfg Config) Result {
	cfg = cfg.defaults()
	_ = os.MkdirAll(cfg.StateDir, 0o755)

	state := inspectState(cfg)
	probes := runProbes(cfg)

	body := render(cfg, state, probes)
	worst := state.RC
	for _, p := range probes {
		if p.RC > worst {
			worst = p.RC
		}
	}
	if worst > 2 {
		worst = 2
	}
	return Result{Body: body, WorstRC: worst, Probes: probes}
}

func defaultHostname() string {
	if h, err := os.Hostname(); err == nil {
		if i := strings.IndexByte(h, '.'); i >= 0 {
			h = h[:i]
		}
		return h
	}
	return "unknown"
}

func defaultExec(name string, args ...string) (int, string) {
	if _, err := exec.LookPath(name); err != nil {
		return 127, fmt.Sprintf("%s: not found in PATH", name)
	}
	out, err := exec.Command(name, args...).CombinedOutput()
	rc := 0
	if err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			rc = ee.ExitCode()
		} else {
			rc = 1
		}
	}
	return rc, string(out)
}

func asExit(err error, target **exec.ExitError) bool {
	for e := err; e != nil; {
		if ee, ok := e.(*exec.ExitError); ok {
			*target = ee
			return true
		}
		type unwrap interface{ Unwrap() error }
		if u, ok := e.(unwrap); ok {
			e = u.Unwrap()
		} else {
			return false
		}
	}
	return false
}

func envIntOr(name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// runProbes fans out every non-state probe onto a goroutine and
// collects the results. Probe order in the slice matches the render
// order; the goroutines only parallelise the work, not the layout.
func runProbes(cfg Config) []ProbeResult {
	defs := probeDefs(cfg)
	results := make([]ProbeResult, len(defs))
	var wg sync.WaitGroup
	for i, d := range defs {
		wg.Add(1)
		go func(i int, d probeDef) {
			defer wg.Done()
			rc, out := d.Run()
			results[i] = ProbeResult{
				Name:    d.Name,
				Title:   d.Title,
				Mode:    d.Mode,
				OKShort: d.OKShort,
				RC:      rc,
				Out:     out,
			}
		}(i, d)
	}
	wg.Wait()
	return results
}
