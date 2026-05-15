// Package turn generates time-limited TURN credentials using the HMAC-SHA1
// scheme described in RFC 8489. Credentials expire after a configurable TTL
// so a stolen credential set has a bounded useful lifetime.
package turn

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"time"
)

// Credentials holds a TURN username and credential pair.
type Credentials struct {
	Username   string `json:"username"`
	Credential string `json:"credential"`
}

// CredentialGenerator generates time-limited TURN credentials using HMAC-SHA1.
type CredentialGenerator struct {
	secret []byte
	ttl    time.Duration
}

// NewCredentialGenerator creates a new CredentialGenerator with the given shared secret and TTL.
func NewCredentialGenerator(secret string, ttl time.Duration) *CredentialGenerator {
	return &CredentialGenerator{
		secret: []byte(secret),
		ttl:    ttl,
	}
}

// Generate creates TURN credentials for the given identifier.
// Username format: "<expiry-unix>:<identifier>"
// Credential: base64(HMAC-SHA1(secret, username))
func (g *CredentialGenerator) Generate(identifier string) Credentials {
	expiry := time.Now().Add(g.ttl).Unix()
	username := fmt.Sprintf("%d:%s", expiry, identifier)
	credential := g.computeHMAC(username)
	return Credentials{
		Username:   username,
		Credential: credential,
	}
}

func (g *CredentialGenerator) computeHMAC(data string) string {
	mac := hmac.New(sha1.New, g.secret)
	mac.Write([]byte(data))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
