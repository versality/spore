// Package queueclassifier classifies the task-queue rows under
// $PROJECT/tasks for the coordinator's idle watchdog. Each row's
// frontmatter (status, scheduler, needs:) plus signals from state.md
// (Open operator questions, Recent events) and the harness notify
// thread file decide a class drawn from a small fixed set:
// runnable-promote, resume, waiting-trigger, operator-blocked,
// invalid-needs-reclassify, plus pass-through "active" / "done".
//
// Classify never returns an error: read failures and malformed
// frontmatter degrade to invalid-needs-reclassify rows so callers can
// always emit a TSV. The CLI wrapper in cmd/spore exits 0 unless it
// cannot even reach the tasks dir.
package queueclassifier

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Class names emitted by Classify. The watchdog matches on these
// literals; do not rename without coordinating with the consumer.
const (
	ClassRunnablePromote        = "runnable-promote"
	ClassResume                 = "resume"
	ClassWaitingTrigger         = "waiting-trigger"
	ClassOperatorBlocked        = "operator-blocked"
	ClassInvalidNeedsReclassify = "invalid-needs-reclassify"
	ClassActive                 = "active"
	ClassDone                   = "done"
)

// DefaultFloor is the fleet-occupancy floor used when --floor / env
// are absent or non-positive. Matches the bash default.
const DefaultFloor = 6

// Config drives Classify. Empty fields fall back to Defaults().
type Config struct {
	Project      string
	StateFile    string
	ThreadFile   string
	ActiveLive   int
	Floor        int
	BudgetAdvice string
	LocalHost    string
}

// Row is one TSV record: {Class}\t{Slug}\t{Status}\t{Reason}.
type Row struct {
	Class  string
	Slug   string
	Status string
	Reason string
}

// Defaults fills zero-value fields from env vars and stdlib defaults.
// Caller-supplied values take precedence. Floor below 1 snaps to
// DefaultFloor; BudgetAdvice outside {tighten, ration} snaps to "ok".
func (c Config) Defaults() Config {
	if c.StateFile == "" {
		c.StateFile = os.Getenv("SKYHELM_STATE_FILE")
	}
	if c.StateFile == "" {
		dir := os.Getenv("SKYHELM_STATE_DIR")
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".local", "state", "skyhelm")
		}
		c.StateFile = filepath.Join(dir, "state.md")
	}
	if c.ThreadFile == "" {
		c.ThreadFile = os.Getenv("HARNESS_NOTIFY_THREAD_FILE")
	}
	if c.ThreadFile == "" {
		dir := os.Getenv("WT_STATE")
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".local", "state", "wt")
		}
		c.ThreadFile = filepath.Join(dir, "harness-notify-threads.jsonl")
	}
	if c.Floor <= 0 {
		c.Floor = DefaultFloor
	}
	switch c.BudgetAdvice {
	case "tighten", "ration":
	default:
		c.BudgetAdvice = "ok"
	}
	if c.LocalHost == "" {
		c.LocalHost = os.Getenv("SKYHELM_LOCAL_HOST")
	}
	if c.LocalHost == "" {
		if h, err := os.Hostname(); err == nil {
			c.LocalHost = shortHost(h)
		} else {
			c.LocalHost = "unknown"
		}
	}
	return c
}

func shortHost(h string) string {
	if i := strings.IndexByte(h, '.'); i >= 0 {
		return h[:i]
	}
	return h
}

// Classify reads $Project/tasks/*.md and returns one Row per task
// scoped to LocalHost. Tasks with a host frontmatter that does not
// match LocalHost (and is non-empty) are skipped. Rows are returned
// sorted by slug.
func Classify(cfg Config) ([]Row, error) {
	cfg = cfg.Defaults()
	if cfg.Project == "" {
		return nil, errors.New("queueclassifier: project not set")
	}
	tasksDir := filepath.Join(cfg.Project, "tasks")
	st, err := os.Stat(tasksDir)
	if err != nil || !st.IsDir() {
		return nil, errors.New("queueclassifier: no tasks dir at " + tasksDir)
	}

	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return nil, err
	}

	type taskInfo struct {
		slug      string
		path      string
		fm        frontmatter
		hostMatch bool
	}
	tasks := make([]taskInfo, 0, len(entries))
	statusBySlug := map[string]string{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		if slug == "README" {
			continue
		}
		path := filepath.Join(tasksDir, e.Name())
		fm := readFrontmatter(path)
		statusBySlug[slug] = fm.fields["status"]
		hostMatch := fm.fields["host"] == "" || fm.fields["host"] == cfg.LocalHost
		tasks = append(tasks, taskInfo{slug: slug, path: path, fm: fm, hostMatch: hostMatch})
	}

	sort.Slice(tasks, func(i, j int) bool { return tasks[i].slug < tasks[j].slug })

	state := loadStateSignals(cfg.StateFile)
	threads := loadThreadSlugs(cfg.ThreadFile)

	rows := make([]Row, 0, len(tasks))
	for _, t := range tasks {
		if !t.hostMatch {
			continue
		}
		rows = append(rows, classifyOne(cfg, t.slug, t.fm, statusBySlug, state, threads))
	}
	return rows, nil
}

func classifyOne(
	cfg Config,
	slug string,
	fm frontmatter,
	statusBySlug map[string]string,
	state stateSignals,
	threads map[string]bool,
) Row {
	status := fm.fields["status"]
	scheduler := fm.fields["scheduler"]

	var missing, unsatisfied []string
	for _, need := range fm.needs {
		if _, ok := statusBySlug[need]; !ok {
			missing = append(missing, need)
		} else if statusBySlug[need] != "done" {
			unsatisfied = append(unsatisfied, need)
		}
	}
	if len(missing) > 0 {
		return Row{
			Class:  ClassInvalidNeedsReclassify,
			Slug:   slug,
			Status: status,
			Reason: "missing-needs:" + strings.Join(missing, ","),
		}
	}
	unsatCSV := strings.Join(unsatisfied, ",")
	hasNeeds := len(fm.needs) > 0

	switch status {
	case "active":
		return Row{ClassActive, slug, status, "already-active"}
	case "done":
		return Row{ClassDone, slug, status, "closed"}
	case "draft":
		switch {
		case unsatCSV != "":
			return Row{ClassWaitingTrigger, slug, status, "needs:" + unsatCSV}
		case cfg.ActiveLive >= cfg.Floor:
			return Row{ClassWaitingTrigger, slug, status, "fleet-at-floor"}
		case cfg.BudgetAdvice == "tighten" || cfg.BudgetAdvice == "ration":
			return Row{ClassWaitingTrigger, slug, status, "budget:" + cfg.BudgetAdvice}
		default:
			return Row{ClassRunnablePromote, slug, status, "draft-ready"}
		}
	case "parked":
		return classifyScheduled(cfg, slug, status, scheduler, unsatCSV, hasNeeds, true)
	case "paused":
		return classifyScheduled(cfg, slug, status, scheduler, unsatCSV, hasNeeds, false)
	case "blocked":
		if state.openQuestion[slug] || state.recentOperatorNotice[slug] || threads[slug] {
			return Row{ClassOperatorBlocked, slug, status, "operator-owner-present"}
		}
		return Row{ClassInvalidNeedsReclassify, slug, status, "blocked-without-operator-owner"}
	default:
		return Row{ClassInvalidNeedsReclassify, slug, status, "invalid-status"}
	}
}

// classifyScheduled handles the parked + paused branches, which share
// gating logic. parked (gateFloor=true) is also subject to fleet-floor
// gating; paused is not (the work was already running).
func classifyScheduled(
	cfg Config,
	slug, status, scheduler, unsatCSV string,
	hasNeeds, gateFloor bool,
) Row {
	promoteClass := ClassResume
	satisfiedReason := "scheduler-satisfied"
	if gateFloor {
		promoteClass = ClassRunnablePromote
	}

	switch {
	case unsatCSV != "":
		return Row{ClassWaitingTrigger, slug, status, "needs:" + unsatCSV}
	case schedulerOperatorOwned(scheduler):
		return Row{ClassOperatorBlocked, slug, status, "scheduler-operator-owned"}
	case schedulerWaitingTrigger(scheduler):
		return Row{ClassWaitingTrigger, slug, status, "scheduler-pending"}
	case schedulerTriggerSatisfied(scheduler, hasNeeds):
		if gateFloor && cfg.ActiveLive >= cfg.Floor {
			return Row{ClassWaitingTrigger, slug, status, "fleet-at-floor"}
		}
		if cfg.BudgetAdvice == "tighten" || cfg.BudgetAdvice == "ration" {
			return Row{ClassWaitingTrigger, slug, status, "budget:" + cfg.BudgetAdvice}
		}
		return Row{promoteClass, slug, status, satisfiedReason}
	default:
		return Row{ClassWaitingTrigger, slug, status, "scheduler-pending"}
	}
}

// FormatTSV renders rows as `{class}\t{slug}\t{status}\t{reason}\n`,
// one per line. Returns an empty string for an empty slice (no
// trailing newline).
func FormatTSV(rows []Row) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.Class)
		b.WriteByte('\t')
		b.WriteString(r.Slug)
		b.WriteByte('\t')
		b.WriteString(r.Status)
		b.WriteByte('\t')
		b.WriteString(r.Reason)
		b.WriteByte('\n')
	}
	return b.String()
}

// frontmatter is the slice of fields the classifier needs from a task
// file's YAML frontmatter: scalar fields (status, scheduler, host) and
// the `needs:` list.
type frontmatter struct {
	fields map[string]string
	needs  []string
}

var (
	fmDelimRE   = regexp.MustCompile(`^---\s*$`)
	fmFieldRE   = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_-]*):\s*(.*?)\s*$`)
	fmListItem  = regexp.MustCompile(`^\s+-\s+(.*)$`)
	fmNeedsHead = regexp.MustCompile(`^needs:\s*$`)
)

// scalarKeys names the scalar frontmatter fields the classifier
// records. Other keys are ignored to keep parsing tight.
var scalarKeys = map[string]bool{
	"status":    true,
	"scheduler": true,
	"host":      true,
}

func readFrontmatter(path string) frontmatter {
	out := frontmatter{fields: map[string]string{}}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	inFM := false
	inNeeds := false
	seenOpen := false
	for sc.Scan() {
		line := sc.Text()
		if fmDelimRE.MatchString(line) {
			if !seenOpen {
				inFM = true
				seenOpen = true
				continue
			}
			break
		}
		if !inFM {
			continue
		}
		if inNeeds {
			if m := fmListItem.FindStringSubmatch(line); m != nil {
				v := stripInlineComment(m[1])
				v = strings.TrimSpace(v)
				if v != "" {
					out.needs = append(out.needs, v)
				}
				continue
			}
			inNeeds = false
		}
		if fmNeedsHead.MatchString(line) {
			inNeeds = true
			continue
		}
		if m := fmFieldRE.FindStringSubmatch(line); m != nil {
			key := m[1]
			if scalarKeys[key] {
				out.fields[key] = strings.TrimSpace(m[2])
			}
		}
	}
	return out
}

func stripInlineComment(s string) string {
	if i := strings.Index(s, "#"); i >= 0 {
		return s[:i]
	}
	return s
}

// stateSignals captures the per-slug bits the classifier looks for in
// state.md: presence under ## Open operator questions, and a Recent
// events row mentioning the slug plus an operator-attention keyword.
type stateSignals struct {
	openQuestion         map[string]bool
	recentOperatorNotice map[string]bool
}

var operatorNoticeRE = regexp.MustCompile(`(?i)operator|notify|notification|attention|asked`)

func loadStateSignals(path string) stateSignals {
	out := stateSignals{
		openQuestion:         map[string]bool{},
		recentOperatorNotice: map[string]bool{},
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return out
	}

	const (
		secNone   = 0
		secOpenQ  = 1
		secRecent = 2
		secOther  = 3
	)
	section := secNone

	headerOpenQ := regexp.MustCompile(`^##\s+Open operator questions\s*$`)
	headerRecent := regexp.MustCompile(`^##\s+Recent events\s*$`)
	headerAny := regexp.MustCompile(`^##\s+`)

	sc := bufio.NewScanner(strings.NewReader(string(body)))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case headerOpenQ.MatchString(line):
			section = secOpenQ
			continue
		case headerRecent.MatchString(line):
			section = secRecent
			continue
		case headerAny.MatchString(line):
			section = secOther
			continue
		}
		switch section {
		case secOpenQ:
			if slug, ok := openQuestionSlug(line); ok {
				out.openQuestion[slug] = true
			}
		case secRecent:
			if !operatorNoticeRE.MatchString(line) {
				continue
			}
			for _, slug := range slugsIn(line) {
				out.recentOperatorNotice[slug] = true
			}
		}
	}
	return out
}

var openQuestionRE = regexp.MustCompile(`^-\s+([A-Za-z0-9_-][A-Za-z0-9._-]*):`)

func openQuestionSlug(line string) (string, bool) {
	m := openQuestionRE.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// slugsIn pulls plausible task slugs out of a Recent events line:
// anything that looks like kebab-case identifier of length >= 3.
var slugTokenRE = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._-]{2,}`)

func slugsIn(line string) []string {
	return slugTokenRE.FindAllString(line, -1)
}

// loadThreadSlugs returns the set of slugs mentioned in the
// harness-notify thread file. The file is JSONL; we look for
// `"slug":"<value>"` substrings to match the bash version's grep.
func loadThreadSlugs(path string) map[string]bool {
	out := map[string]bool{}
	body, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	re := regexp.MustCompile(`"slug":"([^"]+)"`)
	for _, m := range re.FindAllStringSubmatch(string(body), -1) {
		out[m[1]] = true
	}
	return out
}

// schedulerOperatorOwned matches the bash heuristic: any of a small
// set of phrases in the scheduler text means the operator owns the
// trigger.
func schedulerOperatorOwned(scheduler string) bool {
	if scheduler == "" {
		return false
	}
	lower := strings.ToLower(scheduler)
	for _, kw := range []string{
		"operator", "manual", "decide", "approval",
		"blocked until", "credentials", "sudo", "hardware",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

var (
	trackerRE    = regexp.MustCompile(`(^|[^A-Za-z0-9])tracker([^A-Za-z0-9]|$)`)
	resumeWhenRE = regexp.MustCompile(`resume\s+when\s+.*(begins|lands|completes)`)
)

// schedulerWaitingTrigger detects schedulers explicitly waiting on a
// tracker / rollup / external completion event.
func schedulerWaitingTrigger(scheduler string) bool {
	if scheduler == "" {
		return false
	}
	lower := strings.ToLower(scheduler)
	for _, kw := range []string{"never started directly", "epic-tracker", "rollup"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	if trackerRE.MatchString(lower) {
		return true
	}
	if resumeWhenRE.MatchString(lower) {
		return true
	}
	return false
}

// schedulerTriggerSatisfied is the positive case: scheduler text
// implies "go", or there is a non-empty needs: list (every entry of
// which has been verified done before this call).
func schedulerTriggerSatisfied(scheduler string, hasNeeds bool) bool {
	if scheduler == "" {
		return false
	}
	if schedulerOperatorOwned(scheduler) {
		return false
	}
	if schedulerWaitingTrigger(scheduler) {
		return false
	}
	if hasNeeds {
		return true
	}
	lower := strings.ToLower(scheduler)
	for _, kw := range []string{"ready", "unblocked", "resume now", "start now", "trigger satisfied"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
