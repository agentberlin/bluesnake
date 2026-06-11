// Package store is the tunnel control-plane persistence layer: the binding
// between a tunnel's stable identity and the hash of its connect secret.
//
// Only the credential *hash* is ever stored. The plaintext connect secret is
// generated here, returned to the caller exactly once at registration, and
// never persisted in the clear — a database compromise yields no usable
// credentials.
package store

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// Tunnel is one install's control-plane record.
type Tunnel struct {
	ID                string
	ConnectSecretHash []byte // sha256 of the tunnel-auth secret
	Revoked           bool
}

var (
	// ErrNotFound is returned when no tunnel matches an id.
	ErrNotFound = errors.New("tunnel not found")
	// ErrConflict is returned when an id already exists.
	ErrConflict = errors.New("tunnel id already exists")
)

// Store persists tunnel records. Implementations must be safe for concurrent
// use.
type Store interface {
	// Create inserts a new tunnel, returning ErrConflict if the id is taken.
	Create(ctx context.Context, t *Tunnel) error
	// GetByID returns a tunnel or ErrNotFound.
	GetByID(ctx context.Context, id string) (*Tunnel, error)
	// MarkConnected records a successful tunnel connect (best-effort
	// telemetry). Implementations may ignore ErrNotFound.
	MarkConnected(ctx context.Context, id string) error
	// Close releases resources.
	Close()
}

// pepper is an optional server-wide secret mixed into every credential hash
// (injected via SECRET_PEPPER in production). Set once at startup before any
// hashing. When unset (dev/tests), Hash is a plain sha256 so behavior stays
// deterministic and self-consistent.
var pepper []byte

// SetPepper configures the server-wide hash pepper. Call once at startup; an
// empty pepper leaves hashing as plain sha256. Changing the pepper invalidates
// every previously stored hash (clients must re-register).
func SetPepper(p []byte) { pepper = p }

// Hash returns a keyed hash of a high-entropy credential. These tokens are
// random (not user-chosen passwords), so a fast keyed hash with a constant-time
// compare on verification is the right tool — bcrypt/argon would only add
// latency without adding meaningful resistance. With a pepper set it is
// HMAC-SHA256(pepper, secret); otherwise plain sha256(secret).
func Hash(secret string) []byte {
	if len(pepper) > 0 {
		m := hmac.New(sha256.New, pepper)
		m.Write([]byte(secret))
		return m.Sum(nil)
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// NewID returns a 12-character DNS-label-safe tunnel id. 36^12 ≈ 4.7e18 of
// keyspace makes subdomain guessing infeasible; the id doubles as the
// subdomain.
func NewID() (string, error) {
	const n = 12
	out := make([]byte, n)
	buf := make([]byte, n)
	i := 0
	for i < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			// Rejection sampling avoids modulo bias (256 % 36 != 0).
			if int(b) >= 256-(256%len(idAlphabet)) {
				continue
			}
			out[i] = idAlphabet[int(b)%len(idAlphabet)]
			i++
			if i == n {
				break
			}
		}
	}
	return string(out), nil
}

// NewSecret returns a 32-byte base64url connect secret (tunnel auth).
func NewSecret() (string, error) { return randToken(32) }

func randToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
