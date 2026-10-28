package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/BertVanBaelen/HomeDrive/server/internal/auth"
	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// Admin recovery is for an admin who lost their password: there is no
// other admin to send them a link. The operator sets admin_recovery=true
// and restarts; a one-time link appears in the log, like the setup link.
// On that page the admin enters their address and a new password, and
// confirms with a code sent to that address. So recovery needs both access
// to the server's log and to the admin's mailbox.

// StartRecovery creates a recovery token and returns the link to log, or ""
// when there is no admin to recover (then setup applies).
func (s *Service) StartRecovery(ctx context.Context) (string, error) {
	var admins int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL`).Scan(&admins); err != nil || admins == 0 {
		return "", err
	}
	token, hash := auth.NewToken()
	s.recovery.mu.Lock()
	s.recovery.tokenHash, s.recovery.expires, s.recovery.pending = hash, s.now().Add(setupValidity), nil
	s.recovery.mu.Unlock()
	return s.origin + "/recover#" + token, nil
}

func (s *Service) checkRecoveryToken(token string) error {
	r := &s.recovery
	if r.tokenHash == nil || !s.now().Before(r.expires) || !auth.Equal(r.tokenHash, auth.HashToken(token)) {
		return ErrSetupUnavailable
	}
	return nil
}

func (s *Service) CheckRecovery(token string) error {
	s.recovery.mu.Lock()
	defer s.recovery.mu.Unlock()
	return s.checkRecoveryToken(token)
}

// BeginRecovery checks that email belongs to an active admin and mails a
// code there. Any other address gets the same answer but no mail, so the
// page doesn't reveal who the admins are.
func (s *Service) BeginRecovery(ctx context.Context, token, email, password string) error {
	if err := s.CheckRecovery(token); err != nil {
		return err
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = ? AND role = 'admin' AND disabled_at IS NULL`, email))
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if found {
		if err := auth.CheckPassword(password, u.Email, u.Name); err != nil {
			return err
		}
	} else if err := auth.CheckPassword(password, email); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return err
	}
	code := auth.NewCode()
	p := &pendingAdmin{userID: u.ID, email: email, passwordHash: hash,
		codeHash: auth.HashCode("recover", code), codeExpires: s.now().Add(codeValidity)}

	s.recovery.mu.Lock()
	err = s.checkRecoveryToken(token)
	if err == nil {
		s.recovery.pending = p
	}
	s.recovery.mu.Unlock()
	if err != nil || !found {
		return err
	}
	if err := s.mail.Send(ctx, codeMessage(u.Email, u.Locale, code)); err != nil {
		return fmt.Errorf("sending recovery code: %w", err)
	}
	return nil
}

// FinishRecovery checks the code, sets the new password and ends the
// admin's other sessions. The recovery link stops working.
func (s *Service) FinishRecovery(ctx context.Context, token, code string) (User, error) {
	s.recovery.mu.Lock()
	defer s.recovery.mu.Unlock()
	if err := s.checkRecoveryToken(token); err != nil {
		return User{}, err
	}
	p := s.recovery.pending
	if p == nil {
		return User{}, ErrSetupNotStarted
	}
	if p.userID == "" || p.attempts >= codeAttempts || !s.now().Before(p.codeExpires) {
		return User{}, ErrInvalidCode
	}
	if len(code) != auth.CodeDigits || !auth.Equal(p.codeHash, auth.HashCode("recover", code)) {
		p.attempts++
		return User{}, ErrInvalidCode
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ? AND role = 'admin' AND disabled_at IS NULL`,
		p.passwordHash, p.userID)
	if err != nil {
		return User{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return User{}, ErrInvalidCode
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, p.userID); err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invitations SET used_at = ? WHERE user_id = ? AND used_at IS NULL`,
		db.FormatTime(s.now()), p.userID); err != nil {
		return User{}, err
	}
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, p.userID))
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	s.recovery.tokenHash, s.recovery.pending = nil, nil
	return u, nil
}
