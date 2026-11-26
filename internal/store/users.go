package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Role is what a user may do in the console.
type Role string

const (
	// NoRole is the role of new accounts: they may sign in, but do nothing
	// else until an administrator gives them a role.
	NoRole Role = ""
	// SiteOperator manages the sites assigned to the user.
	SiteOperator Role = "site_operator"
	// PlatformOperator manages all sites.
	PlatformOperator Role = "platform_operator"
	// Admin may do everything, including managing certificates and users.
	Admin Role = "admin"
)

// Valid reports whether r is one of the roles above.
func (r Role) Valid() bool {
	switch r {
	case NoRole, SiteOperator, PlatformOperator, Admin:
		return true
	}
	return false
}

// User is an account of the console.
type User struct {
	ID string // see NewID
	// Username is unique, ignoring case. Users signing in with a password
	// enter it; for those signing in with OIDC, it is taken from their
	// claims.
	Username      string
	DisplayName   string
	Email         string
	EmailVerified bool // by the OIDC provider
	Role          Role
	// Sites are the IDs of the sites that a site operator manages. They are
	// kept whatever the role, and removed with the sites.
	Sites []string
	// PasswordHash is empty if the user cannot sign in with a password.
	PasswordHash string
	// OIDCIssuer and OIDCSubject identify the user at an OIDC provider; the
	// subject is empty if the user does not sign in with OIDC.
	OIDCIssuer, OIDCSubject string
	// Managed is set for the administrators listed in the configuration
	// file, which the console cannot change.
	Managed  bool
	Disabled bool
	// CreatedAt and UpdatedAt are maintained by the Store. LastLoginAt is
	// zero if the user never signed in.
	CreatedAt, UpdatedAt, LastLoginAt time.Time
}

// Session is a signed-in browser.
type Session struct {
	// ID is a hash of the token that the browser presents, so that the
	// database does not hold the tokens themselves.
	ID     string
	UserID string
	// Method is how the user signed in: "password" or "oidc".
	Method                       string
	CreatedAt, SeenAt, ExpiresAt time.Time
}

const userColumns = `id, username, display_name, email, email_verified, role, password_hash,
	oidc_issuer, oidc_subject, managed, disabled, created_at, updated_at, last_login_at,
	(SELECT coalesce(json_group_array(site_id), '[]') FROM user_sites WHERE user_id = users.id)`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var role, created, updated, login, sites string
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.EmailVerified, &role, &u.PasswordHash,
		&u.OIDCIssuer, &u.OIDCSubject, &u.Managed, &u.Disabled, &created, &updated, &login, &sites)
	if err != nil {
		return User{}, err
	}
	u.Role = Role(role)
	u.CreatedAt = parseTime(created)
	u.UpdatedAt = parseTime(updated)
	u.LastLoginAt = parseTime(login)
	u.Sites = parseIDs(sites)
	return u, nil
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// parseIDs parses a JSON array of strings, as json_group_array makes them.
func parseIDs(list string) []string {
	ids := []string{}
	_ = json.Unmarshal([]byte(list), &ids) // SQLite makes valid JSON
	return ids
}

// Users returns the users, by username.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+userColumns+" FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// User returns the user with the given ID, or fails with ErrNotFound.
func (s *Store) User(ctx context.Context, id string) (User, error) {
	return s.userWhere(ctx, "id = ?", id)
}

// UserByName returns the user with the given username, ignoring case, or
// fails with ErrNotFound.
func (s *Store) UserByName(ctx context.Context, username string) (User, error) {
	return s.userWhere(ctx, "username = ?", username)
}

// UserByOIDC returns the user with the given identity at an OIDC provider,
// or fails with ErrNotFound.
func (s *Store) UserByOIDC(ctx context.Context, issuer, subject string) (User, error) {
	return s.userWhere(ctx, "oidc_issuer = ? AND oidc_subject = ? AND oidc_subject != ''", issuer, subject)
}

func (s *Store) userWhere(ctx context.Context, where string, args ...any) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE "+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("user: %w", ErrNotFound)
	}
	return u, err
}

// AddUser stores a new user, which needs an ID from NewID. If another user
// has the same username or OIDC identity, it fails with ErrExists.
func (s *Store) AddUser(ctx context.Context, u User) error {
	if u.ID == "" {
		return fmt.Errorf("user %q has no ID", u.Username)
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		now := formatTime(time.Now())
		_, err := tx.ExecContext(ctx, `
			INSERT INTO users (id, username, display_name, email, email_verified, role, password_hash,
				oidc_issuer, oidc_subject, managed, disabled, created_at, updated_at, last_login_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			u.ID, u.Username, u.DisplayName, u.Email, u.EmailVerified, string(u.Role), u.PasswordHash,
			u.OIDCIssuer, u.OIDCSubject, u.Managed, u.Disabled, now, now, formatTime(u.LastLoginAt))
		if err != nil {
			return userError(u, err)
		}
		return setSites(ctx, tx, u)
	})
}

// UpdateUser stores the changes to a user, all fields but CreatedAt. It
// fails with ErrNotFound if there is no such user, and with ErrExists if
// another user has the same username or OIDC identity.
func (s *Store) UpdateUser(ctx context.Context, u User) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE users SET username = ?, display_name = ?, email = ?, email_verified = ?, role = ?,
				password_hash = ?, oidc_issuer = ?, oidc_subject = ?, managed = ?, disabled = ?,
				updated_at = ?, last_login_at = ?
			WHERE id = ?`,
			u.Username, u.DisplayName, u.Email, u.EmailVerified, string(u.Role), u.PasswordHash,
			u.OIDCIssuer, u.OIDCSubject, u.Managed, u.Disabled, formatTime(time.Now()),
			formatTime(u.LastLoginAt), u.ID)
		if err != nil {
			return userError(u, err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return fmt.Errorf("user %q: %w", u.Username, ErrNotFound)
		}
		return setSites(ctx, tx, u)
	})
}

// userError reports a violated uniqueness constraint as ErrExists.
func userError(u User, err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("user %q: %w", u.Username, ErrExists)
	}
	return err
}

func setSites(ctx context.Context, tx *sql.Tx, u User) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM user_sites WHERE user_id = ?", u.ID); err != nil {
		return err
	}
	for _, site := range u.Sites {
		_, err := tx.ExecContext(ctx,
			"INSERT INTO user_sites (user_id, site_id) VALUES (?, ?) ON CONFLICT DO NOTHING", u.ID, site)
		if err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
				return fmt.Errorf("site %q: %w", site, ErrNotFound)
			}
			return err
		}
	}
	return nil
}

// DeleteUser removes a user with their sessions. If there is no such user,
// it fails with ErrNotFound.
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("user: %w", ErrNotFound)
	}
	return nil
}

// RecordLogin sets the time a user last signed in.
func (s *Store) RecordLogin(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, "UPDATE users SET last_login_at = ? WHERE id = ?", formatTime(at), id)
	return err
}

// AddSession stores a new session.
func (s *Store) AddSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, method, created_at, seen_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.UserID, sess.Method, formatTime(sess.CreatedAt), formatTime(sess.SeenAt), formatTime(sess.ExpiresAt))
	return err
}

// Session returns the session with the given ID, expired or not, or fails
// with ErrNotFound.
func (s *Store) Session(ctx context.Context, id string) (Session, error) {
	var sess Session
	var created, seen, expires string
	err := s.db.QueryRowContext(ctx,
		"SELECT id, user_id, method, created_at, seen_at, expires_at FROM sessions WHERE id = ?", id).
		Scan(&sess.ID, &sess.UserID, &sess.Method, &created, &seen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("session: %w", ErrNotFound)
	}
	if err != nil {
		return Session{}, err
	}
	sess.CreatedAt, sess.SeenAt, sess.ExpiresAt = parseTime(created), parseTime(seen), parseTime(expires)
	return sess, nil
}

// TouchSession records that a session was used, and when it expires now.
func (s *Store) TouchSession(ctx context.Context, id string, seen, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, "UPDATE sessions SET seen_at = ?, expires_at = ? WHERE id = ?",
		formatTime(seen), formatTime(expires), id)
	return err
}

// DeleteSession removes a session, if it exists.
func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id)
	return err
}

// DeleteSessions removes the sessions of a user, except the one with the
// ID except, if not empty.
func (s *Store) DeleteSessions(ctx context.Context, userID, except string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ? AND id != ?", userID, except)
	return err
}

// DeleteExpiredSessions removes the sessions that expired before now.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", formatTime(now))
	return err
}

// Setting returns the value of a setting, and whether it is set.
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetSetting changes the value of a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
		key, value)
	return err
}
