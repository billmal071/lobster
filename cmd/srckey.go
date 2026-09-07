package cmd

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"

	"lobster/internal/config"
)

// refKeyBytes is the length of the per-installation ref-signing secret.
const refKeyBytes = 32

var (
	refKeyOnce sync.Once
	refKeyVal  []byte
)

// refSourceSecret returns the per-installation secret that keys sourceKey,
// creating it on first use. It returns nil when no secret can be persisted.
//
// The secret is read from (or written to) config.RefKeyPath with 0600
// permissions. It is generated once per installation and never leaves the
// machine: it is not derived from the playlist source, not printed, and not
// carried in a ref. Only the HMAC of a source under it travels.
//
// nil is a supported outcome, not an error path. A read-only home directory,
// a container with no writable state dir, or a corrupt key file all land
// here, and sourceKey degrades to returning "" rather than falling back to an
// unkeyed digest — see sourceKey for why that degradation is the safe one.
// The result is memoized for the process so a ref minted and a ref resolved
// in the same run always agree.
func refSourceSecret() []byte {
	refKeyOnce.Do(func() { refKeyVal = loadOrCreateRefKey() })
	return refKeyVal
}

func loadOrCreateRefKey() []byte {
	path, err := config.RefKeyPath()
	if err != nil {
		return nil
	}
	// A short or truncated file is treated as absent and rewritten: a
	// half-written key is not a key, and keeping it would silently weaken
	// every digest minted for the life of the installation.
	if b, err := os.ReadFile(path); err == nil && len(b) >= refKeyBytes {
		return b[:refKeyBytes]
	}
	key := make([]byte, refKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil
	}
	// 0600, and written through a temp file + rename so a concurrent reader
	// never sees a partial key. Losing the race is harmless: both writers
	// produce a valid key, and the loser's refs are re-minted on the next
	// `lobster channels` call.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".refkey-*")
	if err != nil {
		return nil
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return nil
	}
	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		return nil
	}
	if err := tmp.Close(); err != nil {
		return nil
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return nil
	}
	return key
}

// sourceKey is the value live-ref matching compares two playlist sources by.
//
// displaySource cannot serve as that value. It drops the entire query string,
// which is exactly where an Xtream source keeps its credentials, so two
// subscriptions to one server —
// "https://host/get.php?username=alice&password=..." and
// "...username=bob&password=..." — reduce to the same "https://host/get.php".
// Matching on that string, a ref minted from alice's playlist would be
// accepted against a channel loaded from bob's: if the channel is gone from
// alice's playlist and bob's carries one channel with the same tvg-id,
// filterBySource keeps it and resolveLiveRef plays it as a unique match.
// Silently playing a stream from a different subscription is the class of
// wrong-content bug the whole live-ref design exists to prevent.
//
// The digest is an HMAC under a per-installation secret, not a plain hash of
// the source. A plain SHA-256 would be reversible in practice here: the ref
// already carries "https://host/get.php" in the clear as its display Source,
// so an attacker holding a ref knows the scheme, host and path, and the only
// unknown left is the username and password. Hashing candidate URLs offline
// until one matches is then a straight dictionary attack against the
// subscription credentials, and refs are printed to an agent's stdout, which
// is exactly where they leak. Under an HMAC the attacker cannot compute a
// candidate digest at all without the secret, which never leaves the machine.
//
// It is truncated to 128 bits: collision resistance far beyond the number of
// playlists any one user configures, without spending another 32 characters
// of every ref.
//
// It returns "" when no secret can be persisted (see refSourceSecret). That
// omits src_key from the ref entirely, so matching falls back to narrowing by
// display Source — the pre-src_key behaviour, which cannot separate two
// subscriptions on one endpoint but is otherwise correct. Falling back to an
// unkeyed digest instead would hand out exactly the guessable value this
// function exists to avoid, and silently: the degraded path must lose a
// capability, never a safety property.
//
// It is never displayed. displaySource remains the only thing shown to a
// human — "https://host/get.php" tells a user which playlist is down, and a
// hex digest does not.
func sourceKey(raw string) string {
	secret := refSourceSecret()
	if len(secret) == 0 {
		return ""
	}
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(raw))
	return hex.EncodeToString(m.Sum(nil)[:16])
}
