package web_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// client makes requests as one user, with their session cookie, if any.
type client struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
	header http.Header // added to every request
}

func as(t *testing.T, h http.Handler, username, password string) *client {
	return &client{t: t, h: h, cookie: signIn(t, h, username, password)}
}

func (c *client) do(method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	for k, v := range c.header {
		r.Header[k] = v
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)
	return rec
}

// want makes a request, and checks its status and that the body contains
// each of contains. It returns the body.
func (c *client) want(method, path, body string, status int, contains ...string) string {
	c.t.Helper()
	rec := c.do(method, path, body)
	got, _ := io.ReadAll(rec.Result().Body)
	if rec.Code != status {
		c.t.Errorf("%s %s = %d %s, want %d", method, path, rec.Code, got, status)
	}
	for _, s := range contains {
		if !strings.Contains(string(got), s) {
			c.t.Errorf("%s %s = %d %s, want it to contain %s", method, path, rec.Code, got, s)
		}
	}
	return string(got)
}

// lacks checks that the body of a request does not contain s.
func (c *client) lacks(method, path, body, s string) {
	c.t.Helper()
	rec := c.do(method, path, body)
	if strings.Contains(rec.Body.String(), s) {
		c.t.Errorf("%s %s = %d %s, want it without %s", method, path, rec.Code, rec.Body, s)
	}
}

func idOf(t *testing.T, body string) string {
	t.Helper()
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil || v.ID == "" {
		t.Fatalf("no id in %s: %v", body, err)
	}
	return v.ID
}

func TestSignIn(t *testing.T) {
	h := newServer(t, options{})
	anon := &client{t: t, h: h}
	anon.want("GET", "/api/sites", "", 401, `"code":"unauthenticated"`)
	anon.want("GET", "/api/auth/me", "", 401, `"code":"unauthenticated"`)
	anon.want("GET", "/api/auth/config", "", 200, `{"password":true,"registration":true,"oidc":null}`)
	anon.want("GET", "/", "", 200, `<div id="root">`) // the UI, to sign in
	anon.want("POST", "/api/auth/login", `{"username": "admin", "password": "wrong"}`, 401, `"code":"invalid_credentials"`)
	anon.want("POST", "/api/auth/login", `{"username": "nobody", "password": "wrong"}`, 401, `"code":"invalid_credentials"`)

	admin := as(t, h, "ADMIN", adminPassword) // usernames ignore case
	if !admin.cookie.HttpOnly || admin.cookie.SameSite != http.SameSiteLaxMode || admin.cookie.Path != "/" {
		t.Errorf("session cookie = %+v", admin.cookie)
	}
	admin.want("GET", "/api/auth/me", "", 200,
		`"username":"admin"`, `"role":"admin"`, `"managed":true`, `"password_managed":true`, `"method":"password"`,
		`"permissions":{"sites":true,"all_sites":true,"certificates":true,"users":true}`)
	admin.want("GET", "/api/sites", "", 200, `{"sites":[]}`)

	// Signing out ends the session.
	admin.want("POST", "/api/auth/logout", "", 204)
	admin.want("GET", "/api/auth/me", "", 401)

	// Scripts sign in with HTTP Basic auth.
	basic := &client{t: t, h: h, header: http.Header{"Authorization": {"Basic " + basicAuth("admin", adminPassword)}}}
	basic.want("GET", "/api/sites", "", 200)
	wrong := &client{t: t, h: h, header: http.Header{"Authorization": {"Basic " + basicAuth("admin", "nope")}}}
	wrong.want("GET", "/api/sites", "", 401, `"code":"invalid_credentials"`)
}

func basicAuth(user, password string) string {
	r := httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth(user, password)
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
}

func TestTooManyAttempts(t *testing.T) {
	anon := &client{t: t, h: newServer(t, options{})}
	for range 10 {
		anon.want("POST", "/api/auth/login", `{"username": "admin", "password": "wrong"}`, 401)
	}
	anon.want("POST", "/api/auth/login", `{"username": "admin", "password": "wrong"}`, 429, `"code":"too_many_attempts"`)
	anon.want("POST", "/api/auth/login", `{"username": "admin", "password": "`+adminPassword+`"}`, 429)
}

func TestCrossOriginRequests(t *testing.T) {
	admin := as(t, newServer(t, options{}), "admin", adminPassword)
	admin.header = http.Header{"Sec-Fetch-Site": {"cross-site"}}
	admin.want("POST", "/api/sites", `{"config": {"server_name": "a.example.com", "default": {"port": 1}}}`, 403)
	admin.want("GET", "/api/sites", "", 200) // reading is harmless
	admin.header = http.Header{"Sec-Fetch-Site": {"same-origin"}}
	admin.want("POST", "/api/sites", `{"config": {"server_name": "a.example.com", "default": {"port": 1}}}`, 201)
}

func TestRoles(t *testing.T) {
	h := newServer(t, options{})
	admin := as(t, h, "admin", adminPassword)
	a := idOf(t, admin.want("POST", "/api/sites", `{"config": {"server_name": "a.example.com", "default": {"port": 1}}}`, 201))
	b := idOf(t, admin.want("POST", "/api/sites", `{"config": {"server_name": "b.example.com", "default": {"port": 2}}}`, 201))
	for _, u := range []string{
		`{"username": "site", "password": "site-password", "role": "site_operator", "sites": ["` + a + `"]}`,
		`{"username": "platform", "password": "platform-password", "role": "platform_operator"}`,
		`{"username": "nobody", "password": "nobody-password"}`,
	} {
		admin.want("POST", "/api/users", u, 201)
	}

	nobody := as(t, h, "nobody", "nobody-password")
	nobody.want("GET", "/api/auth/me", "", 200, `"role":""`, `"permissions":{"sites":false,"all_sites":false,"certificates":false,"users":false}`)
	nobody.want("GET", "/api/sites", "", 403, `"code":"forbidden"`)
	nobody.want("GET", "/api/status", "", 403)
	nobody.want("GET", "/api/certificates", "", 403)

	site := as(t, h, "site", "site-password")
	site.want("GET", "/api/auth/me", "", 200, `"sites":["`+a+`"]`, `"permissions":{"sites":true,"all_sites":false,"certificates":false,"users":false}`)
	site.want("GET", "/api/sites", "", 200, "a.example.com")
	site.lacks("GET", "/api/sites", "", "b.example.com")
	site.want("PUT", "/api/sites/"+a, `{"config": {"server_name": "a.example.com", "default": {"port": 10}}, "generation": 1}`, 200)
	site.want("PUT", "/api/sites/"+b, `{"config": {"server_name": "b.example.com", "default": {"port": 20}}, "generation": 1}`, 403)
	site.want("POST", "/api/sites/preview", `{"id": "`+a+`", "config": {"server_name": "a.example.com", "default": {"port": 11}}}`, 200, "127.0.0.1:11")
	site.lacks("POST", "/api/sites/preview", `{"id": "`+a+`", "config": {"server_name": "a.example.com", "default": {"port": 11}}}`, "b.example.com")
	site.want("POST", "/api/sites/preview", `{"id": "`+b+`", "config": {"server_name": "b.example.com", "default": {"port": 11}}}`, 403)
	site.want("POST", "/api/sites/preview", `{"config": {"server_name": "c.example.com", "default": {"port": 11}}}`, 403)
	site.want("POST", "/api/sites", `{"config": {"server_name": "c.example.com", "default": {"port": 1}}}`, 403)
	site.want("DELETE", "/api/sites/"+a, "", 403)
	site.want("PUT", "/api/sites", `{"configs": []}`, 403)
	site.want("GET", "/api/nginx.conf", "", 403)
	site.want("POST", "/api/apply", "", 403)
	site.want("GET", "/api/status", "", 200)
	site.want("GET", "/api/certificates", "", 200)
	site.want("GET", "/api/authorities", "", 200)
	site.want("POST", "/api/certificates/upload", `{"cert": "", "key": ""}`, 403)
	site.want("POST", "/api/authorities", `{"cert": "", "key": ""}`, 403)
	site.want("GET", "/api/users", "", 403)
	site.want("GET", "/api/settings", "", 403)
	site.want("POST", "/api/generate", "servers: []\n", 200)

	platform := as(t, h, "platform", "platform-password")
	platform.want("GET", "/api/sites", "", 200, "a.example.com", "b.example.com")
	c := idOf(t, platform.want("POST", "/api/sites", `{"config": {"server_name": "c.example.com", "default": {"port": 3}}}`, 201))
	platform.want("DELETE", "/api/sites/"+c, "", 204)
	platform.want("GET", "/api/nginx.conf", "", 200, "b.example.com")
	platform.want("POST", "/api/apply", "", 200)
	platform.want("POST", "/api/certificates/upload", `{"cert": "", "key": ""}`, 403)
	platform.want("DELETE", "/api/certificates/x", "", 403)
	platform.want("GET", "/api/users", "", 403)

	// Role changes take effect right away.
	users := admin.want("GET", "/api/users", "", 200)
	siteID := userID(t, users, "site")
	admin.want("PATCH", "/api/users/"+siteID, `{"sites": ["`+b+`"]}`, 200, `"sites":["`+b+`"]`)
	site.want("PUT", "/api/sites/"+a, `{"config": {"server_name": "a.example.com", "default": {"port": 12}}}`, 403)
	site.want("GET", "/api/sites", "", 200, "b.example.com")
	admin.want("PATCH", "/api/users/"+siteID, `{"role": ""}`, 200)
	site.want("GET", "/api/sites", "", 403)

	// Deleting a site removes it from the site operators.
	admin.want("DELETE", "/api/sites/"+b, "", 204)
	admin.want("GET", "/api/users", "", 200, `"username":"site","display_name":"","email":"","role":"","sites":[]`)
}

// userID returns the ID of the user with the given username in a list of
// users.
func userID(t *testing.T, list, username string) string {
	t.Helper()
	var v struct {
		Users []struct{ ID, Username string }
	}
	if err := json.Unmarshal([]byte(list), &v); err != nil {
		t.Fatal(err)
	}
	for _, u := range v.Users {
		if u.Username == username {
			return u.ID
		}
	}
	t.Fatalf("no user %s in %s", username, list)
	return ""
}

func TestUsers(t *testing.T) {
	h := newServer(t, options{})
	admin := as(t, h, "admin", adminPassword)
	admin.want("POST", "/api/users", `{"username": "a b", "password": "short"}`, 400,
		`"field":"username","message":"\"a b\" is not a valid username`, `"code":"invalid_username"`, `"field":"password"`, `"code":"password_too_short"`)
	admin.want("POST", "/api/users", `{"username": "x", "password": "long enough", "role": "root"}`, 400, `"code":"invalid_role"`)
	admin.want("POST", "/api/users", `{"username": "x", "password": "long enough", "role": "site_operator", "sites": ["nope"]}`, 400, `"code":"site_not_found"`)
	admin.want("POST", "/api/users", `{"username": "Admin", "password": "long enough"}`, 400, `"code":"username_in_use"`)
	bob := idOf(t, admin.want("POST", "/api/users", `{"username": "bob", "display_name": "Bob", "email": "bob@example.com", "password": "bob-password", "role": "admin"}`, 201,
		`"display_name":"Bob"`, `"email":"bob@example.com"`, `"role":"admin"`, `"managed":false`, `"password_managed":false`))
	adminID := userID(t, admin.want("GET", "/api/users", "", 200), "admin")

	// Super administrators are managed by the configuration file.
	admin.want("PATCH", "/api/users/"+adminID, `{"role": "site_operator"}`, 403, `"code":"managed_user"`)
	admin.want("PATCH", "/api/users/"+adminID, `{"disabled": true}`, 403, `"code":"managed_user"`)
	admin.want("PATCH", "/api/users/"+adminID, `{"password": "new-password"}`, 403, `"code":"password_managed"`)
	admin.want("PATCH", "/api/users/"+adminID, `{"display_name": "Root", "role": "admin", "disabled": false}`, 200, `"display_name":"Root"`)
	admin.want("DELETE", "/api/users/"+adminID, "", 403, `"code":"managed_user"`)
	admin.want("POST", "/api/auth/password", `{"current": "`+adminPassword+`", "password": "new-password"}`, 403, `"code":"password_managed"`)

	// Nobody changes their own role, or disables or deletes themselves.
	b := as(t, h, "bob", "bob-password")
	b.want("PATCH", "/api/users/"+bob, `{"role": "platform_operator"}`, 403, `"code":"own_account"`)
	b.want("PATCH", "/api/users/"+bob, `{"disabled": true}`, 403, `"code":"own_account"`)
	b.want("DELETE", "/api/users/"+bob, "", 403, `"code":"own_account"`)
	b.want("PATCH", "/api/users/"+bob, `{"role": "admin", "email": "b@example.com"}`, 200, `"email":"b@example.com"`)
	b.want("PATCH", "/api/users/nope", `{"role": "admin"}`, 404)
	b.want("PATCH", "/api/users/"+bob, `{"unknown": 1}`, 400)

	// Changing your password ends your other sessions.
	other := as(t, h, "bob", "bob-password")
	b.want("POST", "/api/auth/password", `{"current": "wrong", "password": "x"}`, 400, `"field":"current"`, `"code":"wrong_password"`, `"code":"password_too_short"`)
	b.want("POST", "/api/auth/password", `{"current": "bob-password", "password": "bob-password-2"}`, 204)
	b.want("GET", "/api/auth/me", "", 200)
	other.want("GET", "/api/auth/me", "", 401)
	as(t, h, "bob", "bob-password-2")

	// Disabling a user ends their sessions, and keeps them from signing in.
	admin.want("PATCH", "/api/users/"+bob, `{"disabled": true}`, 200, `"disabled":true`)
	b.want("GET", "/api/auth/me", "", 401)
	(&client{t: t, h: h}).want("POST", "/api/auth/login", `{"username": "bob", "password": "bob-password-2"}`, 403, `"code":"account_disabled"`)
	admin.want("PATCH", "/api/users/"+bob, `{"disabled": false, "password": "bob-password-3"}`, 200)
	as(t, h, "bob", "bob-password-3")
	admin.want("DELETE", "/api/users/"+bob, "", 204)
	admin.want("DELETE", "/api/users/"+bob, "", 404)
}

func TestRegistration(t *testing.T) {
	h := newServer(t, options{})
	admin := as(t, h, "admin", adminPassword)
	anon := &client{t: t, h: h}
	admin.want("GET", "/api/settings", "", 200, `{"registration":true}`)

	rec := anon.do("POST", "/api/auth/register", `{"username": "eve", "password": "eve-password", "display_name": "Eve"}`)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"role":""`) || !strings.Contains(rec.Body.String(), `"display_name":"Eve"`) {
		t.Fatalf("register = %d %s", rec.Code, rec.Body)
	}
	// Signing up signs in, without access.
	eve := &client{t: t, h: h}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "simple_proxy_session" {
			eve.cookie = c
		}
	}
	eve.want("GET", "/api/auth/me", "", 200, `"username":"eve"`)
	eve.want("GET", "/api/sites", "", 403)
	anon.want("POST", "/api/auth/register", `{"username": "EVE", "password": "eve-password"}`, 400, `"code":"username_in_use"`)
	anon.want("POST", "/api/auth/register", `{"username": "admin", "password": "eve-password"}`, 400, `"code":"username_in_use"`)

	admin.want("PUT", "/api/settings", `{"registration": false}`, 200, `{"registration":false}`)
	anon.want("GET", "/api/auth/config", "", 200, `"registration":false`)
	anon.want("POST", "/api/auth/register", `{"username": "mallory", "password": "mallory-password"}`, 403, `"code":"registration_closed"`)
	eve.want("PUT", "/api/settings", `{"registration": true}`, 403)
}

func TestPasswordLoginDisabled(t *testing.T) {
	off := false
	o := options{}
	o.auth.PasswordLogin = &off
	o.auth.OIDC = oidcConfig("http://idp.invalid")
	h := newServer(t, o)
	anon := &client{t: t, h: h}
	anon.want("GET", "/api/auth/config", "", 200, `{"password":false,"registration":true,"oidc":{"name":"idp.invalid"}}`)
	anon.want("POST", "/api/auth/login", `{"username": "admin", "password": "`+adminPassword+`"}`, 403, `"code":"password_login_disabled"`)
	anon.want("POST", "/api/auth/register", `{"username": "eve", "password": "eve-password"}`, 403, `"code":"password_login_disabled"`)
	basic := &client{t: t, h: h, header: http.Header{"Authorization": {"Basic " + basicAuth("admin", adminPassword)}}}
	basic.want("GET", "/api/sites", "", 403, `"code":"password_login_disabled"`)
}

func TestCertificateSitesAreFiltered(t *testing.T) {
	h := newServer(t, options{ca: true})
	admin := as(t, h, "admin", adminPassword)
	a := idOf(t, admin.want("POST", "/api/sites", `{"config": {"server_name": "a.example.com", "cert_name": "*.example.com", "default": {"port": 1}}}`, 201))
	admin.want("POST", "/api/sites", `{"config": {"server_name": "b.example.com", "cert_name": "*.example.com", "default": {"port": 2}}}`, 201)
	admin.want("POST", "/api/users", fmt.Sprintf(`{"username": "site", "password": "site-password", "role": "site_operator", "sites": [%q]}`, a), 201)
	admin.want("GET", "/api/certificates/*.example.com", "", 200, "a.example.com", "b.example.com")
	site := as(t, h, "site", "site-password")
	site.want("GET", "/api/certificates/*.example.com", "", 200, "a.example.com")
	site.lacks("GET", "/api/certificates/*.example.com", "", "b.example.com")
}

// Site operators change their sites, but not their raw configuration or
// extra directives, which reach beyond them.
func TestSiteOperatorDirectives(t *testing.T) {
	h := newServer(t, options{})
	admin := as(t, h, "admin", adminPassword)
	a := idOf(t, admin.want("POST", "/api/sites", `{"config": {"server_name": "a.example.com", "options": ["client_max_body_size 1m"], "default": {"port": 1}}}`, 201))
	r := idOf(t, admin.want("POST", "/api/sites", `{"config": {"raw": "server { listen 80; server_name r.example.com; }"}}`, 201))
	admin.want("POST", "/api/users", fmt.Sprintf(`{"username": "site", "password": "site-password", "role": "site_operator", "sites": [%q, %q]}`, a, r), 201)
	site := as(t, h, "site", "site-password")

	// Keeping the directives, in either form, is fine.
	site.want("PUT", "/api/sites/"+a, `{"config": {"server_name": "a.example.com", "options": [{"key": "client_max_body_size", "value": "1m"}], "default": {"port": 2}}}`, 200)
	site.want("PUT", "/api/sites/"+r, `{"config": {"raw": "server { listen 80; server_name r.example.com; }", "enable": false}}`, 200)
	for _, cfg := range []string{
		`{"server_name": "a.example.com", "options": ["client_max_body_size 1g"], "default": {"port": 2}}`,
		`{"server_name": "a.example.com", "default": {"port": 2}}`,
		`{"server_name": "a.example.com", "options": ["client_max_body_size 1m"], "default": {"port": 2, "options": ["access_log /etc/passwd"]}}`,
		`{"server_name": "a.example.com", "options": ["client_max_body_size 1m"], "default": {"port": 2}, "locations": [{"path": "/x", "options": ["root /"]}]}`,
		`{"raw": "server { listen 10.0.0.1:443 ssl; server_name a.example.com; }"}`,
	} {
		site.want("PUT", "/api/sites/"+a, `{"config": `+cfg+`}`, 403, `"code":"directives_restricted"`)
		site.want("POST", "/api/sites/preview", `{"id": "`+a+`", "config": `+cfg+`}`, 403, `"code":"directives_restricted"`)
	}
	site.want("PUT", "/api/sites/"+r, `{"config": {"raw": "server { listen 80; server_name r.example.com; access_log /tmp/x; }"}}`, 403, `"code":"directives_restricted"`)
	// Invalid changes get their problems, as usual.
	site.want("PUT", "/api/sites/"+a, `{"config": {"server_name": "a.example.com", "default": {"port": "x"}}}`, 400)
	// Platform operators may change them.
	admin.want("PUT", "/api/sites/"+a, `{"config": {"server_name": "a.example.com", "options": ["client_max_body_size 1g"], "default": {"port": 2}}}`, 200)
}

func TestTrustedProxies(t *testing.T) {
	login := func(c *client, from string) int {
		c.header = http.Header{"X-Forwarded-For": {"203.0.113.9, " + from}}
		return c.do("POST", "/api/auth/login", `{"username": "admin", "password": "wrong"}`).Code
	}
	// httptest requests come from 192.0.2.1.
	o := options{}
	o.auth.TrustedProxies = []string{"192.0.2.0/24"}
	trusting := &client{t: t, h: newServer(t, o)}
	for range 10 {
		login(trusting, "198.51.100.1")
	}
	if code := login(trusting, "198.51.100.1"); code != 429 {
		t.Errorf("the 11th attempt of a client behind a trusted proxy: %d, want 429", code)
	}
	if code := login(trusting, "198.51.100.2"); code != 401 {
		t.Errorf("another client behind the trusted proxy: %d, want 401", code)
	}
	// Without trusting the proxy, the header could be anything.
	untrusting := &client{t: t, h: newServer(t, options{})}
	for i := range 10 {
		login(untrusting, fmt.Sprintf("198.51.100.%d", i))
	}
	if code := login(untrusting, "198.51.100.99"); code != 429 {
		t.Errorf("X-Forwarded-For from an untrusted address was believed: %d", code)
	}
}

func TestSecureCookies(t *testing.T) {
	h := newServer(t, options{})
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username": "admin", "password": "`+adminPassword+`"}`))
	r.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "__Host-simple_proxy_session" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || cookie.Path != "/" || cookie.Domain != "" {
		t.Fatalf("cookies over HTTPS = %v", rec.Result().Cookies())
	}
	c := &client{t: t, h: h, cookie: cookie, header: http.Header{"X-Forwarded-Proto": {"https"}}}
	c.want("GET", "/api/auth/me", "", 200)
	// The cookie without the prefix, as another host of the domain could
	// set it, is not taken.
	plain := &client{t: t, h: h, cookie: &http.Cookie{Name: "simple_proxy_session", Value: cookie.Value}, header: http.Header{"X-Forwarded-Proto": {"https"}}}
	plain.want("GET", "/api/auth/me", "", 401)
}
