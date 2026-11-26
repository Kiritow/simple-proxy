package auth

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/kiritow/simple-proxy/internal/store"
)

// oidcClient signs users in with an OIDC provider, using the authorization
// code flow with PKCE.
type oidcClient struct {
	cfg    OIDCConfig
	client *http.Client

	// mu guards the following, which are set once the provider has been
	// discovered; until then, every sign-in tries again.
	mu       sync.Mutex
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func newOIDCClient(cfg OIDCConfig, client *http.Client) *oidcClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &oidcClient{cfg: cfg, client: client}
}

// context returns ctx with the HTTP client to talk to the provider.
func (c *oidcClient) context(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, c.client)
}

// setup discovers the provider, unless it already has.
func (c *oidcClient) setup(ctx context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.oauth != nil {
		return c.oauth, c.verifier, nil
	}
	ctx, cancel := context.WithTimeout(c.context(ctx), 15*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, c.cfg.Issuer)
	if err != nil {
		return nil, nil, fmt.Errorf("discovering the OIDC provider %s: %w", c.cfg.Issuer, err)
	}
	c.oauth = &oauth2.Config{
		ClientID:     c.cfg.ClientID,
		ClientSecret: c.cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  c.cfg.RedirectURL,
		Scopes:       c.cfg.Scopes,
	}
	c.verifier = provider.Verifier(&oidc.Config{ClientID: c.cfg.ClientID})
	return c.oauth, c.verifier, nil
}

// OIDCConsoleURL returns the URL of the console as the OIDC provider knows
// it, where users go once they signed in with it, or "" if there is no
// provider.
func (s *Service) OIDCConsoleURL() string {
	if s.oidc == nil {
		return ""
	}
	return s.oidc.cfg.ConsoleURL()
}

// OIDCStart starts signing in with the OIDC provider. It returns the URL to
// send the browser to, and the state of the sign-in, which the browser
// keeps, e.g. in a cookie, for OIDCFinish.
func (s *Service) OIDCStart(ctx context.Context) (authURL, state string, err error) {
	if s.oidc == nil {
		return "", "", ErrOIDCUnavailable
	}
	conf, _, err := s.oidc.setup(ctx)
	if err != nil {
		return "", "", err
	}
	nonce, verifier := rand.Text(), oauth2.GenerateVerifier()
	st := rand.Text()
	authURL = conf.AuthCodeURL(st, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	return authURL, strings.Join([]string{st, nonce, verifier}, "."), nil
}

// OIDCFinish signs in the user that the OIDC provider sent back with code
// and stateParam, given the state that OIDCStart returned. Accounts
// signing in for the first time are created if signing up is open, or if
// they are super administrators. It fails with ErrDisabled or
// ErrRegistrationClosed, or another error describing what went wrong.
func (s *Service) OIDCFinish(ctx context.Context, state, stateParam, code string) (store.User, error) {
	if s.oidc == nil {
		return store.User{}, ErrOIDCUnavailable
	}
	parts := strings.Split(state, ".")
	if len(parts) != 3 || subtle.ConstantTimeCompare([]byte(parts[0]), []byte(stateParam)) != 1 {
		return store.User{}, errors.New("the state does not match; the sign-in expired or was started elsewhere")
	}
	nonce, verifier := parts[1], parts[2]
	conf, idVerifier, err := s.oidc.setup(ctx)
	if err != nil {
		return store.User{}, err
	}
	token, err := conf.Exchange(s.oidc.context(ctx), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return store.User{}, fmt.Errorf("exchanging the code for tokens: %w", err)
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return store.User{}, errors.New("the provider returned no ID token")
	}
	idToken, err := idVerifier.Verify(s.oidc.context(ctx), raw)
	if err != nil {
		return store.User{}, fmt.Errorf("verifying the ID token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		return store.User{}, errors.New("the ID token has the wrong nonce")
	}
	var claims struct {
		Email             string          `json:"email"`
		EmailVerified     json.RawMessage `json:"email_verified"`
		Name              string          `json:"name"`
		PreferredUsername string          `json:"preferred_username"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return store.User{}, fmt.Errorf("reading the claims of the ID token: %w", err)
	}
	// Some providers send "true" as a string.
	verified := strings.Trim(string(claims.EmailVerified), `"`) == "true"
	return s.oidcUser(ctx, identity{
		issuer:            idToken.Issuer,
		subject:           idToken.Subject,
		email:             claims.Email,
		emailVerified:     verified && claims.Email != "",
		name:              claims.Name,
		preferredUsername: claims.PreferredUsername,
	})
}

// identity is a user as the OIDC provider describes them.
type identity struct {
	issuer, subject         string
	email                   string
	emailVerified           bool
	name, preferredUsername string
}

// oidcUser returns the account of id, updated with what the provider says,
// or creates it.
func (s *Service) oidcUser(ctx context.Context, id identity) (store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	admin := false
	for _, a := range s.cfg.Admins {
		admin = admin || s.oidcAdmin(a, id.issuer, id.subject, id.email, id.emailVerified)
	}
	displayName := cmp.Or(id.name, id.preferredUsername, id.email)
	u, err := s.store.UserByOIDC(ctx, id.issuer, id.subject)
	switch {
	case err == nil:
		if u.Disabled && !admin {
			return store.User{}, ErrDisabled
		}
		u.DisplayName = cmp.Or(displayName, u.DisplayName)
		u.Email, u.EmailVerified = id.email, id.emailVerified
		switch {
		case admin:
			u.Role, u.Managed, u.Disabled = store.Admin, true, false
		case u.Managed:
			// The configuration no longer names them, e.g. as their email
			// changed.
			u.Role, u.Managed = store.NoRole, false
			s.log.WarnContext(ctx, "no longer a super administrator, so no longer has a role", "user", u.Username)
		}
		if err := s.store.UpdateUser(ctx, u); err != nil {
			return store.User{}, err
		}
	case errors.Is(err, store.ErrNotFound):
		if !admin {
			if open, err := s.Registration(ctx); err != nil {
				return store.User{}, err
			} else if !open {
				return store.User{}, ErrRegistrationClosed
			}
		}
		username, err := s.freeUsername(ctx, id.preferredUsername, id.email, id.name)
		if err != nil {
			return store.User{}, err
		}
		u = store.User{
			ID:            store.NewID(),
			Username:      username,
			DisplayName:   displayName,
			Email:         id.email,
			EmailVerified: id.emailVerified,
			OIDCIssuer:    id.issuer,
			OIDCSubject:   id.subject,
		}
		if admin {
			u.Role, u.Managed = store.Admin, true
		}
		if err := s.store.AddUser(ctx, u); err != nil {
			return store.User{}, err
		}
		s.log.InfoContext(ctx, "signed up with OIDC", "user", u.Username, "subject", id.subject, "admin", admin)
	default:
		return store.User{}, err
	}
	return s.store.User(ctx, u.ID)
}

// freeUsername returns a username that no one has yet, made from the first
// of candidates that has letters or digits.
func (s *Service) freeUsername(ctx context.Context, candidates ...string) (string, error) {
	base := "user"
	for _, c := range candidates {
		name := strings.Trim(strings.Map(func(r rune) rune {
			if usernameRune(r) {
				return r
			}
			if r == ' ' {
				return '-'
			}
			return -1
		}, c), "._-@")
		if name != "" {
			base = name
			break
		}
	}
	if len(base) > 56 {
		base = base[:56]
	}
	name := base
	for i := 2; ; i++ {
		_, err := s.store.UserByName(ctx, name)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return name, nil
		case err != nil:
			return "", err
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}
