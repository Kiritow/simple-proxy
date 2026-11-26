package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/kiritow/simple-proxy/internal/auth"
	"github.com/kiritow/simple-proxy/internal/store"
)

// access is what a request needs to be served.
type access int

const (
	public      access = iota // nothing
	account                   // being signed in, whatever the role
	sitesAccess               // auth.Permissions.Sites
	allSites                  // auth.Permissions.AllSites
	certsAccess               // auth.Permissions.Certificates
	usersAccess               // auth.Permissions.Users
)

func (a access) allowedBy(p auth.Permissions) bool {
	switch a {
	case sitesAccess:
		return p.Sites
	case allSites:
		return p.AllSites
	case certsAccess:
		return p.Certificates
	case usersAccess:
		return p.Users
	}
	return true
}

// The names of the cookies, which get the prefix __Host- over HTTPS, so
// that other hosts of the domain cannot set them.
const (
	// sessionCookie holds the token of the session.
	sessionCookie = "simple_proxy_session"
	// oidcCookie holds the state of signing in with OIDC, until the
	// provider sends the browser back.
	oidcCookie = "simple_proxy_oidc"
)

// signedIn is the user of a request, and the session they signed in with,
// which has no ID for requests with a password (HTTP Basic auth).
type signedIn struct {
	user    store.User
	session store.Session
}

type signedInKey struct{}

// userOf returns the user of a request that guard let through.
func userOf(r *http.Request) signedIn {
	s, _ := r.Context().Value(signedInKey{}).(signedIn)
	return s
}

// guard serves requests with next if they have the access needed. Users
// are signed in by the session cookie, or, for scripts, by their username
// and password with HTTP Basic auth.
func (h *handler) guard(level access, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if level == public {
			next(w, r)
			return
		}
		s, err := h.authenticate(r)
		if err != nil {
			if errors.Is(err, auth.ErrUnauthenticated) {
				clearCookie(w, r, sessionCookie)
			}
			h.fail(w, r, err)
			return
		}
		if !level.allowedBy(auth.PermissionsOf(s.user.Role)) {
			h.fail(w, r, auth.ErrForbidden)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), signedInKey{}, s)))
	})
}

func (h *handler) authenticate(r *http.Request) (signedIn, error) {
	if username, password, ok := r.BasicAuth(); ok {
		u, err := h.auth.Login(r.Context(), username, password, h.clientOf(r))
		return signedIn{user: u}, err
	}
	c, err := r.Cookie(cookieName(r, sessionCookie))
	if err != nil || c.Value == "" {
		return signedIn{}, auth.ErrUnauthenticated
	}
	u, sess, err := h.auth.Authenticate(r.Context(), c.Value)
	return signedIn{user: u, session: sess}, err
}

// clientOf returns the IP address of the client of r, by which failed
// attempts to sign in are limited: the address r comes from, or for
// trusted reverse proxies, the last address in X-Forwarded-For that is not
// one of them.
func (h *handler) clientOf(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap()
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0 && h.auth.TrustedProxy(addr); i-- {
		next, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		addr = next.Unmap()
	}
	return addr.String()
}

// secure reports whether r came over HTTPS, maybe through a reverse proxy.
func secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func cookieName(r *http.Request, name string) string {
	if secure(r) {
		return "__Host-" + name
	}
	return name
}

func setCookie(w http.ResponseWriter, r *http.Request, name, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(r, name),
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		// Lax, so that the cookies come along when the OIDC provider sends
		// the browser back.
		SameSite: http.SameSiteLaxMode,
		Secure:   secure(r),
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request, name string) {
	if _, err := r.Cookie(cookieName(r, name)); err == nil {
		setCookie(w, r, name, "", time.Unix(0, 0))
	}
}

// startSession signs u in, and sets the session cookie.
func (h *handler) startSession(w http.ResponseWriter, r *http.Request, u store.User, method string) error {
	token, expires, err := h.auth.StartSession(r.Context(), u, method)
	if err != nil {
		return err
	}
	setCookie(w, r, sessionCookie, token, expires)
	return nil
}

// userResponse is a user, as the API describes them.
type userResponse struct {
	ID              string     `json:"id"`
	Username        string     `json:"username"`
	DisplayName     string     `json:"display_name"`
	Email           string     `json:"email"`
	Role            store.Role `json:"role"`
	Sites           []string   `json:"sites"`
	Disabled        bool       `json:"disabled"`
	Method          string     `json:"method"`
	Managed         bool       `json:"managed"`
	PasswordManaged bool       `json:"password_managed"`
	CreatedAt       time.Time  `json:"created_at"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
}

func (h *handler) describeUser(u store.User) userResponse {
	resp := userResponse{
		ID:              u.ID,
		Username:        u.Username,
		DisplayName:     u.DisplayName,
		Email:           u.Email,
		Role:            u.Role,
		Sites:           u.Sites,
		Disabled:        u.Disabled,
		Method:          auth.Method(u),
		Managed:         u.Managed,
		PasswordManaged: h.auth.PasswordManaged(u),
		CreatedAt:       u.CreatedAt,
	}
	if resp.Sites == nil {
		resp.Sites = []string{}
	}
	if !u.LastLoginAt.IsZero() {
		resp.LastLoginAt = &u.LastLoginAt
	}
	return resp
}

type meResponse struct {
	User        userResponse     `json:"user"`
	Permissions auth.Permissions `json:"permissions"`
}

func (h *handler) describeMe(u store.User) meResponse {
	return meResponse{User: h.describeUser(u), Permissions: auth.PermissionsOf(u.Role)}
}

func (h *handler) authConfig(w http.ResponseWriter, r *http.Request) {
	open, err := h.auth.Registration(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	type provider struct {
		Name string `json:"name"`
	}
	resp := struct {
		Password     bool      `json:"password"`
		Registration bool      `json:"registration"`
		OIDC         *provider `json:"oidc"`
	}{Password: h.auth.PasswordLogin(), Registration: open}
	if name := h.auth.OIDCName(); name != "" {
		resp.OIDC = &provider{Name: name}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.describeMe(userOf(r).user))
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := h.auth.Login(r.Context(), req.Username, req.Password, h.clientOf(r))
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrTooManyAttempts) {
			h.log.WarnContext(r.Context(), "failed to sign in", "user", req.Username, "client", h.clientOf(r), "err", err)
		}
		h.fail(w, r, err)
		return
	}
	if err := h.startSession(w, r, u, auth.MethodPassword); err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.describeMe(u))
}

func (h *handler) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := h.auth.Register(r.Context(), req.Username, req.Password, req.DisplayName, h.clientOf(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.startSession(w, r, u, auth.MethodPassword); err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.describeMe(u))
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName(r, sessionCookie)); err == nil && c.Value != "" {
		if err := h.auth.Logout(r.Context(), c.Value); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	clearCookie(w, r, sessionCookie)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s := userOf(r)
	if err := h.auth.ChangePassword(r.Context(), s.user, s.session.ID, req.Current, req.Password); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// oidcLogin sends the browser to the OIDC provider to sign in.
func (h *handler) oidcLogin(w http.ResponseWriter, r *http.Request) {
	authURL, state, err := h.auth.OIDCStart(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "signing in with OIDC", "err", err)
		// Relative to api/auth/oidc/login, wherever the console is.
		h.loginFailed(w, "../../../", err)
		return
	}
	setCookie(w, r, oidcCookie, state, time.Now().Add(10*time.Minute))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// oidcCallback signs in the user that the OIDC provider sent back, and
// sends them on to the console.
func (h *handler) oidcCallback(w http.ResponseWriter, r *http.Request) {
	console := h.auth.OIDCConsoleURL()
	if console == "" {
		console = "../../../"
	}
	var state string
	if c, err := r.Cookie(cookieName(r, oidcCookie)); err == nil {
		state = c.Value
	}
	clearCookie(w, r, oidcCookie)
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		err := errors.New(strings.TrimSpace(e + ": " + q.Get("error_description")))
		h.log.WarnContext(r.Context(), "the OIDC provider did not sign the user in", "err", err)
		h.loginFailed(w, console, err)
		return
	}
	u, err := h.auth.OIDCFinish(r.Context(), state, q.Get("state"), q.Get("code"))
	if err != nil {
		h.log.WarnContext(r.Context(), "signing in with OIDC failed", "err", err)
		h.loginFailed(w, console, err)
		return
	}
	if err := h.startSession(w, r, u, auth.MethodOIDC); err != nil {
		h.log.ErrorContext(r.Context(), "signing in with OIDC", "err", err)
		h.loginFailed(w, console, err)
		return
	}
	redirect(w, console)
}

// loginFailed sends the browser to the console, telling why signing in
// failed with ?login_error=code.
func (h *handler) loginFailed(w http.ResponseWriter, console string, err error) {
	code := auth.ErrOIDCFailed.Code
	var authErr *auth.Error
	if errors.As(err, &authErr) {
		code = authErr.Code
	}
	redirect(w, console+"?login_error="+url.QueryEscape(code))
}

// redirect redirects to location as is: http.Redirect would resolve
// relative locations against the path of the request, which misses the
// prefix of the console if a reverse proxy strips it.
func redirect(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}

func (h *handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.auth.Users(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resp := make([]userResponse, len(users))
	for i, u := range users {
		resp[i] = h.describeUser(u)
	}
	writeJSON(w, http.StatusOK, map[string][]userResponse{"users": resp})
}

func (h *handler) createUser(w http.ResponseWriter, r *http.Request) {
	var req auth.NewUser
	if !decodeJSON(w, r, &req) {
		return
	}
	u, err := h.auth.CreateUser(r.Context(), userOf(r).user, req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, h.describeUser(u))
}

func (h *handler) updateUser(w http.ResponseWriter, r *http.Request) {
	var req auth.UserChange
	if !decodeJSON(w, r, &req) {
		return
	}
	s := userOf(r)
	u, err := h.auth.UpdateUser(r.Context(), s.user, s.session.ID, r.PathValue("id"), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.describeUser(u))
}

func (h *handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.DeleteUser(r.Context(), userOf(r).user, r.PathValue("id")); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type settingsResponse struct {
	Registration bool `json:"registration"`
}

func (h *handler) settings(w http.ResponseWriter, r *http.Request) {
	open, err := h.auth.Registration(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsResponse{Registration: open})
}

func (h *handler) updateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsResponse
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.auth.SetRegistration(r.Context(), req.Registration); err != nil {
		h.fail(w, r, err)
		return
	}
	h.log.InfoContext(r.Context(), "changed the settings", "registration", req.Registration, "by", userOf(r).user.Username)
	h.settings(w, r)
}
