package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/store"
)

// TokenPrefix starts the secrets of API tokens, to tell them from other
// secrets, e.g. in a scan for leaked ones.
const TokenPrefix = "sp_"

// ErrTokenNotFound reports that a token does not exist, or is not the
// user's.
var ErrTokenNotFound = errors.New("token not found")

// CreateToken creates an API token for u, which expires after the given
// number of days, or never if 0. It returns the token and its secret,
// which is not stored, so only the caller ever sees it.
func (s *Service) CreateToken(ctx context.Context, u store.User, name string, days int) (store.Token, string, error) {
	name = strings.TrimSpace(name)
	var errs config.FieldErrors
	switch {
	case name == "":
		errs = append(errs, problem("name", Required, ""))
	case utf8.RuneCountInString(name) > 64:
		errs = append(errs, problem("name", TooLong, ""))
	}
	if days < 0 || days > 3650 {
		errs = append(errs, problem("expires_in_days", InvalidDays, strconv.Itoa(days)))
	}
	if len(errs) > 0 {
		return store.Token{}, "", errs
	}
	secret := TokenPrefix + rand.Text()
	now := s.now()
	t := store.Token{
		ID:        store.NewID(),
		Hash:      hashToken(secret),
		UserID:    u.ID,
		Name:      name,
		Prefix:    secret[:len(TokenPrefix)+4],
		CreatedAt: now,
	}
	if days > 0 {
		t.ExpiresAt = now.Add(time.Duration(days) * 24 * time.Hour)
	}
	if err := s.store.AddToken(ctx, t); err != nil {
		return store.Token{}, "", err
	}
	s.log.InfoContext(ctx, "created an API token", "user", u.Username, "token", t.Name, "prefix", t.Prefix)
	return t, secret, nil
}

// Tokens returns the tokens of u, newest first.
func (s *Service) Tokens(ctx context.Context, u store.User) ([]store.Token, error) {
	return s.store.Tokens(ctx, u.ID)
}

// DeleteToken revokes a token of u. It fails with ErrTokenNotFound if u
// has no such token.
func (s *Service) DeleteToken(ctx context.Context, u store.User, id string) error {
	err := s.store.DeleteToken(ctx, u.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrTokenNotFound
	}
	if err == nil {
		s.log.InfoContext(ctx, "revoked an API token", "user", u.Username, "token", id)
	}
	return err
}

// AuthenticateToken returns the user of an API token's secret. It fails
// with ErrUnauthenticated if there is no such token, it expired, or its user
// is disabled.
func (s *Service) AuthenticateToken(ctx context.Context, secret string) (store.User, error) {
	if !strings.HasPrefix(secret, TokenPrefix) {
		return store.User{}, ErrUnauthenticated
	}
	t, err := s.store.TokenByHash(ctx, hashToken(secret))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.User{}, ErrUnauthenticated
	case err != nil:
		return store.User{}, err
	}
	now := s.now()
	if !t.ExpiresAt.IsZero() && now.After(t.ExpiresAt) {
		return store.User{}, ErrUnauthenticated
	}
	u, err := s.store.User(ctx, t.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.User{}, ErrUnauthenticated
	case err != nil:
		return store.User{}, err
	case u.Disabled:
		return store.User{}, ErrUnauthenticated
	}
	if now.Sub(t.LastUsedAt) >= touchEvery {
		if err := s.store.TouchToken(ctx, t.ID, now); err != nil {
			return store.User{}, err
		}
	}
	return u, nil
}
