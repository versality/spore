#!/usr/bin/env perl
# SessionStart hook: load <project_root>/state.md into the model context.
#
# Resolves <project_root> via `git rev-parse --git-common-dir` so worker
# sessions running in <project_root>/.worktrees/<slug>/ still pick up the
# canonical state.md in the main worktree. Falls back to cwd when not in
# a git repo so the hook is harmless outside spore.
use strict;
use warnings;
use JSON::PP;
use Cwd qw(getcwd abs_path);

my $cwd = $ENV{CLAUDE_PROJECT_DIR} || getcwd();

my $root = $cwd;
my $gcd = `git -C "$cwd" rev-parse --git-common-dir 2>/dev/null`;
chomp $gcd;
if ($gcd ne '') {
    # rev-parse returns either ".git" (cwd is the toplevel) or an
    # absolute path under .worktrees/<slug>/.git. Walk to its parent
    # to land on the project root in either case.
    my $abs = ($gcd =~ m{^/}) ? $gcd : "$cwd/$gcd";
    my $resolved = abs_path("$abs/..");
    $root = $resolved if defined $resolved;
}

my $path = "$root/state.md";
open my $fh, '<:encoding(UTF-8)', $path or exit 0;
my $body = do { local $/; <$fh> };
close $fh;
exit 0 unless defined $body && length $body;

print encode_json({
  hookSpecificOutput => {
    hookEventName     => "SessionStart",
    additionalContext => $body,
  },
});
exit 0;
