package auth

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewLinkToken(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		token, hash := NewLinkToken()
		if !ValidLinkToken(token) || !Equal(hash, HashToken(token)) || seen[token] {
			t.Fatalf("token %q", token)
		}
		seen[token] = true
	}
}

// FuzzValidLinkToken checks that every accepted token is exactly 128 bits
// in canonical base64url, so one link has one address.
func FuzzValidLinkToken(f *testing.F) {
	token, _ := NewLinkToken()
	for _, s := range []string{token, token[:21], token + "A", strings.Repeat("A", 22), strings.Repeat("_", 22),
		"AAAAAAAAAAAAAAAAAAAAAB", "AAAAAAAAAAAAAAAAAAAA==", "AAAAAAAAAAAAAAAAAAAA+/", "AAAAAAAAAA\nAAAAAAAAAAA", "", "../../etc/passwd"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !ValidLinkToken(s) {
			return
		}
		b, err := base64.RawURLEncoding.DecodeString(s)
		if len(s) != LinkTokenLength || err != nil || len(b) != 16 || base64.RawURLEncoding.EncodeToString(b) != s {
			t.Fatalf("ValidLinkToken accepted %q", s)
		}
	})
}
