// Package verify ports the coordinator-verify-done.sh verdict logic to Go.
// Given a slug and a project root, it inspects git history, wt-task
// event logs, and claude-code session transcripts to determine whether
// a rower's done flip is backed by real evidence.
package verify

import (
	"regexp"
	"strings"
)

type Verdict string

const (
	RealImpl             Verdict = "real-impl"
	RationalClose        Verdict = "rational-close"
	CrossRepo            Verdict = "cross-repo"
	SuspectHallucination Verdict = "suspect-hallucination"
	BogusEvidence        Verdict = "bogus-evidence"
	LostToReflog         Verdict = "lost-to-reflog"
	Unknown              Verdict = "unknown"
)

type Result struct {
	Slug              string  `json:"slug"`
	Verdict           Verdict `json:"verdict"`
	GitCommit         string  `json:"git_commit"`
	MergeEntry        string  `json:"merge_entry"`
	FinalTool         string  `json:"final_tool"`
	FinalText         string  `json:"final_text"`
	LastTimestamp     string  `json:"last_timestamp"`
	FrontmatterStatus string  `json:"frontmatter_status"`
	CrossRepoPath     string  `json:"cross_repo_path,omitempty"`
	ReflogSHA         string  `json:"reflog_sha,omitempty"`
	EvidenceStatus    string  `json:"evidence_status,omitempty"`
	EvidenceFailures  string  `json:"evidence_failures,omitempty"`
}

type Config struct {
	ProjectRoot string
	EventsFile  string
	ProjectsDir string
}

var (
	rationalRE   = regexp.MustCompile(`(?i)(abandon|already (done|shipped|landed)|shipped in [a-f0-9]{7,40}|superseded|replaced by|rejected|rejection|out of bot reach|work is done|nothing (left|to do)|administrative close)`)
	completionRE = regexp.MustCompile(`(?i)(\bmerged\b|\bdone\.|\bcompleted\b|fast-forward|wt merge succeeded|all green)`)
)

func Verify(slug string, cfg Config) Result {
	r := Result{
		Slug:              slug,
		Verdict:           Unknown,
		MergeEntry:        "none",
		FinalTool:         "none",
		LastTimestamp:     "?",
		FrontmatterStatus: "?",
	}

	root := cfg.ProjectRoot
	if root == "" {
		root = resolveRepoRoot()
	}

	mainBranch := resolveMainBranch(root)

	implCommit := findImplCommit(root, slug, mainBranch)
	r.GitCommit = implCommit

	reflogSHA := ""
	if implCommit == "" {
		reflogSHA = findReflogCommit(root, slug, mainBranch)
	}
	r.ReflogSHA = reflogSHA

	mergeEntry := findMergeEvent(slug, cfg.EventsFile)
	r.MergeEntry = mergeEntry

	session := findSessionFile(slug, cfg.ProjectsDir)
	finalTool, finalText, lastTS, gitCommitSeen, wtMergeSeen, crossRepoPath := analyzeSession(session)
	r.FinalTool = finalTool
	r.FinalText = truncate(finalText, 200)
	r.LastTimestamp = lastTS
	r.CrossRepoPath = crossRepoPath

	r.FrontmatterStatus = readFrontmatterStatus(root, slug)

	evidenceFailures := checkEvidence(root, slug)
	if evidenceFailures != "" {
		r.EvidenceStatus = "failed"
		r.EvidenceFailures = evidenceFailures
	} else if hasEvidenceSection(root, slug) {
		r.EvidenceStatus = "ok"
	}

	r.Verdict = decide(implCommit, reflogSHA, mergeEntry, finalTool,
		finalText, crossRepoPath, evidenceFailures,
		gitCommitSeen, wtMergeSeen)

	return r
}

func decide(implCommit, reflogSHA, mergeEntry, finalTool, finalText,
	crossRepoPath, evidenceFailures string,
	gitCommitSeen, wtMergeSeen bool) Verdict {

	if evidenceFailures != "" {
		return BogusEvidence
	}
	if implCommit != "" {
		return RealImpl
	}
	if reflogSHA != "" {
		return LostToReflog
	}
	if crossRepoPath != "" {
		return CrossRepo
	}

	ftLower := strings.ToLower(finalText)
	if finalTool == "wt-abandon" || finalTool == "tell-abandoned" ||
		rationalRE.MatchString(ftLower) {
		return RationalClose
	}

	if mergeEntry == "none" && !gitCommitSeen && !wtMergeSeen &&
		completionRE.MatchString(ftLower) {
		return SuspectHallucination
	}

	return Unknown
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func collapseSpaces(s string) string {
	prev := false
	var buf strings.Builder
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !prev {
				buf.WriteByte(' ')
			}
			prev = true
		} else {
			buf.WriteRune(r)
			prev = false
		}
	}
	return buf.String()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
