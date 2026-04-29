#!/usr/bin/env perl
# PreToolUse hook: deny `Bash run_in_background:true`. Long-running jobs
# go in tmux windows so the operator and the spore coordinator can both
# see live output, and so session-close reaps the process. Bash
# run_in_background buffers output to a file the coordinator never
# polls, which on a spore box looks like a silent hang.
use strict;
use warnings;
use JSON::PP;

my $raw = do { local $/; <STDIN> };
my $payload = eval { decode_json($raw) };
exit 0 unless ref($payload) eq 'HASH';
exit 0 unless ($payload->{tool_name} // '') eq 'Bash';

my $bg = $payload->{tool_input}{run_in_background};
my $truthy = JSON::PP::is_bool($bg)
    ? !!$bg
    : ($bg && $bg ne 'false' && $bg ne '0');
exit 0 unless $truthy;

my $msg = "Long-running jobs must run in a tmux window, not Bash "
        . "run_in_background. Spawn one with `tmux new-window -t "
        . "<session> -n <name>`, drive it via `tmux send-keys`, and "
        . "read it via `tmux capture-pane` or a tee'd log file.";

print encode_json({
  hookSpecificOutput => {
    hookEventName            => "PreToolUse",
    permissionDecision       => "deny",
    permissionDecisionReason => $msg,
  },
});
exit 0;
