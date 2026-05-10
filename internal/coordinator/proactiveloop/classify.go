package proactiveloop

import (
	"strconv"
	"strings"
)

// classify shells out to the queue classifier and parses its TSV
// stdout into the five action buckets the bash uses. Returns empty
// buckets on no-project / classifier error (the error itself surfaces
// as the "invalid" bucket).
func classify(cfg Config, projects []string, floor, activeLive int) (startable, parked, paused, blocked, invalid []string) {
	project := firstProject(projects)
	if project == "" {
		return
	}
	out, code := cfg.Exec(cfg.ClassifierBin,
		"--project", project,
		"--state", cfg.StateFile,
		"--active-live", strconv.Itoa(activeLive),
		"--floor", strconv.Itoa(floor),
	)
	if code != 0 {
		invalid = append(invalid, "classifier-error:"+firstLine(out))
		return
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		class := fields[0]
		slug := fields[1]
		if class == "" || slug == "" {
			continue
		}
		status := ""
		reason := ""
		if len(fields) >= 3 {
			status = fields[2]
		}
		if len(fields) >= 4 {
			reason = fields[3]
		}
		key := class + ":" + status
		switch key {
		case "runnable-promote:draft":
			startable = append(startable, slug)
		case "runnable-promote:parked":
			parked = append(parked, slug+":"+reason)
		case "resume:paused":
			paused = append(paused, slug+":"+reason)
		case "invalid-needs-reclassify:blocked":
			if reason == "blocked-without-operator-owner" {
				blocked = append(blocked, slug+":assign-owner")
			} else {
				invalid = append(invalid, slug+":"+reason)
			}
		default:
			if class == "invalid-needs-reclassify" {
				invalid = append(invalid, slug+":"+reason)
			}
		}
	}
	return
}

// parseActiveLive scans `wt-task fleet status` output for an
// `active-live=N` token; returns 0 when missing.
func parseActiveLive(out string) int {
	for _, line := range strings.Split(out, "\n") {
		for _, tok := range strings.Fields(line) {
			const pfx = "active-live="
			if !strings.HasPrefix(tok, pfx) {
				continue
			}
			n, err := strconv.Atoi(strings.TrimRight(tok[len(pfx):], ",;"))
			if err == nil {
				return n
			}
		}
	}
	return 0
}

// extractJSONField does a tiny ad-hoc lookup for "key":"value" in a
// JSON blob, just enough to mirror `jq -r '.advice // ""'`. We avoid a
// full JSON dependency to match the bash exactly: malformed input
// returns "".
func extractJSONField(blob, key string) string {
	needle := "\"" + key + "\""
	i := strings.Index(blob, needle)
	if i < 0 {
		return ""
	}
	rest := blob[i+len(needle):]
	rest = strings.TrimLeft(rest, " \t\r\n:")
	if !strings.HasPrefix(rest, "\"") {
		return ""
	}
	rest = rest[1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}
