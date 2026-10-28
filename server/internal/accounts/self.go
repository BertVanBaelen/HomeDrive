package accounts

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/BertVanBaelen/HomeDrive/server/internal/auth"
	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// What signed-in users can do to their own account.

var ErrWrongPassword = errors.New("wrong password")

func (s *Service) passwordHash(ctx context.Context, userID string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash)
	return hash, err
}

func (s *Service) checkPassword(ctx context.Context, userID, password string) error {
	hash, err := s.passwordHash(ctx, userID)
	if err != nil {
		return err
	}
	ok, err := s.hasher.Verify(ctx, hash, password)
	if err != nil {
		return err
	}
	if !ok {
		return ErrWrongPassword
	}
	return nil
}

// Reauth confirms the password for the current session, which allows
// sensitive actions for the next few minutes.
func (s *Service) Reauth(ctx context.Context, sess Session, password string) error {
	if err := s.checkPassword(ctx, sess.UserID, password); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET recent_auth_at = ? WHERE id_hash = ?`, db.FormatTime(s.now()), sess.hash)
	return err
}

// UpdateProfile changes the user's display name and/or language.
func (s *Service) UpdateProfile(ctx context.Context, userID string, name, locale *string) (User, error) {
	if name != nil {
		n, err := NormalizeName(*name)
		if err != nil {
			return User{}, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET name = ? WHERE id = ?`, n, userID); err != nil {
			return User{}, err
		}
	}
	if locale != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET locale = ? WHERE id = ?`, normalizeLocale(*locale), userID); err != nil {
			return User{}, err
		}
	}
	return s.User(ctx, userID)
}

// ChangePassword sets a new password after checking the current one. All
// other sessions of the user end; this one stays.
func (s *Service) ChangePassword(ctx context.Context, u User, sess Session, current, next string) error {
	if err := s.checkPassword(ctx, u.ID, current); err != nil {
		return err
	}
	if err := auth.CheckPassword(next, u.Email, u.Name); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, next)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := db.FormatTime(s.now())
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, u.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id_hash != ?`, u.ID, sess.hash); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET recent_auth_at = ? WHERE id_hash = ?`, now, sess.hash); err != nil {
		return err
	}
	return tx.Commit()
}

// SessionInfo is one signed-in browser or app in the device list.
type SessionInfo struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	UserAgent  string    `json:"userAgent"`
	IP         string    `json:"ip"`
	Current    bool      `json:"current"`
}

// sessionID is the public handle of a session: the start of its token's
// hash, which is itself not secret.
func sessionID(hash []byte) string { return hex.EncodeToString(hash[:12]) }

func (s *Service) Sessions(ctx context.Context, sess Session) ([]SessionInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id_hash, created_at, last_seen_at, user_agent, ip FROM sessions
		 WHERE user_id = ? AND expires_at > ? ORDER BY last_seen_at DESC`, sess.UserID, db.FormatTime(s.now()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionInfo{}
	for rows.Next() {
		var (
			hash          []byte
			created, seen string
			info          SessionInfo
		)
		if err := rows.Scan(&hash, &created, &seen, &info.UserAgent, &info.IP); err != nil {
			return nil, err
		}
		info.ID = sessionID(hash)
		info.CreatedAt, _ = db.ParseTime(created)
		info.LastSeenAt, _ = db.ParseTime(seen)
		info.Current = auth.Equal(hash, sess.hash)
		out = append(out, info)
	}
	return out, rows.Err()
}

// RevokeSession signs out one of the user's own sessions.
func (s *Service) RevokeSession(ctx context.Context, userID, id string) error {
	if len(id) != 24 {
		return ErrNotFound
	}
	if _, err := hex.DecodeString(id); err != nil {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND lower(hex(substr(id_hash, 1, 12))) = ?`, userID, strings.ToLower(id))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
