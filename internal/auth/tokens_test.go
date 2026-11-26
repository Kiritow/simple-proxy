package auth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kiritow/simple-proxy/internal/auth"
	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/store"
)

func TestTokens(t *testing.T) {
	s := openStore(t)
	ctx := t.Context()
	svc, _ := newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root", Password: "root-password"}}})
	root, err := s.UserByName(ctx, "root")
	if err != nil {
		t.Fatal(err)
	}

	tok, secret, err := svc.CreateToken(ctx, root, "  laptop  ", 30)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, auth.TokenPrefix) || tok.Name != "laptop" || !strings.HasPrefix(secret, tok.Prefix) || len(tok.Prefix) != 7 {
		t.Errorf("CreateToken = %+v, %q", tok, secret)
	}
	if days := tok.ExpiresAt.Sub(tok.CreatedAt).Hours() / 24; days != 30 {
		t.Errorf("token expires after %v days, want 30", days)
	}
	if strings.Contains(tok.Hash, secret) || tok.Hash == "" {
		t.Errorf("token hash = %q", tok.Hash)
	}
	u, err := svc.AuthenticateToken(ctx, secret)
	if err != nil || u.ID != root.ID {
		t.Fatalf("AuthenticateToken = %+v, %v", u, err)
	}
	if list, _ := svc.Tokens(ctx, root); len(list) != 1 || list[0].LastUsedAt.IsZero() {
		t.Errorf("Tokens after use = %+v", list)
	}
	for _, bad := range []string{"", secret + "x", strings.TrimPrefix(secret, auth.TokenPrefix), "sp_nope"} {
		if _, err := svc.AuthenticateToken(ctx, bad); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Errorf("AuthenticateToken(%q) = %v, want ErrUnauthenticated", bad, err)
		}
	}

	// Without expiry.
	_, forever, err := svc.CreateToken(ctx, root, "forever", 0)
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := svc.Tokens(ctx, root); len(list) != 2 || list[0].Name != "forever" || !list[0].ExpiresAt.IsZero() {
		t.Errorf("Tokens = %+v, want the newest first, without expiry", list)
	}

	// Problems.
	for _, c := range []struct {
		name  string
		days  int
		field string
	}{{"", 1, "name"}, {strings.Repeat("x", 65), 1, "name"}, {"ok", -1, "expires_in_days"}, {"ok", 3651, "expires_in_days"}} {
		_, _, err := svc.CreateToken(ctx, root, c.name, c.days)
		var fe config.FieldErrors
		if !errors.As(err, &fe) || fe[0].Field != c.field {
			t.Errorf("CreateToken(%q, %d) = %v, want a problem with %s", c.name, c.days, err, c.field)
		}
	}

	// Disabled users' tokens stop working, and deleted users' go.
	other, err := svc.CreateUser(ctx, root, auth.NewUser{Username: "other", Password: "other-password", Role: store.SiteOperator})
	if err != nil {
		t.Fatal(err)
	}
	_, otherSecret, _ := svc.CreateToken(ctx, other, "x", 0)
	disabled := true
	if _, err := svc.UpdateUser(ctx, root, "", other.ID, auth.UserChange{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateToken(ctx, otherSecret); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("token of a disabled user: %v", err)
	}
	// Nobody deletes others' tokens.
	otherTokens, _ := svc.Tokens(ctx, other)
	if err := svc.DeleteToken(ctx, root, otherTokens[0].ID); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Errorf("deleting another user's token: %v, want ErrTokenNotFound", err)
	}
	if err := svc.DeleteUser(ctx, root, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TokenByHash(ctx, otherTokens[0].Hash); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("token of a deleted user: %v", err)
	}

	// Revoked and expired tokens stop working.
	if err := svc.DeleteToken(ctx, root, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateToken(ctx, secret); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("revoked token: %v", err)
	}
	const oldSecret = "sp_EXPIREDEXPIREDEXPIREDEX"
	sum := sha256.Sum256([]byte(oldSecret))
	expired := store.Token{ID: store.NewID(), Hash: hex.EncodeToString(sum[:]), UserID: root.ID, Name: "old", Prefix: "sp_EXPI",
		CreatedAt: time.Now().Add(-48 * time.Hour), ExpiresAt: time.Now().Add(-time.Hour)}
	if err := s.AddToken(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateToken(ctx, oldSecret); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("expired token: %v, want ErrUnauthenticated", err)
	}
	if _, err := svc.AuthenticateToken(ctx, forever); err != nil {
		t.Errorf("a token that never expires: %v", err)
	}
}
