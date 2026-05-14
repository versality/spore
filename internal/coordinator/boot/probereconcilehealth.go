package boot

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/versality/spore/internal/coordinator/reconcilehealth"
)

// probeReconcileHealth reads $WT_STATE/reconcile-health.json (written
// by the wt-go reconciler each tick) and renders any incidents the
// boot summary should surface. Silent-on-ok semantics: empty body
// rolls into the trailing `ok: ...` line; incident lines lift the
// probe section into the body so the operator sees the wedge before
// the fleet idles for it.
func probeReconcileHealth(cfg Config) (int, string) {
	path := filepath.Join(cfg.WTState, "reconcile-health.json")
	h, err := reconcilehealth.Read(path)
	if err != nil {
		return 2, fmt.Sprintf("failed: reconcile-health parse %s: %v\n", path, err)
	}
	findings, rc := reconcilehealth.Verdict(h, cfg.Now(), reconcilehealth.DefaultStaleAfter)
	if rc == 0 {
		return 0, ""
	}
	var b strings.Builder
	for _, line := range findings {
		fmt.Fprintln(&b, line)
	}
	return rc, b.String()
}
