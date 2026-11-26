package store_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kiritow/simple-proxy/internal/store"
)

func TestUsers(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sites.db"))
	ctx := t.Context()
	if err := s.ReplaceSites(ctx, []store.Site{site("s1", `{"a":1}`), site("s2", `{"b":2}`)}); err != nil {
		t.Fatal(err)
	}

	alice := store.User{ID: store.NewID(), Username: "Alice", Role: store.SiteOperator, Sites: []string{"s1", "s2"}, PasswordHash: "h"}
	if err := s.AddUser(ctx, alice); err != nil {
		t.Fatal(err)
	}
	dup := store.User{ID: store.NewID(), Username: "alice"}
	if err := s.AddUser(ctx, dup); !errors.Is(err, store.ErrExists) {
		t.Errorf("adding a username differing in case: %v, want ErrExists", err)
	}
	bad := store.User{ID: store.NewID(), Username: "bob", Sites: []string{"nope"}}
	if err := s.AddUser(ctx, bad); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("adding a user with a missing site: %v, want ErrNotFound", err)
	}
	if _, err := s.UserByName(ctx, "bob"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the user with a missing site was stored: %v", err)
	}

	got, err := s.UserByName(ctx, "ALICE")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != alice.ID || got.Role != store.SiteOperator || !slices.Equal(got.Sites, []string{"s1", "s2"}) || got.CreatedAt.IsZero() || !got.LastLoginAt.IsZero() {
		t.Errorf("UserByName = %+v", got)
	}

	oidc := store.User{ID: store.NewID(), Username: "carol", OIDCIssuer: "https://id", OIDCSubject: "123", Email: "c@example.com", EmailVerified: true}
	if err := s.AddUser(ctx, oidc); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUser(ctx, store.User{ID: store.NewID(), Username: "carol2", OIDCIssuer: "https://id", OIDCSubject: "123"}); !errors.Is(err, store.ErrExists) {
		t.Errorf("adding an OIDC identity twice: %v, want ErrExists", err)
	}
	if got, err := s.UserByOIDC(ctx, "https://id", "123"); err != nil || got.ID != oidc.ID || !got.EmailVerified {
		t.Errorf("UserByOIDC = %+v, %v", got, err)
	}
	if _, err := s.UserByOIDC(ctx, "https://other", "123"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UserByOIDC of another issuer: %v, want ErrNotFound", err)
	}

	// Deleting a site removes it from the users.
	if err := s.ReplaceSites(ctx, []store.Site{site("s2", `{"b":2}`)}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.User(ctx, alice.ID); !slices.Equal(got.Sites, []string{"s2"}) {
		t.Errorf("Sites after deleting s1 = %q", got.Sites)
	}

	got.Role, got.Sites, got.Disabled = store.Admin, nil, true
	if err := s.UpdateUser(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.User(ctx, alice.ID); got.Role != store.Admin || len(got.Sites) != 0 || !got.Disabled {
		t.Errorf("updated user = %+v", got)
	}
	got.Username = "CAROL"
	if err := s.UpdateUser(ctx, got); !errors.Is(err, store.ErrExists) {
		t.Errorf("renaming to a taken username: %v, want ErrExists", err)
	}

	users, err := s.Users(ctx)
	if err != nil || len(users) != 2 || users[0].Username != "Alice" || users[1].Username != "carol" {
		t.Errorf("Users = %+v, %v", users, err)
	}
	if err := s.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, alice.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting a deleted user: %v, want ErrNotFound", err)
	}
}

func TestSessions(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sites.db"))
	ctx := t.Context()
	u := store.User{ID: store.NewID(), Username: "u"}
	if err := s.AddUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, sess := range []store.Session{
		{ID: "a", UserID: u.ID, Method: "password", CreatedAt: now, SeenAt: now, ExpiresAt: now.Add(time.Hour)},
		{ID: "b", UserID: u.ID, Method: "oidc", CreatedAt: now, SeenAt: now, ExpiresAt: now.Add(time.Hour)},
		{ID: "old", UserID: u.ID, Method: "password", CreatedAt: now, SeenAt: now, ExpiresAt: now.Add(-time.Hour)},
	} {
		if err := s.AddSession(ctx, sess); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.Session(ctx, "b"); err != nil || got.UserID != u.ID || got.Method != "oidc" || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("Session = %+v, %v", got, err)
	}
	if err := s.TouchSession(ctx, "b", now.Add(time.Minute), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Session(ctx, "b"); !got.ExpiresAt.Equal(now.Add(2*time.Hour)) || !got.SeenAt.Equal(now.Add(time.Minute)) {
		t.Errorf("touched session = %+v", got)
	}
	if err := s.DeleteExpiredSessions(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, "old"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expired session: %v, want ErrNotFound", err)
	}
	if err := s.DeleteSessions(ctx, u.ID, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, "b"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("session b after DeleteSessions: %v, want ErrNotFound", err)
	}
	// Deleting the user ends their sessions.
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, "a"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("session of a deleted user: %v, want ErrNotFound", err)
	}
}

func TestSettings(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "sites.db"))
	ctx := t.Context()
	if _, ok, err := s.Setting(ctx, "k"); ok || err != nil {
		t.Errorf("unset setting: %v, %v", ok, err)
	}
	for _, v := range []string{"one", "two"} {
		if err := s.SetSetting(ctx, "k", v); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := s.Setting(ctx, "k"); got != v || !ok || err != nil {
			t.Errorf("Setting = %q, %v, %v; want %q", got, ok, err, v)
		}
	}
}
