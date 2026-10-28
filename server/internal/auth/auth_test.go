package auth

import (
	"context"
	"strings"
	"testing"
)

func TestCheckPassword(t *testing.T) {
	for _, tc := range []struct {
		pw   string
		want error
	}{
		{"short", ErrPasswordTooShort},
		{"elf tekens!", ErrPasswordTooShort},
		{"twelve chars", nil},
		{"Password1234", ErrPasswordCommon},
		{"aaaaaaaaaaaaaaa", ErrPasswordCommon},
		{"abababababababab", ErrPasswordCommon},
		{"1234567890123", ErrPasswordCommon},
		{"qwertyuiopasdf", ErrPasswordCommon},
		{"homedrive123", ErrPasswordCommon},
		{"anna@example.com", ErrPasswordCommon},
		{"anna@example.com!!", ErrPasswordCommon},
		{"tulips in the rain", nil},
		{strings.Repeat("x", 300), ErrPasswordTooLong},
	} {
		if got := CheckPassword(tc.pw, "anna@example.com", "Anna"); got != tc.want {
			t.Errorf("CheckPassword(%q) = %v, want %v", tc.pw, got, tc.want)
		}
	}
}

func TestHashAndVerify(t *testing.T) {
	h := NewHasher()
	ctx := context.Background()
	enc, err := h.Hash(ctx, "tulips in the rain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=65536,t=3,p=1$") {
		t.Fatalf("encoded = %q", enc)
	}
	for _, tc := range []struct {
		enc, pw string
		want    bool
	}{
		{enc, "tulips in the rain", true},
		{enc, "tulips in the rain ", false},
		{"", "tulips in the rain", false},
		{"$argon2id$garbage", "tulips in the rain", false},
	} {
		if got, err := h.Verify(ctx, tc.enc, tc.pw); got != tc.want || err != nil {
			t.Errorf("Verify(%q, %q) = %v, %v", tc.enc, tc.pw, got, err)
		}
	}
}

func TestCodes(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		c := NewCode()
		if len(c) != CodeDigits || strings.Trim(c, "0123456789") != "" {
			t.Fatalf("code %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 95 {
		t.Fatalf("only %d distinct codes in 100", len(seen))
	}
	if Equal(HashCode("a", "12345678"), HashCode("b", "12345678")) {
		t.Fatal("code hash doesn't depend on the challenge")
	}
}
