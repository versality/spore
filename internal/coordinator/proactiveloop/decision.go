package proactiveloop

import (
	"fmt"
	"strings"
)

type actionsInput struct {
	advice         string
	replenishFloor int
	activeLive     int
	startable      []string
	parked         []string
	paused         []string
	blocked        []string
	invalid        []string
	stale          []string
	unread         int
	unreadNewest   string
	watchdogRC     int
	watchdogOut    string
	replenishRC    int
	replenishOut   string
	fleetRC        int
	fleetOut       string
}

func buildActions(in actionsInput) []string {
	var actions []string
	canStart := in.advice != "ration" && in.replenishFloor > 0 && in.activeLive < in.replenishFloor
	if canStart && len(in.startable) > 0 {
		actions = append(actions, "start one of: "+joinCSV(5, in.startable))
	}
	if canStart && len(in.parked) > 0 {
		actions = append(actions, "reclassify/promote parked: "+joinCSV(5, in.parked))
	}
	if canStart && len(in.paused) > 0 {
		actions = append(actions, "resume/reclassify paused: "+joinCSV(5, in.paused))
	}
	if len(in.blocked) > 0 {
		actions = append(actions, "resolve blocked ownership: "+joinCSV(5, in.blocked))
	}
	if len(in.invalid) > 0 {
		actions = append(actions, "reclassify invalid queue rows: "+joinCSV(5, in.invalid))
	}
	if len(in.stale) > 0 {
		actions = append(actions, "reconcile state.md active rows: "+joinCSV(5, in.stale))
	}
	if in.unread > 0 {
		newest := in.unreadNewest
		if newest == "" {
			newest = "unknown"
		}
		actions = append(actions, fmt.Sprintf("drain skyhelm inbox: %d unread newest=%s", in.unread, newest))
	}
	if in.watchdogRC == 0 && len(actions) > 0 {
		actions = append(actions, "idle-watchdog said ok; handle this proactive-loop list instead")
	} else if in.watchdogRC == 2 {
		first := nthLine(in.watchdogOut, 2)
		if first != "" {
			actions = append(actions, "idle-watchdog also reports: "+first)
		}
	}
	if in.replenishRC == 0 && strings.Contains(in.replenishOut, "no promotable drafts") && len(actions) > 0 {
		actions = append(actions, "fleet replenish found no promotable drafts; triage backlog now")
	}
	if in.fleetRC != 0 && in.fleetRC != 2 {
		actions = append(actions, "inspect fleet status error: "+firstLine(in.fleetOut))
	}
	return actions
}

func joinCSV(maxItems int, items []string) string {
	var b strings.Builder
	for i, it := range items {
		if i >= maxItems {
			break
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(it)
	}
	if len(items) > maxItems {
		fmt.Fprintf(&b, ",+%d", len(items)-maxItems)
	}
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func nthLine(s string, n int) string {
	if n <= 0 {
		return ""
	}
	for i, line := range strings.Split(s, "\n") {
		if i+1 == n {
			return line
		}
	}
	return ""
}
