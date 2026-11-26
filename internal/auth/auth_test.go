package auth_test

import (
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiritow/simple-proxy/internal/auth"
	"github.com/kiritow/simple-proxy/internal/store"
)

func TestPasswords(t *testing.T) {
	h := auth.HashPassword("correct horse")
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") || !auth.IsHash(h) {
		t.Errorf("HashPassword = %q", h)
	}
	if h == auth.HashPassword("correct horse") {
		t.Error("hashes are not salted")
	}
	if !auth.CheckPassword(h, "correct horse") || auth.CheckPassword(h, "correct horsE") || auth.CheckPassword("", "") {
		t.Error("CheckPassword is wrong")
	}
	for _, s := range []string{"", "password", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", "$argon2id$v=19$m=0,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA"} {
		if auth.IsHash(s) {
			t.Errorf("IsHash(%q)", s)
		}
	}
}

func TestParseConfig(t *testing.T) {
	cfg, err := auth.ParseConfig([]byte(`
admins:
- username: admin
  password: change-me-now
- email: ops@example.com
oidc:
  issuer: https://id.example.com/realms/main
  client_id: simple-proxy
  redirect_url: https://proxy.example.com/api/auth/oidc/callback
  scopes: [profile]
`))
	if err != nil {
		t.Fatal(err)
	}
	if o := cfg.OIDC; o.Name != "id.example.com" || strings.Join(o.Scopes, " ") != "openid profile" || o.ConsoleURL() != "https://proxy.example.com/" {
		t.Errorf("OIDC = %+v, console %q", o, o.ConsoleURL())
	}
	if !cfg.PasswordEnabled() || len(cfg.Admins) != 2 {
		t.Errorf("config = %+v", cfg)
	}
	if cfg, err := auth.ParseConfig(nil); err != nil || !cfg.PasswordEnabled() {
		t.Errorf("empty config: %+v, %v", cfg, err)
	}

	for _, c := range []struct{ yaml, want string }{
		{"admins: [{}]", "set the username"},
		{"admins: [{username: a b}]", "not a valid username"},
		{"admins: [{username: a, password: short}]", "at least 8 characters"},
		{"admins: [{username: a}, {username: A}]", "listed twice"},
		{"admins: [{username: a, email: a@example.com}]", "list them separately"},
		{"admins: [{email: a@example.com}]", "there is no oidc provider"},
		{"password_login: false", "no oidc provider"},
		{"oidc: {issuer: id.example.com, client_id: c, redirect_url: https://p/api/auth/oidc/callback}", "issuer"},
		{"oidc: {issuer: https://id, redirect_url: https://p/api/auth/oidc/callback}", "client_id is required"},
		{"oidc: {issuer: https://id, client_id: c}", "redirect_url is required"},
		{"oidc: {issuer: https://id, client_id: c, redirect_url: https://p/callback}", "must end with /api/auth/oidc/callback"},
		{"unknown: 1", "field unknown not found"},
	} {
		if _, err := auth.ParseConfig([]byte(c.yaml)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("ParseConfig(%q) = %v, want an error containing %q", c.yaml, err, c.want)
		}
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "sites.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// newService returns a Service with cfg, and what it logged.
func newService(t *testing.T, s *store.Store, cfg auth.Config) (*auth.Service, string) {
	t.Helper()
	var log bytes.Buffer
	svc, err := auth.New(t.Context(), auth.Options{Store: s, Config: cfg, Logger: slog.New(slog.NewTextHandler(&log, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return svc, log.String()
}

var loggedPassword = regexp.MustCompile(`password=(\w+)`)

func TestSuperAdmins(t *testing.T) {
	s := openStore(t)
	ctx := t.Context()

	// Without a password, the account gets a random one, which is logged.
	svc, log := newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root"}}})
	m := loggedPassword.FindStringSubmatch(log)
	if m == nil {
		t.Fatalf("no password logged: %s", log)
	}
	root, err := svc.Login(ctx, "root", m[1], "test")
	if err != nil || root.Role != store.Admin || !root.Managed || svc.PasswordManaged(root) {
		t.Fatalf("Login = %+v, %v", root, err)
	}
	// The password stays when starting again.
	if _, log = newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root"}}}); loggedPassword.MatchString(log) {
		t.Errorf("a password was logged again: %s", log)
	}
	svc, _ = newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root"}}})
	if _, err := svc.Login(ctx, "root", m[1], "test"); err != nil {
		t.Errorf("the password changed: %v", err)
	}

	// A password in the configuration replaces it, also as a hash.
	svc, _ = newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root", Password: "configured"}}})
	if u, err := svc.Login(ctx, "root", "configured", "test"); err != nil || !svc.PasswordManaged(u) {
		t.Errorf("configured password: %+v, %v", u, err)
	}
	svc, _ = newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root", Password: auth.HashPassword("hashed-pw")}}})
	if _, err := svc.Login(ctx, "root", "hashed-pw", "test"); err != nil {
		t.Errorf("configured hash: %v", err)
	}

	// Existing accounts become super administrators, and are enabled. Who
	// signed up with the username may not be who is meant, so the account
	// gets a new password, which is logged, and its sessions end.
	alice := store.User{ID: store.NewID(), Username: "alice", Disabled: true, PasswordHash: auth.HashPassword("alice-password")}
	if err := s.AddUser(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSession(ctx, store.Session{ID: "squatter", UserID: alice.ID, Method: auth.MethodPassword, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	svc, log = newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root"}, {Username: "ALICE"}}})
	m = loggedPassword.FindStringSubmatch(log)
	if m == nil || !strings.Contains(log, "user=alice") {
		t.Fatalf("no new password for alice logged: %s", log)
	}
	if _, err := svc.Login(ctx, "alice", "alice-password", "test"); err == nil {
		t.Error("alice kept her password")
	}
	if u, err := svc.Login(ctx, "alice", m[1], "test"); err != nil || u.Role != store.Admin || !u.Managed {
		t.Errorf("alice = %+v, %v", u, err)
	}
	if _, err := s.Session(ctx, "squatter"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("alice's session lasts: %v", err)
	}

	// Super administrators keep their password and email, unless they
	// change them themselves.
	rootUser, _ := s.UserByName(ctx, "root")
	aliceUser, _ := s.UserByName(ctx, "alice")
	password, email := "takeover-password", "evil@example.com"
	for _, ch := range []auth.UserChange{{Password: &password}, {Email: &email}} {
		if _, err := svc.UpdateUser(ctx, aliceUser, "", rootUser.ID, ch); !errors.Is(err, auth.ErrManagedUser) {
			t.Errorf("changing another super administrator with %+v: %v, want ErrManagedUser", ch, err)
		}
	}
	if _, err := svc.UpdateUser(ctx, rootUser, "", rootUser.ID, auth.UserChange{Email: &email}); err != nil {
		t.Errorf("a super administrator changing their own email: %v", err)
	}

	// Those no longer listed lose their role.
	newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root"}}})
	if u, err := s.UserByName(ctx, "alice"); err != nil || u.Role != store.NoRole || u.Managed {
		t.Errorf("alice after removing her = %+v, %v", u, err)
	}
}

// Super administrators listed by username sign in with a password, so the
// username of an account of the OIDC provider, which it may choose, is
// refused.
func TestSuperAdminOfOIDC(t *testing.T) {
	s := openStore(t)
	ctx := t.Context()
	if err := s.AddUser(ctx, store.User{ID: store.NewID(), Username: "carol", OIDCIssuer: "https://id.example.com", OIDCSubject: "c"}); err != nil {
		t.Fatal(err)
	}
	cfg := auth.Config{
		Admins: []auth.AdminConfig{{Username: "carol", Password: "carol-password"}},
		OIDC:   &auth.OIDCConfig{Issuer: "https://id.example.com", ClientID: "c", RedirectURL: "https://p/" + auth.CallbackPath},
	}
	_, err := auth.New(ctx, auth.Options{Store: s, Config: cfg})
	if err == nil || !strings.Contains(err.Error(), "list it by email or subject") {
		t.Errorf("New = %v, want an error", err)
	}
}

// Concurrent attempts to sign in do not get past the limit.
func TestConcurrentLogins(t *testing.T) {
	s := openStore(t)
	svc, _ := newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root", Password: "root-password"}}})
	var wg sync.WaitGroup
	var mu sync.Mutex
	limited := 0
	for range 40 {
		wg.Go(func() {
			_, err := svc.Login(t.Context(), "root", "wrong-password", "client")
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, auth.ErrTooManyAttempts) {
				limited++
			}
		})
	}
	wg.Wait()
	if limited != 30 {
		t.Errorf("%d of 40 concurrent attempts were limited, want 30", limited)
	}
}

// Every attempt to sign up counts, so that usernames cannot be probed.
func TestSignUpAttempts(t *testing.T) {
	s := openStore(t)
	svc, _ := newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root", Password: "root-password"}}})
	for range 20 {
		if _, err := svc.Register(t.Context(), "root", "some-password", "", "client"); errors.Is(err, auth.ErrTooManyAttempts) {
			t.Fatal("limited too early")
		}
	}
	if _, err := svc.Register(t.Context(), "fresh", "some-password", "", "client"); !errors.Is(err, auth.ErrTooManyAttempts) {
		t.Errorf("Register after 20 attempts: %v, want ErrTooManyAttempts", err)
	}
	if _, err := svc.Register(t.Context(), "fresh", "some-password", "", "other client"); err != nil {
		t.Errorf("Register from another client: %v", err)
	}
}

func TestFirstAdmin(t *testing.T) {
	s := openStore(t)
	ctx := t.Context()
	// Without super administrators or administrators, one is created.
	if err := s.AddUser(ctx, store.User{ID: store.NewID(), Username: "admin"}); err != nil {
		t.Fatal(err)
	}
	_, log := newService(t, s, auth.Config{})
	if !strings.Contains(log, "user=admin-2") || !loggedPassword.MatchString(log) {
		t.Fatalf("no administrator created: %s", log)
	}
	u, err := s.UserByName(ctx, "admin-2")
	if err != nil || u.Role != store.Admin || u.Managed {
		t.Fatalf("admin-2 = %+v, %v", u, err)
	}
	// Once there is one, no other is.
	if _, log = newService(t, s, auth.Config{}); strings.Contains(log, "password=") {
		t.Errorf("another administrator was created: %s", log)
	}
}

// The history of sites is for super administrators, or for all
// administrators if the configuration lists none.
func TestHistoryPermission(t *testing.T) {
	s := openStore(t)
	admin := store.User{Username: "admin", Role: store.Admin}
	super := store.User{Username: "root", Role: store.Admin, Managed: true}
	platform := store.User{Username: "p", Role: store.PlatformOperator, Managed: true}

	svc, _ := newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root", Password: "root-password"}}})
	if !svc.Permissions(super).History || svc.Permissions(admin).History || svc.Permissions(platform).History {
		t.Error("with super administrators, only they may see the history")
	}
	if p := svc.Permissions(super); !p.Sites || !p.AllSites || !p.Certificates || !p.Users {
		t.Errorf("super administrator permissions = %+v", p)
	}
	svc, _ = newService(t, s, auth.Config{})
	if !svc.Permissions(admin).History || svc.Permissions(platform).History {
		t.Error("without super administrators, administrators may see the history")
	}
}

func TestLastAdmin(t *testing.T) {
	s := openStore(t)
	ctx := t.Context()
	svc, log := newService(t, s, auth.Config{})
	first, err := s.UserByName(ctx, "admin")
	if err != nil {
		t.Fatal(err, log)
	}
	second, err := svc.CreateUser(ctx, first, auth.NewUser{Username: "second", Password: "second-password", Role: store.Admin})
	if err != nil {
		t.Fatal(err)
	}
	// One administrator may demote the other, but not the last.
	role := store.PlatformOperator
	if _, err := svc.UpdateUser(ctx, second, "", first.ID, auth.UserChange{Role: &role}); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteUser(ctx, first, second.ID); !errors.Is(err, auth.ErrLastAdmin) {
		t.Errorf("deleting the last administrator: %v, want ErrLastAdmin", err)
	}
	disabled := true
	if _, err := svc.UpdateUser(ctx, first, "", second.ID, auth.UserChange{Disabled: &disabled}); !errors.Is(err, auth.ErrLastAdmin) {
		t.Errorf("disabling the last administrator: %v, want ErrLastAdmin", err)
	}
}

func TestSessions(t *testing.T) {
	s := openStore(t)
	ctx := t.Context()
	svc, _ := newService(t, s, auth.Config{Admins: []auth.AdminConfig{{Username: "root", Password: "root-password"}}})
	u, err := svc.Login(ctx, "root", "root-password", "test")
	if err != nil {
		t.Fatal(err)
	}
	token, expires, err := svc.StartSession(ctx, u, auth.MethodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if got, sess, err := svc.Authenticate(ctx, token); err != nil || got.ID != u.ID || sess.Method != auth.MethodPassword || sess.ExpiresAt.After(expires) {
		t.Errorf("Authenticate = %+v, %+v, %v", got, sess, err)
	}
	if _, _, err := svc.Authenticate(ctx, token+"x"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("wrong token: %v", err)
	}
	if u, _ := s.User(ctx, u.ID); u.LastLoginAt.IsZero() {
		t.Error("the sign-in is not recorded")
	}
	// Sessions with a password end once passwords are no longer accepted.
	off := false
	oidc := &auth.OIDCConfig{Issuer: "https://id.invalid", ClientID: "c", RedirectURL: "https://p/" + auth.CallbackPath}
	svc2, _ := newService(t, s, auth.Config{PasswordLogin: &off, OIDC: oidc, Admins: []auth.AdminConfig{{Username: "root"}}})
	if _, _, err := svc2.Authenticate(ctx, token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("password session without password sign-in: %v", err)
	}
	// Sessions with OIDC end once the provider is removed.
	member := store.User{ID: store.NewID(), Username: "member", OIDCIssuer: oidc.Issuer, OIDCSubject: "m"}
	if err := s.AddUser(ctx, member); err != nil {
		t.Fatal(err)
	}
	withOIDC, _ := newService(t, s, auth.Config{OIDC: oidc, Admins: []auth.AdminConfig{{Username: "root"}}})
	oidcToken, _, err := withOIDC.StartSession(ctx, member, auth.MethodOIDC)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := withOIDC.Authenticate(ctx, oidcToken); err != nil {
		t.Errorf("OIDC session: %v", err)
	}
	if _, _, err := svc.Authenticate(ctx, oidcToken); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("OIDC session without a provider: %v", err)
	}
	if err := svc.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Authenticate(ctx, token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("after Logout: %v", err)
	}
}
