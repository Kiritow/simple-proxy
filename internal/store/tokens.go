package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Token is a personal API token, which scripts and MCP clients present to
// act as its user.
type Token struct {
	ID string // see NewID
	// Hash identifies the token by a hash of its secret, so that the
	// database does not hold the secrets themselves.
	Hash   string
	UserID string
	Name   string // what it is for
	// Prefix is the start of the secret, to recognize it.
	Prefix    string
	CreatedAt time.Time
	// LastUsedAt is zero if the token was never used, and ExpiresAt if it
	// never expires.
	LastUsedAt, ExpiresAt time.Time
}

const tokenColumns = "id, hash, user_id, name, prefix, created_at, last_used_at, expires_at"

func scanToken(row interface{ Scan(...any) error }) (Token, error) {
	var t Token
	var created, used, expires string
	if err := row.Scan(&t.ID, &t.Hash, &t.UserID, &t.Name, &t.Prefix, &created, &used, &expires); err != nil {
		return Token{}, err
	}
	t.CreatedAt, t.LastUsedAt, t.ExpiresAt = parseTime(created), parseTime(used), parseTime(expires)
	return t, nil
}

// AddToken stores a new token, which needs an ID from NewID and the hash of
// its secret.
func (s *Store) AddToken(ctx context.Context, t Token) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO api_tokens ("+tokenColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		t.ID, t.Hash, t.UserID, t.Name, t.Prefix, formatTime(t.CreatedAt), formatTime(t.LastUsedAt), formatTime(t.ExpiresAt))
	return err
}

// TokenByHash returns the token with the given hash, expired or not, or
// fails with ErrNotFound.
func (s *Store) TokenByHash(ctx context.Context, hash string) (Token, error) {
	t, err := scanToken(s.db.QueryRowContext(ctx, "SELECT "+tokenColumns+" FROM api_tokens WHERE hash = ?", hash))
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, fmt.Errorf("token: %w", ErrNotFound)
	}
	return t, err
}

// Tokens returns the tokens of a user, newest first.
func (s *Store) Tokens(ctx context.Context, userID string) ([]Token, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+tokenColumns+" FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC, rowid DESC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := []Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// DeleteToken removes the token with the given ID if it belongs to the user
// with the given ID, and fails with ErrNotFound otherwise.
func (s *Store) DeleteToken(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM api_tokens WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("token: %w", ErrNotFound)
	}
	return nil
}

// TouchToken records when a token was used.
func (s *Store) TouchToken(ctx context.Context, id string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, "UPDATE api_tokens SET last_used_at = ? WHERE id = ?", formatTime(at), id)
	return err
}
