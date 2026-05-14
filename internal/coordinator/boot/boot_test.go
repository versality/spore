package boot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeExec returns an Exec stub backed by a name->(rc, out) lookup.
// Calls to unknown commands return rc=127 so the tests fail loudly
// rather than silently swallowing a missed probe.
func fakeExec(table map[string][2]any) func(string, ...string) (int, string) {
	return func(name string, args ...string) (int, string) {
		key := name
		if len(args) > 0 {
			key = name + " " + strings.Join(args, " ")
		}
		if v, ok := table[key]; ok {
			return v[0].(int), v[1].(string)
		}
		// Fall back to a name-only lookup so callers can stub by bare
		// command without enumerating every arg combo.
		if v, ok := table[name]; ok {
			return v[0].(int), v[1].(string)
		}
		return 127, key + ": not in fake table"
	}
}

func baseCfg(t *testing.T) Config {
	dir := t.TempDir()
	return Config{
		StateDir: dir,
		WTState:  filepath.Join(dir, "wt"),
		Root:     filepath.Join(dir, "root"),
		LineCap:  80,
		ByteCap:  8192,
		SLACap:   3,
		Now:      func() time.Time { return time.Date(2026, 5, 14, 9, 0, 0, 0, time.UTC) },
		Hostname: func() string { return "test-host" },
		SelfExe:  "spore",
	}
}

func TestRunHealthyAllOK(t *testing.T) {
	cfg := baseCfg(t)
	state := filepath.Join(cfg.StateDir, "state.md")
	if err := os.WriteFile(state, []byte("# state\nactive: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\tSTATUS\tTITLE\nfoo\tactive\tA foo\n"},
		"wt task fleet status":   {0, "live=2 parked=0\n"},
		"wt task agents status":  {0, "on\n"},
		"skyhelm-budget summary": {0, "tokens 1000/1M\n"},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok\n"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok\n"},
		"spore coordinator state-debt":                                    {0, ""},
	})

	r := Run(cfg)
	if r.WorstRC != 0 {
		t.Fatalf("worst=%d body=%q", r.WorstRC, r.Body)
	}
	if !strings.Contains(r.Body, "## state.md (inline)") {
		t.Errorf("missing inline state.md section:\n%s", r.Body)
	}
	if !strings.Contains(r.Body, "# state") {
		t.Errorf("inline body missing:\n%s", r.Body)
	}
	if !strings.Contains(r.Body, "(first boot, snapshot established; 1 tasks)") {
		t.Errorf("first-boot label missing:\n%s", r.Body)
	}
	if !strings.Contains(r.Body, "\nok: agents on") {
		t.Errorf("ok rollup missing 'agents on':\n%s", r.Body)
	}
	for _, want := range []string{"opencode liveness", "spore monitor", "state-debt", "idle watchdog", "worker stop errors", "comm-feedback (no)"} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("ok rollup missing %q:\n%s", want, r.Body)
		}
	}
	snap := filepath.Join(cfg.StateDir, "last-boot-tasks.txt")
	if _, err := os.Stat(snap); err != nil {
		t.Errorf("snapshot not written: %v", err)
	}
}

func TestRunOversizedStateMdTripsExit2(t *testing.T) {
	cfg := baseCfg(t)
	cfg.LineCap = 3
	state := filepath.Join(cfg.StateDir, "state.md")
	body := "L1\nL2\nL3\nL4\nL5\n"
	if err := os.WriteFile(state, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\tSTATUS\n"},
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "on\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})

	r := Run(cfg)
	if r.WorstRC != 2 {
		t.Fatalf("worst=%d body=%q", r.WorstRC, r.Body)
	}
	if !strings.Contains(r.Body, "[exit=2]") {
		t.Errorf("state.md header missing [exit=2]:\n%s", r.Body)
	}
	if !strings.Contains(r.Body, "oversized: lines=5>cap=3") {
		t.Errorf("oversized line missing:\n%s", r.Body)
	}
}

func TestRunMissingStateMd(t *testing.T) {
	cfg := baseCfg(t)
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\n"},
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "on\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})
	r := Run(cfg)
	if r.WorstRC != 0 {
		t.Fatalf("missing state.md should not trip exit; got %d", r.WorstRC)
	}
	if !strings.Contains(r.Body, "(missing - create from template before reconciling)") {
		t.Errorf("missing-state notice absent:\n%s", r.Body)
	}
	if strings.Contains(r.Body, "## state.md (inline)") {
		t.Errorf("inline section should be omitted when state.md missing:\n%s", r.Body)
	}
}

func TestRunTaskLSDiff(t *testing.T) {
	cfg := baseCfg(t)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Pre-seed snapshot to force the diff branch.
	snap := filepath.Join(cfg.StateDir, "last-boot-tasks.txt")
	if err := os.WriteFile(snap, []byte("alpha\tactive\nbeta\tparked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\tSTATUS\nalpha\tdone\ngamma\tactive\n"},
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "off\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})
	r := Run(cfg)
	if r.WorstRC != 0 {
		t.Fatalf("worst=%d", r.WorstRC)
	}
	for _, want := range []string{"added:", "  gamma\tactive", "removed:", "  beta\t(was parked)", "status changed:", "  alpha\tactive -> done"} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("diff missing %q:\n%s", want, r.Body)
		}
	}
	if !strings.Contains(r.Body, "agents off (auto-promote disabled)") {
		t.Errorf("agents-off rollup missing:\n%s", r.Body)
	}
}

func TestRunSLAOverCapEmitsFooterAndSidecar(t *testing.T) {
	cfg := baseCfg(t)
	cfg.SLACap = 2
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	slaOut := "stale: a\nstale: b\nstale: c\nstale: d\n"
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\n"},
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "on\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {2, slaOut},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})
	r := Run(cfg)
	if r.WorstRC != 2 {
		t.Fatalf("worst=%d", r.WorstRC)
	}
	if !strings.Contains(r.Body, "stale: a\nstale: b\n(2 more, full at ") {
		t.Errorf("SLA cap not applied:\n%s", r.Body)
	}
	sidecar := filepath.Join(cfg.StateDir, "sla-scan-latest.txt")
	got, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("sidecar read: %v", err)
	}
	if string(got) != slaOut {
		t.Errorf("sidecar mismatch:\nwant=%q\ngot=%q", slaOut, got)
	}
}

func TestRunCommFeedbackPresentEmitsSection(t *testing.T) {
	cfg := baseCfg(t)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(cfg.StateDir, "comm-feedback.ready")
	if err := os.WriteFile(ready, []byte("operator: tone is curt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\n"},
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "on\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})
	r := Run(cfg)
	if !strings.Contains(r.Body, "## comm-feedback.ready") {
		t.Errorf("comm-feedback section missing:\n%s", r.Body)
	}
	if !strings.Contains(r.Body, "operator: tone is curt") {
		t.Errorf("comm-feedback body missing:\n%s", r.Body)
	}
	if strings.Contains(r.Body, "comm-feedback (no)") {
		t.Errorf("ok-rollup should not list comm-feedback when present:\n%s", r.Body)
	}
}

func TestRunWorkerStopErrorsTailEmits(t *testing.T) {
	cfg := baseCfg(t)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.WTState, 0o755); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(cfg.WTState, "worker-stop-errors.jsonl")
	body := strings.Repeat(`{"slug":"x","err":"boom"}`+"\n", 7)
	if err := os.WriteFile(ledger, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\n"},
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "on\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})
	r := Run(cfg)
	if r.WorstRC != 2 {
		t.Fatalf("worst=%d body=%s", r.WorstRC, r.Body)
	}
	if !strings.Contains(r.Body, "unresolved worker Stop hook errors from") {
		t.Errorf("worker-stop-errors section absent:\n%s", r.Body)
	}
	// Tail-5 contract: must not contain a sixth line.
	count := strings.Count(r.Body, `{"slug":"x","err":"boom"}`)
	if count != 5 {
		t.Errorf("expected 5 tailed lines, got %d:\n%s", count, r.Body)
	}
}

func TestRunWorstExitCapped(t *testing.T) {
	cfg := baseCfg(t)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {99, "boom\n"}, // unusually high rc
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "on\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})
	r := Run(cfg)
	if r.WorstRC != 2 {
		t.Fatalf("worst should be capped at 2, got %d", r.WorstRC)
	}
}

func TestRunAgentsStateFailureSurfacedAsSection(t *testing.T) {
	cfg := baseCfg(t)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\n"},
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "weird\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})
	r := Run(cfg)
	if r.WorstRC != 2 {
		t.Fatalf("worst=%d", r.WorstRC)
	}
	if !strings.Contains(r.Body, "## wt task agents [exit=2]") {
		t.Errorf("agents failure section missing:\n%s", r.Body)
	}
	if !strings.Contains(r.Body, "failed: unexpected wt task agents status: weird") {
		t.Errorf("agents diagnostic missing:\n%s", r.Body)
	}
}

func TestRunReconcileHealthDirtyMainSurfacesSection(t *testing.T) {
	cfg := baseCfg(t)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.WTState, 0o755); err != nil {
		t.Fatal(err)
	}
	// Snapshot timestamp is the boot clock minus 30s; well inside the
	// 5-minute stale window, so only the dirty-main finding fires.
	healthPath := filepath.Join(cfg.WTState, "reconcile-health.json")
	body := `{
  "ts": "2026-05-14T08:59:30Z",
  "version": 1,
  "projects": {
    "spore": {"status": "dirty-main", "dirty_files": [" M cmd/spore/main.go"], "skipped_slugs": ["slug-a"]}
  }
}`
	if err := os.WriteFile(healthPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\n"},
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "on\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})
	r := Run(cfg)
	if r.WorstRC != 2 {
		t.Fatalf("worst=%d, want 2; body=%s", r.WorstRC, r.Body)
	}
	if !strings.Contains(r.Body, "## reconcile health [exit=2]") {
		t.Errorf("reconcile-health section header missing:\n%s", r.Body)
	}
	if !strings.Contains(r.Body, "dirty-main spore") {
		t.Errorf("dirty-main line missing:\n%s", r.Body)
	}
	if strings.Contains(r.Body, "\nok: ") && strings.Contains(r.Body, "reconcile health,") {
		t.Errorf("reconcile health should not appear in ok rollup when dirty:\n%s", r.Body)
	}
}

func TestRunReconcileHealthMissingFileSilent(t *testing.T) {
	cfg := baseCfg(t)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No reconcile-health.json on disk. Probe must roll up silently.
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\n"},
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "on\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})
	r := Run(cfg)
	if r.WorstRC != 0 {
		t.Fatalf("worst=%d, want 0 when file missing", r.WorstRC)
	}
	if strings.Contains(r.Body, "## reconcile health") {
		t.Errorf("section should be silent when file missing:\n%s", r.Body)
	}
	if !strings.Contains(r.Body, "reconcile health") {
		t.Errorf("ok rollup should still list 'reconcile health':\n%s", r.Body)
	}
}

func TestRunReconcileHealthStaleFiresSection(t *testing.T) {
	cfg := baseCfg(t)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.WTState, 0o755); err != nil {
		t.Fatal(err)
	}
	healthPath := filepath.Join(cfg.WTState, "reconcile-health.json")
	// Snapshot is 30 minutes old vs the boot clock; well past the
	// 5-minute stale window.
	body := `{"ts":"2026-05-14T08:30:00Z","version":1,"projects":{"spore":{"status":"ok"}}}`
	if err := os.WriteFile(healthPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\n"},
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "on\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})
	r := Run(cfg)
	if r.WorstRC != 2 {
		t.Fatalf("worst=%d, want 2 on stale", r.WorstRC)
	}
	if !strings.Contains(r.Body, "## reconcile health [exit=2]") {
		t.Errorf("reconcile-health section header missing:\n%s", r.Body)
	}
	if !strings.Contains(r.Body, "stale") {
		t.Errorf("stale finding missing:\n%s", r.Body)
	}
}

func TestRunReconcileHealthFleetDisabledSilent(t *testing.T) {
	cfg := baseCfg(t)
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "state.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.WTState, 0o755); err != nil {
		t.Fatal(err)
	}
	healthPath := filepath.Join(cfg.WTState, "reconcile-health.json")
	// fleet_disabled suppresses dirty-main as expected behaviour.
	body := `{
  "ts": "2026-05-14T08:59:30Z",
  "version": 1,
  "fleet_disabled": true,
  "projects": {"spore": {"status": "dirty-main", "dirty_files": ["a"]}}
}`
	if err := os.WriteFile(healthPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Exec = fakeExec(map[string][2]any{
		"wt task ls":             {0, "SLUG\n"},
		"wt task fleet status":   {0, ""},
		"wt task agents status":  {0, "off\n"},
		"skyhelm-budget summary": {0, ""},
		"skyhelm-sla-scanner":    {0, ""},
		"skyhelm-idle-watchdog":  {0, "ok"},
		filepath.Join(cfg.Root, "harness", "opencode-worker-liveness.sh"): {0, ""},
		"spore coordinator monitor":                                       {0, "ok"},
		"spore coordinator state-debt":                                    {0, ""},
	})
	r := Run(cfg)
	if r.WorstRC != 0 {
		t.Fatalf("worst=%d, want 0 under fleet_disabled; body=%s", r.WorstRC, r.Body)
	}
	if strings.Contains(r.Body, "## reconcile health [exit=2]") {
		t.Errorf("reconcile-health should not surface incident under fleet_disabled:\n%s", r.Body)
	}
}
