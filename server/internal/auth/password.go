// Package auth holds the building blocks of sign-in: password hashing and
// rules, session tokens and email codes. It knows nothing about the
// database; package accounts puts the pieces together.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters from docs/architecture.md §5.
const (
	argonMemory  = 64 * 1024 // KiB
	argonTime    = 3
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16

	// MinPasswordLength counts characters, not bytes.
	MinPasswordLength = 12
	// MaxPasswordLength stops someone from making us hash megabytes.
	MaxPasswordLength = 256
)

var (
	ErrPasswordTooShort = errors.New("password too short")
	ErrPasswordTooLong  = errors.New("password too long")
	ErrPasswordCommon   = errors.New("password too common")
)

// CheckPassword applies the password rules. related holds values the
// password must not equal, such as the email address.
func CheckPassword(pw string, related ...string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinPasswordLength:
		return ErrPasswordTooShort
	case n > MaxPasswordLength:
		return ErrPasswordTooLong
	}
	lower := strings.ToLower(pw)
	if commonPasswords[lower] || isTrivial(lower) {
		return ErrPasswordCommon
	}
	// A related value with a few characters around it is no better than the
	// value itself.
	for _, r := range append(related, "homedrive") {
		r = strings.ToLower(strings.TrimSpace(r))
		if len(r) >= 4 && strings.Contains(lower, r) && n-utf8.RuneCountInString(r) < 6 {
			return ErrPasswordCommon
		}
	}
	return nil
}

// isTrivial catches one repeated character and runs like 1234567890ab.
func isTrivial(pw string) bool {
	distinct := map[rune]bool{}
	for _, c := range pw {
		distinct[c] = true
	}
	if len(distinct) <= 3 {
		return true
	}
	for _, run := range []string{"0123456789012345", "9876543210987654", "abcdefghijklmnopqrstuvwxyz", "qwertyuiopasdfghjklzxcvbnm"} {
		if strings.Contains(run, pw) {
			return true
		}
	}
	return false
}

//go:embed common-passwords.txt
var commonPasswordsText string

var commonPasswords = func() map[string]bool {
	m := map[string]bool{}
	for _, line := range strings.Split(commonPasswordsText, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			m[strings.ToLower(line)] = true
		}
	}
	return m
}()

// Hasher hashes and verifies passwords with Argon2id. Each hash takes 64 MiB,
// so at most two run at the same time; further callers wait their turn.
type Hasher struct{ slots chan struct{} }

func NewHasher() *Hasher { return &Hasher{slots: make(chan struct{}, 2)} }

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hasher) release() { <-h.slots }

// Hash returns an encoded hash in the usual $argon2id$ format.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	salt := make([]byte, argonSaltLen)
	rand.Read(salt)
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reports whether password matches encoded. With encoded empty it
// still does the work of one hash, so unknown accounts take as long as
// known ones.
func (h *Hasher) Verify(ctx context.Context, encoded, password string) (bool, error) {
	if len(password) > MaxPasswordLength*4 {
		return false, nil
	}
	var (
		version, mem, t int
		threads         uint8
		saltB64, keyB64 string
	)
	parts := strings.Split(encoded, "$")
	ok := len(parts) == 6 && parts[1] == "argon2id"
	if ok {
		_, err1 := fmt.Sscanf(parts[2], "v=%d", &version)
		_, err2 := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &threads)
		saltB64, keyB64 = parts[4], parts[5]
		ok = err1 == nil && err2 == nil && version == argon2.Version &&
			mem > 0 && mem <= 1<<20 && t > 0 && t <= 10 && threads > 0
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(saltB64)
	want, err2 := base64.RawStdEncoding.DecodeString(keyB64)
	if !ok || err1 != nil || err2 != nil || len(want) == 0 {
		// Unknown account or damaged hash: spend the same effort anyway.
		mem, t, threads = argonMemory, argonTime, argonThreads
		salt, want, ok = make([]byte, argonSaltLen), make([]byte, argonKeyLen), false
	}

	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()
	got := argon2.IDKey([]byte(password), salt, uint32(t), uint32(mem), threads, uint32(len(want)))
	return ok && subtle.ConstantTimeCompare(got, want) == 1, nil
}
