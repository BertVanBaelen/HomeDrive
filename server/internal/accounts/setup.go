package accounts

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/auth"
)

// The first admin is created through a one-time link that is written to the
// log at startup while there are no users. The link's token lives in the
// URL fragment, so it never reaches a proxy or Cloudflare's logs. Setup
// state is kept in memory only: a restart prints a fresh link.

var (
	ErrSetupUnavailable = errors.New("setup link invalid or expired")
	ErrSetupNotStarted  = errors.New("setup not started")
)

const setupValidity = time.Hour

type setupState struct {
	mu        sync.Mutex
	tokenHash []byte
	expires   time.Time
	pending   *pendingAdmin
}

type pendingAdmin struct {
	userID, email, name, locale, passwordHash string
	codeHash                                  []byte
	codeExpires                               time.Time
	attempts                                  int
}

// StartSetup creates a setup token if there are no users yet. It returns
// the link to log, or "" when setup isn't needed.
func (s *Service) StartSetup(ctx context.Context) (string, error) {
	n, err := s.CountUsers(ctx)
	if err != nil || n > 0 {
		return "", err
	}
	token, hash := auth.NewToken()
	s.setup.mu.Lock()
	s.setup.tokenHash, s.setup.expires, s.setup.pending = hash, s.now().Add(setupValidity), nil
	s.setup.mu.Unlock()
	return s.origin + "/setup#" + token, nil
}

// checkSetupToken must be called with s.setup.mu held.
func (s *Service) checkSetupToken(token string) error {
	if s.setup.tokenHash == nil || !s.now().Before(s.setup.expires) || !auth.Equal(s.setup.tokenHash, auth.HashToken(token)) {
		return ErrSetupUnavailable
	}
	return nil
}

// CheckSetup reports whether token is a valid, unused setup token.
func (s *Service) CheckSetup(token string) error {
	s.setup.mu.Lock()
	defer s.setup.mu.Unlock()
	return s.checkSetupToken(token)
}

// BeginSetup validates the first admin's details and emails them a code.
// Calling it again replaces the details and sends a new code.
func (s *Service) BeginSetup(ctx context.Context, token, email, name, password, locale string) error {
	if err := s.CheckSetup(token); err != nil {
		return err
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	if name, err = NormalizeName(name); err != nil {
		return err
	}
	if err := auth.CheckPassword(password, email, name); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return err
	}
	code := auth.NewCode()
	p := &pendingAdmin{
		email: email, name: name, locale: normalizeLocale(locale), passwordHash: hash,
		codeHash: auth.HashCode("setup", code), codeExpires: s.now().Add(codeValidity),
	}

	s.setup.mu.Lock()
	err = s.checkSetupToken(token)
	if err == nil {
		s.setup.pending = p
	}
	s.setup.mu.Unlock()
	if err != nil {
		return err
	}
	if err := s.mail.Send(ctx, codeMessage(email, p.locale, code)); err != nil {
		return fmt.Errorf("sending setup code: %w", err)
	}
	return nil
}

// FinishSetup checks the emailed code and creates the admin account. The
// setup link stops working afterwards.
func (s *Service) FinishSetup(ctx context.Context, token, code string) (User, error) {
	s.setup.mu.Lock()
	defer s.setup.mu.Unlock()
	if err := s.checkSetupToken(token); err != nil {
		return User{}, err
	}
	p := s.setup.pending
	if p == nil {
		return User{}, ErrSetupNotStarted
	}
	if p.attempts >= codeAttempts || !s.now().Before(p.codeExpires) {
		return User{}, ErrInvalidCode
	}
	if len(code) != auth.CodeDigits || !auth.Equal(p.codeHash, auth.HashCode("setup", code)) {
		p.attempts++
		return User{}, ErrInvalidCode
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return User{}, err
	}
	if n > 0 {
		return User{}, ErrSetupUnavailable
	}
	u, err := s.createUser(ctx, tx, p.email, p.name, p.passwordHash, "admin", p.locale, nil)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	s.setup.tokenHash, s.setup.pending = nil, nil
	return u, nil
}
