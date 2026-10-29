package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math/big"
)

// NewToken returns a 256-bit random token for a cookie or link, and the
// SHA-256 that is stored in its place.
func NewToken() (token string, hash []byte) {
	b := make([]byte, 32)
	rand.Read(b)
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// LinkTokenLength is the length of a public link's token: 128 random bits
// in base64url.
const LinkTokenLength = 22

// NewLinkToken returns a token for a public link and its SHA-256.
func NewLinkToken() (token string, hash []byte) {
	b := make([]byte, 16)
	rand.Read(b)
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token)
}

// ValidLinkToken reports whether s could be a link token, so that anything
// else is turned away before the database is asked.
func ValidLinkToken(s string) bool {
	if len(s) != LinkTokenLength {
		return false
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(b) == 16
}

// CodeDigits is the length of email codes.
const CodeDigits = 8

// NewCode returns a uniformly random 8-digit code.
func NewCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%08d", n.Int64())
}

// HashCode binds a code to its challenge, so equal codes for different
// challenges hash differently.
func HashCode(challenge, code string) []byte {
	h := sha256.Sum256([]byte(challenge + ":" + code))
	return h[:]
}

// Equal compares two hashes in constant time.
func Equal(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
