package lints

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// FingerprintVersion is the schema tag prefixed onto every computed
// fingerprint. Bump it when the input shape changes (new normalization,
// new field) so stale entries in `scout-minted.tsv` get re-minted
// rather than silently colliding.
const FingerprintVersion = "v1"

// Fingerprint computes the stable identifier for a finding. The hash
// covers the tuple (lintName, path, line, message); callers that need
// to dedup across runs ignore lints that change their message text.
func Fingerprint(lintName, p string, line int, msg string) string {
	h := sha256.New()
	h.Write([]byte(lintName))
	h.Write([]byte{0})
	h.Write([]byte(p))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(line)))
	h.Write([]byte{0})
	h.Write([]byte(msg))
	sum := h.Sum(nil)
	return FingerprintVersion + ":" + hex.EncodeToString(sum[:8])
}
