package web_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiritow/simple-proxy/internal/auth"
)

const console = "http://console.test/proxy/"

func oidcConfig(issuer string) *auth.OIDCConfig {
	return &auth.OIDCConfig{
		Issuer:       issuer,
		ClientID:     "simple-proxy",
		ClientSecret: "secret",
		RedirectURL:  console + auth.CallbackPath,
		Scopes:       []string{"openid", "profile", "email"},
	}
}

// provider is a fake OIDC provider. Users "authorize" with authorize, which
// returns the code the provider would send back with.
type provider struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu    sync.Mutex
	codes map[string]grant
}

// grant is what the provider knows about a code.
type grant struct {
	nonce, challenge string
	claims           map[string]any
}

func newProvider(t *testing.T) *provider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &provider{t: t, key: key, codes: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                p.srv.URL,
			"authorization_endpoint":                p.srv.URL + "/authorize",
			"token_endpoint":                        p.srv.URL + "/token",
			"jwks_uri":                              p.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("POST /token", p.token)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// authorize follows the redirect to the provider in authURL, and returns
// the URL the provider sends the browser back to, with claims for the user.
func (p *provider) authorize(authURL string, claims map[string]any) string {
	p.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil || !strings.HasPrefix(authURL, p.srv.URL+"/authorize?") {
		p.t.Fatalf("not sent to the provider: %q", authURL)
	}
	q := u.Query()
	if q.Get("client_id") != "simple-proxy" || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" ||
		q.Get("redirect_uri") != console+auth.CallbackPath || !strings.Contains(q.Get("scope"), "openid") {
		p.t.Fatalf("bad authorization request %q", authURL)
	}
	code := rand.Text()
	p.mu.Lock()
	p.codes[code] = grant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), claims: claims}
	p.mu.Unlock()
	return "/" + auth.CallbackPath + "?" + url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
}

func (p *provider) token(w http.ResponseWriter, r *http.Request) {
	id, secret, _ := r.BasicAuth()
	if id == "" {
		id, secret = r.FormValue("client_id"), r.FormValue("client_secret")
	}
	p.mu.Lock()
	g, ok := p.codes[r.FormValue("code")]
	delete(p.codes, r.FormValue("code"))
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
	if !ok || id != "simple-proxy" || secret != "secret" || b64(sum[:]) != g.challenge {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		return
	}
	claims := map[string]any{
		"iss": p.srv.URL, "aud": "simple-proxy", "nonce": g.nonce,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range g.claims {
		claims[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": p.sign(claims),
	})
}

func (p *provider) sign(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	input := b64(header) + "." + b64(payload)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		p.t.Fatal(err)
	}
	return input + "." + b64(sig)
}

// oidcSignIn signs in with the provider as the user with claims, and
// returns where the browser ends up and the session cookie, if any.
func oidcSignIn(t *testing.T, h http.Handler, p *provider, claims map[string]any) (string, *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/auth/oidc/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("oidc/login = %d %s", rec.Code, rec.Body)
	}
	var state *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "simple_proxy_oidc" {
			state = c
		}
	}
	if state == nil || !state.HttpOnly {
		t.Fatalf("oidc/login set no state cookie: %v", rec.Result().Cookies())
	}
	back := p.authorize(rec.Header().Get("Location"), claims)

	r := httptest.NewRequest("GET", back, nil)
	r.AddCookie(state)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusFound {
		t.Fatalf("oidc/callback = %d %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "simple_proxy_session" && c.Value != "" {
			return rec.Header().Get("Location"), c
		}
	}
	return rec.Header().Get("Location"), nil
}

func TestOIDC(t *testing.T) {
	p := newProvider(t)
	o := options{}
	o.auth.OIDC = oidcConfig(p.srv.URL)
	o.auth.OIDC.Name = "Example ID"
	o.auth.Admins = []auth.AdminConfig{{Email: "boss@example.com"}}
	h := newServer(t, o)
	(&client{t: t, h: h}).want("GET", "/api/auth/config", "", 200, `"oidc":{"name":"Example ID"}`)

	// A new account is created, without access.
	to, cookie := oidcSignIn(t, h, p, map[string]any{
		"sub": "u1", "email": "jane@example.com", "email_verified": true, "name": "Jane Doe", "preferred_username": "jane",
	})
	if to != console || cookie == nil {
		t.Fatalf("signed in to %q with %v, want %q with a session", to, cookie, console)
	}
	jane := &client{t: t, h: h, cookie: cookie}
	jane.want("GET", "/api/auth/me", "", 200, `"username":"jane"`, `"display_name":"Jane Doe"`, `"email":"jane@example.com"`,
		`"role":""`, `"method":"oidc"`, `"managed":false`)
	jane.want("GET", "/api/sites", "", 403)
	jane.want("POST", "/api/auth/password", `{"current": "", "password": "whatever-it-is"}`, 403, `"code":"not_password_account"`)

	// Signing in again finds the same account, updated, by subject.
	_, cookie = oidcSignIn(t, h, p, map[string]any{"sub": "u1", "name": "Jane Roe", "preferred_username": "other"})
	(&client{t: t, h: h, cookie: cookie}).want("GET", "/api/auth/me", "", 200, `"username":"jane"`, `"display_name":"Jane Roe"`)

	// Usernames are unique.
	_, cookie = oidcSignIn(t, h, p, map[string]any{"sub": "u2", "preferred_username": "Jane"})
	(&client{t: t, h: h, cookie: cookie}).want("GET", "/api/auth/me", "", 200, `"username":"Jane-2"`)

	// Super administrators are found by verified email.
	_, cookie = oidcSignIn(t, h, p, map[string]any{"sub": "u3", "email": "boss@example.com", "email_verified": false})
	(&client{t: t, h: h, cookie: cookie}).want("GET", "/api/auth/me", "", 200, `"role":""`)
	_, cookie = oidcSignIn(t, h, p, map[string]any{"sub": "u4", "email": "BOSS@example.com", "email_verified": true})
	boss := &client{t: t, h: h, cookie: cookie}
	boss.want("GET", "/api/auth/me", "", 200, `"role":"admin"`, `"managed":true`)
	// Only ASCII letters match regardless of case: "ſ" is not "s".
	_, cookie = oidcSignIn(t, h, p, map[string]any{"sub": "u6", "email": "boſs@example.com", "email_verified": true})
	(&client{t: t, h: h, cookie: cookie}).want("GET", "/api/auth/me", "", 200, `"role":""`, `"managed":false`)
	// A super administrator whose email changed no longer is one.
	_, cookie = oidcSignIn(t, h, p, map[string]any{"sub": "u4", "email": "former@example.com", "email_verified": true})
	(&client{t: t, h: h, cookie: cookie}).want("GET", "/api/auth/me", "", 200, `"role":""`, `"managed":false`)
	_, cookie = oidcSignIn(t, h, p, map[string]any{"sub": "u4", "email": "boss@example.com", "email_verified": true})
	boss = &client{t: t, h: h, cookie: cookie}
	boss.want("GET", "/api/auth/me", "", 200, `"role":"admin"`, `"managed":true`)

	// Without signing up, only existing accounts and super administrators
	// sign in.
	boss.want("PUT", "/api/settings", `{"registration": false}`, 200)
	to, cookie = oidcSignIn(t, h, p, map[string]any{"sub": "u5"})
	if to != console+"?login_error=registration_closed" || cookie != nil {
		t.Errorf("new account with signing up closed: sent to %q with %v", to, cookie)
	}
	if _, cookie = oidcSignIn(t, h, p, map[string]any{"sub": "u1"}); cookie == nil {
		t.Error("existing account cannot sign in with signing up closed")
	}

	// Disabled accounts cannot sign in.
	janeID := userID(t, boss.want("GET", "/api/users", "", 200), "jane")
	boss.want("PATCH", "/api/users/"+janeID, `{"disabled": true}`, 200)
	if to, cookie = oidcSignIn(t, h, p, map[string]any{"sub": "u1"}); to != console+"?login_error=account_disabled" || cookie != nil {
		t.Errorf("disabled account: sent to %q with %v", to, cookie)
	}
}

func TestOIDCFailures(t *testing.T) {
	p := newProvider(t)
	o := options{}
	o.auth.OIDC = oidcConfig(p.srv.URL)
	h := newServer(t, o)

	call := func(path string, cookies ...*http.Cookie) string {
		r := httptest.NewRequest("GET", path, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusFound || rec.Result().Cookies() != nil && hasSession(rec.Result().Cookies()) {
			t.Errorf("%s = %d %v, want a redirect without a session", path, rec.Code, rec.Result().Cookies())
		}
		return rec.Header().Get("Location")
	}
	failed := console + "?login_error=oidc_failed"
	if to := call("/" + auth.CallbackPath + "?error=access_denied"); to != failed {
		t.Errorf("refused by the provider: sent to %q", to)
	}
	if to := call("/" + auth.CallbackPath + "?code=x&state=y"); to != failed {
		t.Errorf("without the state cookie: sent to %q", to)
	}
	if to := call("/"+auth.CallbackPath+"?code=x&state=y", &http.Cookie{Name: "simple_proxy_oidc", Value: "z.n.v"}); to != failed {
		t.Errorf("with the wrong state: sent to %q", to)
	}
	if to := call("/"+auth.CallbackPath+"?code=unknown&state=y", &http.Cookie{Name: "simple_proxy_oidc", Value: "y.n.v"}); to != failed {
		t.Errorf("with an unknown code: sent to %q", to)
	}

	// Without a provider.
	h = newServer(t, options{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/auth/oidc/login", nil))
	if to := rec.Header().Get("Location"); rec.Code != http.StatusFound || to != "../../../?login_error=oidc_unavailable" {
		t.Errorf("without a provider: %d to %q", rec.Code, to)
	}
}

func hasSession(cookies []*http.Cookie) bool {
	for _, c := range cookies {
		if c.Name == "simple_proxy_session" && c.Value != "" {
			return true
		}
	}
	return false
}
