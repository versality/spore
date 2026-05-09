package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/versality/spore/internal/event"
)

const eventUsage = `spore event - canonical fleet event bus

Usage:
  spore event <subcommand> [flags]

Subcommands:
  publish    Append one event to the bus.
  tail       Read events; filter, optionally follow.
  watch      Long-running: run a command per matching event.

Storage:
  $XDG_STATE_HOME/spore/events.jsonl (default ~/.local/state/spore/events.jsonl).
  Rotated to events-<ts>.jsonl past $SPORE_EVENT_MAX_BYTES (default 64 MiB).
  Use $SPORE_EVENT_DIR to override the directory (test-only).

Schema (jsonl, one event per line):
  {ts, source, level, kind, message}                  required
  {slug, data}                                        optional
  level                                               info|warn|error
`

const eventPublishUsage = `spore event publish - append one event

Usage:
  spore event publish --source S --kind K --level L [flags] -- "<message>"
  spore event publish --source S --kind K --level L --message-stdin [flags]

Flags:
  --source         Free-form source tag (e.g. skyhelm, wt-task).        required
  --kind           Free-form event kind, convention <source>:<name>.    required
  --level          info | warn | error.                                 required
  --slug           Optional slug (e.g. task slug, project name).
  --data           Optional raw JSON value attached to the event.
  --message-stdin  Read the message from stdin instead of trailing arg.

The message is the trailing positional after '--', or stdin when
--message-stdin is set. Multi-line ok via stdin.
`

const eventTailUsage = `spore event tail - read events

Usage:
  spore event tail [--since DUR] [--level L] [--source S] [--kind K] [--slug X] [-n N] [--follow]

Flags:
  --since   Only events at or after now-DUR (e.g. 1h, 15m, 24h).
  --level   Match level (info|warn|error).
  --source  Match source.
  --kind    Match kind.
  --slug    Match slug.
  -n        Cap output at N events. Default 50. 0 means no cap.
  --follow  After the historical read, tail the current jsonl.

Filters compose AND. Output is one JSON object per line.
`

const eventWatchUsage = `spore event watch - run a command per matching event

Usage:
  spore event watch --filter '<expr>' --exec '<cmd>'

Flags:
  --filter  AND-composed key=value expression. Keys: level, source, kind, slug.
            Example: 'level=error AND source=systemd'.
  --exec    Shell command to run per match. The event JSON is piped to its
            stdin. The following SPORE_EVENT_* env vars are also set:
              SPORE_EVENT_TS, SPORE_EVENT_SOURCE, SPORE_EVENT_LEVEL,
              SPORE_EVENT_KIND, SPORE_EVENT_SLUG, SPORE_EVENT_MESSAGE.

Watch is long-running. Ctrl-C to stop. Each match spawns one process and
waits for it to exit before reading the next event so handlers serialize.
`

func runEvent(args []string) int {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, eventUsage)
		return 2
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Print(eventUsage)
		return 0
	case "publish":
		return runEventPublish(rest)
	case "tail":
		return runEventTail(rest)
	case "watch":
		return runEventWatch(rest)
	default:
		fmt.Fprintf(os.Stderr, "spore event: unknown subcommand %q\n\n%s", sub, eventUsage)
		return 2
	}
}

func runEventPublish(args []string) int {
	fs := flag.NewFlagSet("event publish", flag.ContinueOnError)
	source := fs.String("source", "", "")
	kind := fs.String("kind", "", "")
	level := fs.String("level", "", "")
	slug := fs.String("slug", "", "")
	data := fs.String("data", "", "")
	stdinMsg := fs.Bool("message-stdin", false, "")
	help := fs.Bool("h", false, "")
	helpLong := fs.Bool("help", false, "")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore event publish:", err)
		fmt.Fprint(os.Stderr, eventPublishUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(eventPublishUsage)
		return 0
	}

	var message string
	switch {
	case *stdinMsg:
		if fs.NArg() != 0 {
			fmt.Fprintln(os.Stderr, "spore event publish: --message-stdin and positional message are mutually exclusive")
			return 2
		}
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "spore event publish: read stdin:", err)
			return 1
		}
		message = strings.TrimRight(string(b), "\n")
	case fs.NArg() == 1:
		message = fs.Arg(0)
	default:
		fmt.Fprintln(os.Stderr, "spore event publish: expected exactly one positional message (or --message-stdin)")
		fmt.Fprint(os.Stderr, eventPublishUsage)
		return 2
	}

	ev := &event.Event{
		Source:  *source,
		Kind:    *kind,
		Level:   *level,
		Slug:    *slug,
		Message: message,
	}
	if *data != "" {
		ev.Data = json.RawMessage(*data)
	}
	if err := event.Append(ev); err != nil {
		fmt.Fprintln(os.Stderr, "spore event publish:", err)
		return 1
	}
	return 0
}

func runEventTail(args []string) int {
	fs := flag.NewFlagSet("event tail", flag.ContinueOnError)
	since := fs.Duration("since", 0, "")
	level := fs.String("level", "", "")
	source := fs.String("source", "", "")
	kind := fs.String("kind", "", "")
	slug := fs.String("slug", "", "")
	n := fs.Int("n", 50, "")
	follow := fs.Bool("follow", false, "")
	help := fs.Bool("h", false, "")
	helpLong := fs.Bool("help", false, "")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore event tail:", err)
		fmt.Fprint(os.Stderr, eventTailUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(eventTailUsage)
		return 0
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "spore event tail: unexpected positional args")
		return 2
	}

	f := event.Filter{
		Level:  *level,
		Source: *source,
		Kind:   *kind,
		Slug:   *slug,
	}
	if *since > 0 {
		f.Since = time.Now().UTC().Add(-*since)
	}

	limit := *n
	events, err := event.Read(f, limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore event tail:", err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	for _, ev := range events {
		if err := enc.Encode(&ev); err != nil {
			fmt.Fprintln(os.Stderr, "spore event tail:", err)
			return 1
		}
	}
	if !*follow {
		return 0
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	stop := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(stop)
	}()
	err = event.Follow(stop, 0, f, func(ev event.Event) {
		_ = enc.Encode(&ev)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore event tail:", err)
		return 1
	}
	return 0
}

func runEventWatch(args []string) int {
	fs := flag.NewFlagSet("event watch", flag.ContinueOnError)
	filterExpr := fs.String("filter", "", "")
	execCmd := fs.String("exec", "", "")
	help := fs.Bool("h", false, "")
	helpLong := fs.Bool("help", false, "")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore event watch:", err)
		fmt.Fprint(os.Stderr, eventWatchUsage)
		return 2
	}
	if *help || *helpLong {
		fmt.Print(eventWatchUsage)
		return 0
	}
	if strings.TrimSpace(*execCmd) == "" {
		fmt.Fprintln(os.Stderr, "spore event watch: --exec is required")
		return 2
	}
	f, err := parseWatchFilter(*filterExpr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore event watch:", err)
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	stop := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(stop)
	}()

	emit := func(ev event.Event) {
		runWatchExec(ctx, *execCmd, ev)
	}
	if err := event.Follow(stop, 0, f, emit); err != nil {
		fmt.Fprintln(os.Stderr, "spore event watch:", err)
		return 1
	}
	return 0
}

// parseWatchFilter parses key=value pairs joined by 'AND' (case-insensitive,
// whitespace-tolerant). An empty expression matches everything.
func parseWatchFilter(expr string) (event.Filter, error) {
	var f event.Filter
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return f, nil
	}
	parts := splitAnd(expr)
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			return f, fmt.Errorf("filter clause %q: want key=value", p)
		}
		key := strings.TrimSpace(p[:eq])
		val := strings.TrimSpace(p[eq+1:])
		switch key {
		case "level":
			f.Level = val
		case "source":
			f.Source = val
		case "kind":
			f.Kind = val
		case "slug":
			f.Slug = val
		default:
			return f, fmt.Errorf("filter key %q: want level|source|kind|slug", key)
		}
	}
	return f, nil
}

// splitAnd splits on 'AND' tokens regardless of case, with surrounding
// whitespace on either side. It does not interpret quoted strings; values
// must not contain a literal " AND " (a fine constraint for free-form tags).
func splitAnd(expr string) []string {
	var out []string
	rest := expr
	for {
		idx := indexFoldWord(rest, "AND")
		if idx < 0 {
			out = append(out, rest)
			return out
		}
		out = append(out, rest[:idx])
		rest = rest[idx+len("AND"):]
	}
}

// indexFoldWord finds the case-insensitive position of word in s where it
// appears as a standalone token (whitespace on both sides, or at a boundary).
// Returns -1 when not found.
func indexFoldWord(s, word string) int {
	low := strings.ToLower(s)
	w := strings.ToLower(word)
	from := 0
	for {
		i := strings.Index(low[from:], w)
		if i < 0 {
			return -1
		}
		i += from
		leftOK := i == 0 || isSpace(low[i-1])
		rightOK := i+len(w) == len(low) || isSpace(low[i+len(w)])
		if leftOK && rightOK {
			return i
		}
		from = i + len(w)
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func runWatchExec(ctx context.Context, shellCmd string, ev event.Event) {
	c := exec.CommandContext(ctx, "sh", "-c", shellCmd)
	c.Env = append(os.Environ(),
		"SPORE_EVENT_TS="+ev.Ts.UTC().Format(time.RFC3339Nano),
		"SPORE_EVENT_SOURCE="+ev.Source,
		"SPORE_EVENT_LEVEL="+ev.Level,
		"SPORE_EVENT_KIND="+ev.Kind,
		"SPORE_EVENT_SLUG="+ev.Slug,
		"SPORE_EVENT_MESSAGE="+ev.Message,
	)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	body, err := json.Marshal(&ev)
	if err == nil {
		c.Stdin = strings.NewReader(string(body) + "\n")
	}
	if err := c.Run(); err != nil {
		// Surface but don't kill the watcher: a bad handler shouldn't
		// silence subsequent events.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			fmt.Fprintf(os.Stderr, "spore event watch: exec exit %d\n", ee.ExitCode())
		} else {
			fmt.Fprintln(os.Stderr, "spore event watch: exec:", err)
		}
	}
}
