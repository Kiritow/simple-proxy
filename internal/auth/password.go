package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// MinPasswordLength is the length that passwords need at least, in
// characters.
const MinPasswordLength = 8

// maxPasswordLength keeps hashing from being fed megabytes.
const maxPasswordLength = 1024

// Argon2id parameters, as OWASP recommends them: 19 MiB of memory, two
// iterations, one thread.
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

var b64 = base64.RawStdEncoding

// hashing limits how many passwords are hashed at once, as each hash takes
// argonMemory: requests beyond wait their turn.
var hashing = make(chan struct{}, max(runtime.GOMAXPROCS(0), 2))

func argonKey(password string, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
	hashing <- struct{}{}
	defer func() { <-hashing }()
	return argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)
}

// HashPassword returns an Argon2id hash of password, in the PHC string
// format: $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>.
func HashPassword(password string) string {
	salt := make([]byte, argonSaltLen)
	rand.Read(salt)
	key := argonKey(password, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// IsHash reports whether s is a password hash, as HashPassword returns
// them, rather than a password.
func IsHash(s string) bool {
	_, err := parseHash(s)
	return err == nil
}

type argonHash struct {
	memory    uint32
	time      uint32
	threads   uint8
	salt, key []byte
}

func parseHash(s string) (argonHash, error) {
	var h argonHash
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return h, errors.New("not an Argon2id hash in the PHC format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return h, fmt.Errorf("unsupported Argon2 version %q", parts[2])
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &h.memory, &h.time, &h.threads); err != nil {
		return h, fmt.Errorf("invalid Argon2 parameters %q", parts[3])
	}
	if h.memory == 0 || h.memory > 1<<20 || h.time == 0 || h.time > 100 || h.threads == 0 {
		return h, fmt.Errorf("unreasonable Argon2 parameters %q", parts[3])
	}
	var err error
	if h.salt, err = b64.DecodeString(parts[4]); err != nil {
		return h, fmt.Errorf("invalid salt: %w", err)
	}
	if h.key, err = b64.DecodeString(parts[5]); err != nil || len(h.key) < 16 {
		return h, errors.New("invalid hash")
	}
	return h, nil
}

// CheckPassword reports whether password matches hash. It is false for an
// empty or invalid hash.
func CheckPassword(hash, password string) bool {
	h, err := parseHash(hash)
	if err != nil || len(password) > maxPasswordLength {
		return false
	}
	key := argonKey(password, h.salt, h.time, h.memory, h.threads, uint32(len(h.key)))
	return subtle.ConstantTimeCompare(key, h.key) == 1
}

// dummyHash is checked against when there is no user, so that failing to
// sign in takes as long whether the user exists or not.
var dummyHash = HashPassword(rand.Text())
