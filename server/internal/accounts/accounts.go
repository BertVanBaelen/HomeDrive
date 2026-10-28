// Package accounts manages users, sign-in and sessions on top of the
// database: password + email code login, the first-admin setup link, and
// session cookies.
package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BertVanBaelen/HomeDrive/server/internal/auth"
	"github.com/BertVanBaelen/HomeDrive/server/internal/db"
	"github.com/BertVanBaelen/HomeDrive/server/internal/files"
	hdmail "github.com/BertVanBaelen/HomeDrive/server/internal/mail"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidCode        = errors.New("invalid or expired code")
	ErrTooManyCodes       = errors.New("too many codes requested")
	ErrInvalidEmail       = errors.New("invalid email address")
	ErrInvalidName        = errors.New("invalid name")
	ErrEmailTaken         = errors.New("email address already in use")
	ErrNoSession          = errors.New("no valid session")
)

const (
	codeValidity     = 10 * time.Minute
	codeAttempts     = 5
	codesPerHour     = 5
	sessionIdle      = 30 * 24 * time.Hour
	sessionAbsolute  = 90 * 24 * time.Hour
	lastSeenInterval = 5 * time.Minute
	// RecentAuth is how long after signing in sensitive actions are allowed
	// without asking for the password again.
	RecentAuth = 15 * time.Minute
)

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Locale    string    `json:"locale"`
	CreatedAt time.Time `json:"createdAt"`
}

func (u User) IsAdmin() bool { return u.Role == "admin" }

type Session struct {
	UserID       string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	RecentAuthAt time.Time
}

type Service struct {
	db     *sql.DB
	hasher *auth.Hasher
	mail   hdmail.Sender
	log    *slog.Logger
	origin string // public_url, for links in emails
	now    func() time.Time

	setup setupState
}

func New(conn *sql.DB, sender hdmail.Sender, log *slog.Logger, origin string) *Service {
	return &Service{db: conn, hasher: auth.NewHasher(), mail: sender, log: log, origin: origin, now: time.Now}
}

// NormalizeEmail trims and lower-cases an address and checks its form.
func NormalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || len(s) > 254 || !strings.Contains(s[strings.IndexByte(s, '@'):], ".") {
		return "", ErrInvalidEmail
	}
	return s, nil
}

// NormalizeName trims a display name and checks its length.
func NormalizeName(s string) (string, error) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || utf8.RuneCountInString(s) > 80 || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", ErrInvalidName
	}
	return s, nil
}

func normalizeLocale(s string) string {
	if s == "nl" || s == "en" {
		return s
	}
	return ""
}

// CountUsers is zero until the first admin has been set up.
func (s *Service) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

const userColumns = `id, email, name, role, locale, created_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var created string
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Locale, &created); err != nil {
		return User{}, err
	}
	u.CreatedAt, _ = db.ParseTime(created)
	return u, nil
}

func (s *Service) User(ctx context.Context, id string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ? AND disabled_at IS NULL`, id))
}

// createUser inserts a user and gives them their drives, inside tx.
func (s *Service) createUser(ctx context.Context, tx *sql.Tx, email, name, passwordHash, role, locale string) (User, error) {
	now := s.now()
	u := User{ID: db.NewID(), Email: email, Name: name, Role: role, Locale: normalizeLocale(locale), CreatedAt: now}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, email, name, password_hash, role, locale, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.Name, passwordHash, u.Role, u.Locale, db.FormatTime(now))
	if db.IsUniqueViolation(err) {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, err
	}
	if err := files.ProvisionUser(ctx, tx, u.ID, now); err != nil {
		return User{}, fmt.Errorf("creating drives: %w", err)
	}
	return u, nil
}

// CreateUser adds an account with its drives. role is "admin" or "member".
func (s *Service) CreateUser(ctx context.Context, email, name, password, role, locale string) (User, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	if name, err = NormalizeName(name); err != nil {
		return User{}, err
	}
	if role != "admin" && role != "member" {
		return User{}, fmt.Errorf("invalid role %q", role)
	}
	if err := auth.CheckPassword(password, email, name); err != nil {
		return User{}, err
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	u, err := s.createUser(ctx, tx, email, name, hash, role, locale)
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}

// Authenticate checks an email address and password. Unknown addresses,
// disabled accounts and wrong passwords all give ErrInvalidCredentials and
// take the same time.
func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	email, _ = NormalizeEmail(email)
	var hash string
	var disabled sql.NullString
	u, err := scanUserWith(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+`, password_hash, disabled_at FROM users WHERE email = ?`, email), &hash, &disabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	ok, verr := s.hasher.Verify(ctx, hash, password)
	if verr != nil {
		return User{}, verr
	}
	if err != nil || !ok || disabled.Valid {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

func scanUserWith(row *sql.Row, extra ...any) (User, error) {
	var u User
	var created string
	dest := append([]any{&u.ID, &u.Email, &u.Name, &u.Role, &u.Locale, &created}, extra...)
	if err := row.Scan(dest...); err != nil {
		return User{}, err
	}
	u.CreatedAt, _ = db.ParseTime(created)
	return u, nil
}

// SendLoginCode emails a fresh code to u and returns the challenge ID the
// browser needs to submit it.
func (s *Service) SendLoginCode(ctx context.Context, u User) (string, error) {
	since := db.FormatTime(s.now().Add(-time.Hour))
	var recent int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM email_codes WHERE user_id = ? AND created_at > ?`, u.ID, since).Scan(&recent); err != nil {
		return "", err
	}
	if recent >= codesPerHour {
		return "", ErrTooManyCodes
	}
	challenge, code := db.NewID(), auth.NewCode()
	now := s.now()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO email_codes (id, user_id, purpose, code_hash, created_at, expires_at) VALUES (?, ?, 'login', ?, ?, ?)`,
		challenge, u.ID, auth.HashCode(challenge, code), db.FormatTime(now), db.FormatTime(now.Add(codeValidity))); err != nil {
		return "", err
	}
	if err := s.mail.Send(ctx, codeMessage(u.Email, u.Locale, code)); err != nil {
		return "", fmt.Errorf("sending login code: %w", err)
	}
	return challenge, nil
}

// VerifyLoginCode checks a code against its challenge. Each challenge
// allows a few attempts and one success.
func (s *Service) VerifyLoginCode(ctx context.Context, challenge, code string) (User, error) {
	if !db.ValidID(challenge) || len(code) != auth.CodeDigits {
		return User{}, ErrInvalidCode
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var (
		userID, expires string
		hash            []byte
		attempts        int
		used            sql.NullString
	)
	err = tx.QueryRowContext(ctx,
		`SELECT user_id, code_hash, expires_at, attempts, used_at FROM email_codes WHERE id = ? AND purpose = 'login'`,
		challenge).Scan(&userID, &hash, &expires, &attempts, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCode
	}
	if err != nil {
		return User{}, err
	}
	if used.Valid || attempts >= codeAttempts || expires <= db.FormatTime(s.now()) {
		return User{}, ErrInvalidCode
	}
	if !auth.Equal(hash, auth.HashCode(challenge, code)) {
		if _, err := tx.ExecContext(ctx, `UPDATE email_codes SET attempts = attempts + 1 WHERE id = ?`, challenge); err != nil {
			return User{}, err
		}
		if err := tx.Commit(); err != nil {
			return User{}, err
		}
		return User{}, ErrInvalidCode
	}
	if _, err := tx.ExecContext(ctx, `UPDATE email_codes SET used_at = ? WHERE id = ?`, db.FormatTime(s.now()), challenge); err != nil {
		return User{}, err
	}
	u, err := scanUser(tx.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ? AND disabled_at IS NULL`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidCode
	}
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}

// CreateSession starts a session for u and returns the cookie token.
func (s *Service) CreateSession(ctx context.Context, userID, userAgent, ip string) (string, error) {
	token, hash := auth.NewToken()
	now := db.FormatTime(s.now())
	if len(userAgent) > 300 {
		userAgent = userAgent[:300]
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, user_id, created_at, last_seen_at, expires_at, recent_auth_at, user_agent, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		hash, userID, now, now, db.FormatTime(s.now().Add(sessionIdle)), now, userAgent, ip)
	return token, err
}

// SessionUser looks up a session token. It slides the idle expiry forward,
// never past the absolute limit.
func (s *Service) SessionUser(ctx context.Context, token string) (User, Session, error) {
	if token == "" || len(token) > 100 {
		return User{}, Session{}, ErrNoSession
	}
	hash := auth.HashToken(token)
	var created, lastSeen, expires, recent string
	u, err := scanUserWith(s.db.QueryRowContext(ctx,
		`SELECT u.id, u.email, u.name, u.role, u.locale, u.created_at, s.created_at, s.last_seen_at, s.expires_at, s.recent_auth_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.id_hash = ? AND u.disabled_at IS NULL`, hash), &created, &lastSeen, &expires, &recent)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, Session{}, ErrNoSession
	}
	if err != nil {
		return User{}, Session{}, err
	}
	now := s.now()
	sess := Session{UserID: u.ID}
	sess.CreatedAt, _ = db.ParseTime(created)
	sess.ExpiresAt, _ = db.ParseTime(expires)
	sess.RecentAuthAt, _ = db.ParseTime(recent)
	absolute := sess.CreatedAt.Add(sessionAbsolute)
	if !now.Before(sess.ExpiresAt) || !now.Before(absolute) {
		s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hash)
		return User{}, Session{}, ErrNoSession
	}
	if seen, _ := db.ParseTime(lastSeen); now.Sub(seen) > lastSeenInterval {
		sess.ExpiresAt = now.Add(sessionIdle)
		if sess.ExpiresAt.After(absolute) {
			sess.ExpiresAt = absolute
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id_hash = ?`,
			db.FormatTime(now), db.FormatTime(sess.ExpiresAt), hash); err != nil {
			s.log.Warn("updating session", "error", err)
		}
	}
	return u, sess, nil
}

func (s *Service) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, auth.HashToken(token))
	return err
}

// Audit records a security-relevant event.
func (s *Service) Audit(ctx context.Context, userID, ip, event, detail string) {
	var uid any
	if userID != "" {
		uid = userID
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO audit_log (at, user_id, ip, event, detail) VALUES (?, ?, ?, ?, ?)`,
		db.FormatTime(s.now()), uid, ip, event, detail); err != nil {
		s.log.Warn("writing audit log", "event", event, "error", err)
	}
}
