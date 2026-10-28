package accounts

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/auth"
	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
)

// Device tokens let something without a browser session upload for a user:
// the iPhone Shortcut. A token can only add files to its owner's Inbox,
// shows up in the owner's account settings and can be revoked there.

var (
	ErrTooManyDevices = errors.New("too many device tokens")
	ErrNoDevice       = errors.New("invalid device token")
)

const (
	maxDevicesPerUser = 20
	// DeviceTokenPrefix makes tokens recognisable, for people and for
	// secret scanners.
	DeviceTokenPrefix = "hd_"
)

type DeviceToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
}

// CreateDeviceToken makes a new upload token for the user. The token is
// returned once and never stored.
func (s *Service) CreateDeviceToken(ctx context.Context, userID, name string) (DeviceToken, string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return DeviceToken{}, "", ErrInvalidName
	}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM device_tokens WHERE user_id = ? AND revoked_at IS NULL`, userID).Scan(&n); err != nil {
		return DeviceToken{}, "", err
	}
	if n >= maxDevicesPerUser {
		return DeviceToken{}, "", ErrTooManyDevices
	}
	raw, _ := auth.NewToken()
	token := DeviceTokenPrefix + raw
	d := DeviceToken{ID: db.NewID(), Name: name, CreatedAt: s.now()}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO device_tokens (id, user_id, name, token_hash, scope, created_at) VALUES (?, ?, ?, ?, 'upload', ?)`,
		d.ID, userID, d.Name, auth.HashToken(token), db.FormatTime(d.CreatedAt))
	return d, token, err
}

func (s *Service) DeviceTokens(ctx context.Context, userID string) ([]DeviceToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, created_at, last_used_at FROM device_tokens
		 WHERE user_id = ? AND revoked_at IS NULL ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceToken{}
	for rows.Next() {
		var (
			d       DeviceToken
			created string
			used    sql.NullString
		)
		if err := rows.Scan(&d.ID, &d.Name, &created, &used); err != nil {
			return nil, err
		}
		d.CreatedAt, _ = db.ParseTime(created)
		if used.Valid {
			t, _ := db.ParseTime(used.String)
			d.LastUsedAt = &t
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) RevokeDeviceToken(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE device_tokens SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		db.FormatTime(s.now()), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeviceUser resolves an upload token to its owner and notes its use.
func (s *Service) DeviceUser(ctx context.Context, token string) (User, string, error) {
	if !strings.HasPrefix(token, DeviceTokenPrefix) || len(token) > 100 {
		return User{}, "", ErrNoDevice
	}
	var id string
	u, err := scanUserWith(s.db.QueryRowContext(ctx,
		`SELECT u.id, u.email, u.name, u.role, u.locale, u.created_at, d.id
		 FROM device_tokens d JOIN users u ON u.id = d.user_id
		 WHERE d.token_hash = ? AND d.revoked_at IS NULL AND d.scope = 'upload' AND u.disabled_at IS NULL`,
		auth.HashToken(token)), &id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", ErrNoDevice
	}
	if err != nil {
		return User{}, "", err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE device_tokens SET last_used_at = ? WHERE id = ?`,
		db.FormatTime(s.now()), id); err != nil {
		s.log.Warn("noting device token use", "error", err)
	}
	return u, id, nil
}
