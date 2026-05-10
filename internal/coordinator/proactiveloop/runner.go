package proactiveloop

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os/exec"
	"strconv"
	"time"
)

// systemRunner is the default Runner: spawns the binary with args,
// captures stdout (combined), returns the exit code. ExitError exit
// status is unwrapped; other errors yield code -1 with the error
// message as stdout (mirrors the bash 2>&1 pattern).
func systemRunner(name string, args ...string) (string, int) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	return string(out) + err.Error(), -1
}

// defaultRandom mirrors the bash `$(date +%s%N)-$$-$RANDOM` pattern
// used to mint inbox filenames. We avoid OS-specific %N support by
// stamping nanoseconds + 16 random hex chars; uniqueness is what the
// stamp needs to provide, not literal compatibility.
func defaultRandom() string {
	nanos := strconv.FormatInt(time.Now().UnixNano(), 10)
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nanos
	}
	return nanos + "-" + hex.EncodeToString(b[:])
}
