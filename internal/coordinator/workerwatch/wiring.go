package workerwatch

import (
	"os"
	"path/filepath"
	"time"

	"github.com/versality/spore/internal/coordinator/verify"
	"github.com/versality/spore/internal/fleet"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/task/frontmatter"
)

// DefaultStateFile returns the spore-namespaced snapshot path:
// $SPORE_WORKER_WATCH_FILE, else $SPORE_WORKER_WATCH_DIR/state.ndjson,
// else $HOME/.local/state/spore/worker-watch/state.ndjson. Caller is
// responsible for the dir mkdir; SaveStateFile handles that on write.
func DefaultStateFile() string {
	if p := os.Getenv("SPORE_WORKER_WATCH_FILE"); p != "" {
		return p
	}
	dir := os.Getenv("SPORE_WORKER_WATCH_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state", "spore", "worker-watch")
	}
	return filepath.Join(dir, "state.ndjson")
}

// DefaultProjectsFile returns the projects-list path:
// $WT_CFG/projects when WT_CFG is set, else $HOME/.config/wt/projects.
// Delegates to internal/fleet so the watcher scans the same project set
// the fleet liveness pass sees.
func DefaultProjectsFile() string {
	if p := fleet.ProjectsFilePath(); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wt", "projects")
}

// ReadProjects parses the projects-list file. Blank lines and #
// comments are skipped. Missing file is empty, not an error. Order
// is preserved.
func ReadProjects(path string) ([]string, error) {
	return fleet.ReadProjects(path)
}

// ScanActive walks every projectRoot, lists tasks/*.md, filters to
// status=active, and returns one TaskRef per worker. Slug is
// "<project>/<base>" when len(projectRoots) > 1, else the bare base
// slug. project names that fail ProjectName resolution fall back to
// filepath.Base(projectRoot). Errors per project are returned
// aggregated to keep one bad project from blanking the watcher.
func ScanActive(projectRoots []string) ([]TaskRef, error) {
	if len(projectRoots) == 0 {
		root, err := mainRepoRoot()
		if err != nil {
			return nil, err
		}
		projectRoots = []string{root}
	}
	multi := len(projectRoots) > 1
	var refs []TaskRef
	var firstErr error
	for _, root := range projectRoots {
		metas, err := task.List(filepath.Join(root, "tasks"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		project, perr := task.ProjectName(root)
		if perr != nil || project == "" {
			project = filepath.Base(root)
		}
		for _, m := range metas {
			if !task.IsActive(m.Status) {
				continue
			}
			base := m.Slug
			if base == "" {
				continue
			}
			slug := base
			if multi {
				slug = project + "/" + base
			}
			agent := m.Agent
			if agent == "" {
				agent = "claude"
			}
			refs = append(refs, TaskRef{
				Slug:        slug,
				BaseSlug:    base,
				ProjectRoot: root,
				Status:      m.Status,
				Agent:       agent,
			})
		}
	}
	return refs, firstErr
}

// ResolveDisappearance reads the post-disappearance status of a slug
// no longer in the active set. baseSlug + projectRoot identify the
// task file location. "missing" status is returned when the .md
// itself is gone. Verdict is filled only for status=done via
// verify.Verify (in-process).
func ResolveDisappearance(slug, baseSlug, projectRoot string) FinalStatus {
	if projectRoot == "" {
		root, err := mainRepoRoot()
		if err == nil {
			projectRoot = root
		}
	}
	taskFile := filepath.Join(projectRoot, "tasks", baseSlug+".md")
	b, err := os.ReadFile(taskFile)
	if err != nil {
		return FinalStatus{Status: "missing"}
	}
	m, _, err := frontmatter.Parse(b)
	if err != nil {
		return FinalStatus{Status: "?"}
	}
	status := m.Status
	if status == "" {
		status = "?"
	}
	out := FinalStatus{Status: status}
	if task.IsDone(status) {
		res := verify.Verify(baseSlug, verify.Config{ProjectRoot: projectRoot})
		out.Verdict = string(res.Verdict)
	}
	return out
}

// ProductionEnv builds the Env used by the CLI hook path. It binds
// real I/O for every probe and caches the resolved project list +
// state file so a single Run pass reuses one consistent view.
func ProductionEnv(now time.Time, projectsFile, stateFile string) Env {
	projects, _ := ResolveProjectRoots(projectsFile)
	home, _ := os.UserHomeDir()

	return Env{
		Now: func() time.Time { return now },
		Active: func() []TaskRef {
			refs, _ := ScanActive(projects)
			return refs
		},
		HeadSHA: HeadShortSHA,
		Idle: func(projectRoot, baseSlug, agent string) (int, bool) {
			wtDir := filepath.Join(projectRoot, ".worktrees", baseSlug)
			if _, err := os.Stat(wtDir); err != nil {
				return 0, false
			}
			switch agent {
			case "opencode":
				return OpencodeIdleSecs("opencode", wtDir, now)
			default:
				return ClaudeIdleSecs(home, wtDir, now)
			}
		},
		Resolve: func(slug, baseSlug, _ string) FinalStatus {
			project := projectRootForSlug(slug, baseSlug, projects)
			return ResolveDisappearance(slug, baseSlug, project)
		},
		LoadState: func() ([]Snapshot, error) { return LoadStateFile(stateFile) },
		SaveState: func(snaps []Snapshot) error { return SaveStateFile(stateFile, snaps) },
	}
}

// ResolveProjectRoots reads projectsFile and falls back to the
// current main repo root when the file is empty or missing.
func ResolveProjectRoots(projectsFile string) ([]string, error) {
	projects, err := ReadProjects(projectsFile)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		root, mErr := mainRepoRoot()
		if mErr != nil {
			return nil, err
		}
		return []string{root}, nil
	}
	return projects, nil
}

// projectRootForSlug maps a project-prefixed slug back to its owning
// projectRoot. Bare slugs (single-project mode) return the first
// project root. Unknown projects fall back to the first root too.
func projectRootForSlug(slug, baseSlug string, projects []string) string {
	if len(projects) == 0 {
		return ""
	}
	if slug == baseSlug {
		return projects[0]
	}
	target := slug[:len(slug)-len(baseSlug)-1] // strip "/<base>"
	for _, root := range projects {
		name, err := task.ProjectName(root)
		if err != nil || name == "" {
			name = filepath.Base(root)
		}
		if name == target {
			return root
		}
	}
	return projects[0]
}

func mainRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return wd, nil
}
