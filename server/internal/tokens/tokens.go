// Package tokens provides the shared token hashing and random-token generation
// used by the auth, device, and pairing packages. It lives on its own so those
// packages do not have to import one another (previously auth and pairing
// depended on the device package solely for its token hasher, and each
// re-implemented the same random-hex generator). The name is plural so it never
// collides with the ubiquitous `token` local variable in its callers.
package tokens

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// Hash returns the SHA-256 hex digest of a plaintext token. Session, magic-link,
// and device tokens are persisted hashed so a database leak does not expose
// usable credentials; callers hash on write and hash again to look up.
func Hash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// RandomHex returns a cryptographically random hex string encoding n random
// bytes, so the result is 2*n characters. Used to mint opaque session, magic-link,
// pairing, and OAuth-state tokens.
func RandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
