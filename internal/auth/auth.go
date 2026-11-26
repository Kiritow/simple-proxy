// Package auth signs users in to the console, with a password or with an
// OpenID Connect provider, keeps their sessions, and decides what they may
// do by their role.
//
// The super administrators are listed in the configuration file; New makes
// them administrators, and the console cannot change them. Accounts that
// sign up, with a password or by signing in with OIDC for the first time,
// have no role until an administrator gives them one.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/store"
)

// Session lifetimes: a session ends after SessionIdle without being used,
// and SessionMax after signing in in any case.
const (
	SessionIdle = 7 * 24 * time.Hour
	SessionMax  = 30 * 24 * time.Hour
	// touchEvery limits how often using a session is recorded.
	touchEvery = time.Hour
)

// How users signed in, as sessions record it.
const (
	MethodPassword = "password"
	MethodOIDC     = "oidc"
)

// Permissions are what a role allows.
type Permissions struct {
	// Sites allows seeing and changing sites: all of them with AllSites,
	// otherwise those assigned to the user.
	Sites bool `json:"sites"`
	// AllSites allows adding, deleting and importing sites, applying them
	// again, and seeing the whole nginx configuration.
	AllSites bool `json:"all_sites"`
	// Certificates allows requesting, uploading, signing, renaming and
	// deleting certificates and CAs; everyone with Sites may see them.
	Certificates bool `json:"certificates"`
	// Users allows managing the users and the settings.
	Users bool `json:"users"`
	// History allows seeing statistics of the versions of sites, and
	// clearing old ones: for super administrators only, see
	// Service.Permissions.
	History bool `json:"history"`
}

// Permissions returns what u may do: what their role allows, and History
// for super administrators, or for all administrators if the configuration
// lists no super administrators.
func (s *Service) Permissions(u store.User) Permissions {
	p := PermissionsOf(u.Role)
	p.History = u.Role == store.Admin && (u.Managed || len(s.cfg.Admins) == 0)
	return p
}

// PermissionsOf returns what role allows; Service.Permissions adds what
// depends on the user.
func PermissionsOf(role store.Role) Permissions {
	switch role {
	case store.Admin:
		return Permissions{Sites: true, AllSites: true, Certificates: true, Users: true}
	case store.PlatformOperator:
		return Permissions{Sites: true, AllSites: true}
	case store.SiteOperator:
		return Permissions{Sites: true}
	}
	return Permissions{}
}

// CanManageSite reports whether u may see and change the site with the
// given ID.
func CanManageSite(u store.User, id string) bool {
	p := PermissionsOf(u.Role)
	return p.AllSites || (p.Sites && slices.Contains(u.Sites, id))
}

// Method returns how u signs in: MethodOIDC for accounts of the OIDC
// provider, MethodPassword otherwise.
func Method(u store.User) string {
	if u.OIDCSubject != "" {
		return MethodOIDC
	}
	return MethodPassword
}

// Error is a refusal, with a code identifying it for clients.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

var (
	ErrUnauthenticated       = &Error{"unauthenticated", "sign in first"}
	ErrForbidden             = &Error{"forbidden", "your role does not allow this"}
	ErrInvalidCredentials    = &Error{"invalid_credentials", "wrong username or password"}
	ErrDisabled              = &Error{"account_disabled", "the account is disabled"}
	ErrPasswordLoginDisabled = &Error{"password_login_disabled", "signing in with a password is disabled"}
	ErrTooManyAttempts       = &Error{"too_many_attempts", "too many attempts; try again later"}
	ErrRegistrationClosed    = &Error{"registration_closed", "signing up is closed; ask an administrator for an account"}
	ErrPasswordManaged       = &Error{"password_managed", "the password is set in the configuration file"}
	ErrNotPasswordAccount    = &Error{"not_password_account", "the account signs in with OIDC and has no password"}
	ErrManagedUser           = &Error{"managed_user", "super administrators are managed in the configuration file"}
	ErrOwnAccount            = &Error{"own_account", "you cannot change your own role, or disable or delete your own account"}
	ErrLastAdmin             = &Error{"last_admin", "at least one enabled administrator must remain"}
	ErrOIDCUnavailable       = &Error{"oidc_unavailable", "signing in with OIDC is not configured"}
	ErrOIDCFailed            = &Error{"oidc_failed", "signing in with OIDC failed"}
	ErrDirectivesRestricted  = &Error{"directives_restricted", "only platform operators and administrators may change raw configuration and extra directives"}
	// ErrUserNotFound reports that a user does not exist.
	ErrUserNotFound = errors.New("user not found")
)

// ProblemCode identifies a problem with a field of a request, so that
// clients can describe it in their own words.
type ProblemCode string

const (
	InvalidUsername  ProblemCode = "invalid_username"
	UsernameInUse    ProblemCode = "username_in_use"
	PasswordTooShort ProblemCode = "password_too_short"
	WrongPassword    ProblemCode = "wrong_password"
	InvalidRole      ProblemCode = "invalid_role"
	SiteNotFound     ProblemCode = "site_not_found"
	TooLong          ProblemCode = "too_long"
)

var problemText = map[ProblemCode]string{
	InvalidUsername:  "%q is not a valid username: use 1–64 letters, digits, '.', '_', '-' or '@'",
	UsernameInUse:    "the username %q is taken",
	PasswordTooShort: fmt.Sprintf("needs at least %d characters", MinPasswordLength),
	WrongPassword:    "wrong password",
	InvalidRole:      "%q is not a role",
	SiteNotFound:     "there is no site %q",
	TooLong:          "is too long",
}

// Problem is a problem with a field of a request, reported as a
// config.FieldError.
type Problem struct {
	Code  ProblemCode
	Value string
}

func (p *Problem) Error() string {
	text := problemText[p.Code]
	if strings.Contains(text, "%") {
		return fmt.Sprintf(text, p.Value)
	}
	return text
}

func problem(field string, code ProblemCode, value string) *config.FieldError {
	return &config.FieldError{Field: field, Err: &Problem{Code: code, Value: value}}
}

// checkUsername checks a username: 1–64 letters, digits, '.', '_', '-' or
// '@'.
func checkUsername(name string) error {
	if name == "" || len(name) > 64 || strings.IndexFunc(name, func(r rune) bool { return !usernameRune(r) }) >= 0 {
		return &Problem{Code: InvalidUsername, Value: name}
	}
	return nil
}

func usernameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-@", r)
}

// Options configure a Service.
type Options struct {
	Store  *store.Store
	Config Config
	Logger *slog.Logger
	// HTTPClient talks to the OIDC provider; http.DefaultClient if nil.
	HTTPClient *http.Client
}

// Service signs users in and manages them. It is safe for concurrent use.
type Service struct {
	store *store.Store
	cfg   Config
	log   *slog.Logger
	oidc  *oidcClient // nil without a provider
	// logins limits failed attempts to sign in by client and username, and
	// clients by client alone, for those trying many usernames; signups
	// limits attempts to sign up by client.
	logins, clients, signups *limiter
	// trusted are the reverse proxies whose X-Forwarded-For is believed.
	trusted []netip.Prefix
	now     func() time.Time

	// mu serializes changes to users, so that the checks before them, e.g.
	// that an administrator remains, still hold when they are stored.
	mu sync.Mutex
}

// New returns a Service, after checking the configuration, and making its
// super administrators administrators. Super administrators who are no longer
// listed lose their role. If there are no super administrators and no
// administrator, New creates one with a random password, and logs it.
func New(ctx context.Context, opts Options) (*Service, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if err := opts.Config.check(); err != nil {
		return nil, err
	}
	s := &Service{
		store:   opts.Store,
		cfg:     opts.Config,
		log:     opts.Logger,
		logins:  newLimiter(10, 15*time.Minute),
		clients: newLimiter(100, 15*time.Minute),
		signups: newLimiter(20, time.Hour),
		trusted: opts.Config.trusted,
		now:     time.Now,
	}
	if o := opts.Config.OIDC; o != nil {
		s.oidc = newOIDCClient(*o, opts.HTTPClient)
	}
	if err := s.applyConfig(ctx); err != nil {
		return nil, fmt.Errorf("applying the administrators of the configuration: %w", err)
	}
	if err := s.store.DeleteExpiredSessions(ctx, s.now()); err != nil {
		return nil, err
	}
	return s, nil
}

// PasswordLogin reports whether users may sign in with a password.
func (s *Service) PasswordLogin() bool { return s.cfg.PasswordEnabled() }

// OIDCName returns the name of the OIDC provider, or "" if there is none.
func (s *Service) OIDCName() string {
	if s.oidc == nil {
		return ""
	}
	return s.oidc.cfg.Name
}

const registrationKey = "registration"

// Registration reports whether new accounts may sign up, which they may
// unless an administrator closed it.
func (s *Service) Registration(ctx context.Context) (bool, error) {
	value, ok, err := s.store.Setting(ctx, registrationKey)
	return !ok || value == "on", err
}

// SetRegistration opens or closes signing up.
func (s *Service) SetRegistration(ctx context.Context, open bool) error {
	value := "off"
	if open {
		value = "on"
	}
	return s.store.SetSetting(ctx, registrationKey, value)
}

// Login checks the password of a user, signing in from client, e.g. an IP
// address, which limits failed attempts. It fails with
// ErrInvalidCredentials, ErrDisabled or ErrTooManyAttempts.
func (s *Service) Login(ctx context.Context, username, password, client string) (store.User, error) {
	if !s.PasswordLogin() {
		return store.User{}, ErrPasswordLoginDisabled
	}
	// Attempts count until they succeed.
	key := client + "\x00" + asciiLower(username)
	if !s.logins.take(key) {
		return store.User{}, ErrTooManyAttempts
	}
	if !s.clients.take(client) {
		s.logins.give(key)
		return store.User{}, ErrTooManyAttempts
	}
	u, err := s.store.UserByName(ctx, username)
	switch {
	case errors.Is(err, store.ErrNotFound):
		CheckPassword(dummyHash, password) // takes as long as for a user
		return store.User{}, ErrInvalidCredentials
	case err != nil:
		return store.User{}, err
	}
	hash := u.PasswordHash
	if hash == "" {
		hash = dummyHash // e.g. for accounts of the OIDC provider
	}
	if !CheckPassword(hash, password) || u.PasswordHash == "" {
		return store.User{}, ErrInvalidCredentials
	}
	s.logins.reset(key)
	s.clients.give(client)
	if u.Disabled {
		return store.User{}, ErrDisabled
	}
	return u, nil
}

// Register adds an account signing in with a password, without a role.
func (s *Service) Register(ctx context.Context, username, password, displayName, client string) (store.User, error) {
	if !s.PasswordLogin() {
		return store.User{}, ErrPasswordLoginDisabled
	}
	if open, err := s.Registration(ctx); err != nil {
		return store.User{}, err
	} else if !open {
		return store.User{}, ErrRegistrationClosed
	}
	// Every attempt counts, so that usernames cannot be probed at will.
	if !s.signups.take(client) {
		return store.User{}, ErrTooManyAttempts
	}
	u := store.User{ID: store.NewID(), Username: username, DisplayName: strings.TrimSpace(displayName)}
	errs := checkProfile(u)
	if err := checkPassword("password", password); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return store.User{}, errs
	}
	u.PasswordHash = HashPassword(password)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.add(ctx, u); err != nil {
		return store.User{}, err
	}
	s.log.InfoContext(ctx, "signed up", "user", u.Username, "client", client)
	return s.store.User(ctx, u.ID)
}

// checkProfile checks the username, display name and email of u.
func checkProfile(u store.User) config.FieldErrors {
	var errs config.FieldErrors
	if err := checkUsername(u.Username); err != nil {
		errs = append(errs, &config.FieldError{Field: "username", Err: err})
	}
	if utf8.RuneCountInString(u.DisplayName) > 128 {
		errs = append(errs, problem("display_name", TooLong, ""))
	}
	if len(u.Email) > 254 {
		errs = append(errs, problem("email", TooLong, ""))
	}
	return errs
}

func checkPassword(field, password string) *config.FieldError {
	switch {
	case utf8.RuneCountInString(password) < MinPasswordLength:
		return problem(field, PasswordTooShort, "")
	case len(password) > maxPasswordLength:
		return problem(field, TooLong, "")
	}
	return nil
}

// add stores a new user, reporting a username in use for the field
// username.
func (s *Service) add(ctx context.Context, u store.User) error {
	err := s.store.AddUser(ctx, u)
	return s.userError(err, u)
}

func (s *Service) userError(err error, u store.User) error {
	switch {
	case errors.Is(err, store.ErrExists):
		return config.FieldErrors{problem("username", UsernameInUse, u.Username)}
	case errors.Is(err, store.ErrNotFound) && strings.HasPrefix(err.Error(), "site "):
		return config.FieldErrors{problem("sites", SiteNotFound, "")}
	}
	return err
}

// StartSession signs u in, with method. It returns the token identifying
// the session, and when it expires at the latest.
func (s *Service) StartSession(ctx context.Context, u store.User, method string) (string, time.Time, error) {
	token := rand.Text()
	now := s.now()
	sess := store.Session{
		ID:        hashToken(token),
		UserID:    u.ID,
		Method:    method,
		CreatedAt: now,
		SeenAt:    now,
		ExpiresAt: now.Add(SessionIdle),
	}
	if err := s.store.AddSession(ctx, sess); err != nil {
		return "", time.Time{}, err
	}
	if err := s.store.RecordLogin(ctx, u.ID, now); err != nil {
		return "", time.Time{}, err
	}
	s.log.InfoContext(ctx, "signed in", "user", u.Username, "method", method)
	return token, now.Add(SessionMax), nil
}

// hashToken returns the ID of the session with the given token.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Authenticate returns the user and the session of a session token. It
// fails with ErrUnauthenticated if the session expired or does not exist,
// or the user is disabled.
func (s *Service) Authenticate(ctx context.Context, token string) (store.User, store.Session, error) {
	sess, err := s.store.Session(ctx, hashToken(token))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.User{}, store.Session{}, ErrUnauthenticated
	case err != nil:
		return store.User{}, store.Session{}, err
	}
	now := s.now()
	if now.After(sess.ExpiresAt) || (sess.Method == MethodPassword && !s.PasswordLogin()) {
		return store.User{}, store.Session{}, errors.Join(ErrUnauthenticated, s.store.DeleteSession(ctx, sess.ID))
	}
	u, err := s.store.User(ctx, sess.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.User{}, store.Session{}, ErrUnauthenticated
	case err != nil:
		return store.User{}, store.Session{}, err
	case u.Disabled:
		return store.User{}, store.Session{}, ErrUnauthenticated
	case sess.Method == MethodOIDC && (s.oidc == nil || u.OIDCIssuer != s.oidc.cfg.Issuer):
		// The provider was removed or replaced, maybe for good reason.
		return store.User{}, store.Session{}, errors.Join(ErrUnauthenticated, s.store.DeleteSession(ctx, sess.ID))
	}
	if now.Sub(sess.SeenAt) >= touchEvery {
		expires := now.Add(SessionIdle)
		if limit := sess.CreatedAt.Add(SessionMax); expires.After(limit) {
			expires = limit
		}
		if err := s.store.TouchSession(ctx, sess.ID, now, expires); err != nil {
			return store.User{}, store.Session{}, err
		}
		sess.SeenAt, sess.ExpiresAt = now, expires
	}
	return u, sess, nil
}

// Logout ends the session with the given token.
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, hashToken(token))
}

// PasswordManaged reports whether the password of u is set in the
// configuration file, so that it cannot be changed in the console.
func (s *Service) PasswordManaged(u store.User) bool {
	if !u.Managed || u.OIDCSubject != "" {
		return false
	}
	for _, a := range s.cfg.Admins {
		if a.Password != "" && asciiLower(a.Username) == asciiLower(u.Username) {
			return true
		}
	}
	return false
}

// ChangePassword changes the password of u, who enters their current one,
// and ends their sessions other than the one with the ID keep.
func (s *Service) ChangePassword(ctx context.Context, u store.User, keep, current, password string) error {
	if err := s.passwordChangeable(u); err != nil {
		return err
	}
	if !s.logins.take(u.ID) {
		return ErrTooManyAttempts
	}
	var errs config.FieldErrors
	if !CheckPassword(u.PasswordHash, current) {
		errs = append(errs, problem("current", WrongPassword, ""))
	} else {
		s.logins.reset(u.ID)
	}
	if err := checkPassword("password", password); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errs
	}
	_, err := s.modify(ctx, u.ID, func(u *store.User) error {
		u.PasswordHash = HashPassword(password)
		return nil
	})
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "changed their password", "user", u.Username)
	return s.store.DeleteSessions(ctx, u.ID, keep)
}

func (s *Service) passwordChangeable(u store.User) error {
	switch {
	case s.PasswordManaged(u):
		return ErrPasswordManaged
	case Method(u) != MethodPassword:
		return ErrNotPasswordAccount
	}
	return nil
}

// modify changes the user with the given ID with change, and stores the
// result.
func (s *Service) modify(ctx context.Context, id string, change func(*store.User) error) (store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, err := s.user(ctx, id)
	if err != nil {
		return store.User{}, err
	}
	if err := change(&u); err != nil {
		return store.User{}, err
	}
	if err := s.store.UpdateUser(ctx, u); err != nil {
		return store.User{}, s.userError(err, u)
	}
	return s.store.User(ctx, id)
}

func (s *Service) user(ctx context.Context, id string) (store.User, error) {
	u, err := s.store.User(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, ErrUserNotFound
	}
	return u, err
}

// Users returns the users, by username.
func (s *Service) Users(ctx context.Context) ([]store.User, error) {
	return s.store.Users(ctx)
}

// NewUser is an account that an administrator adds.
type NewUser struct {
	Username    string     `json:"username"`
	DisplayName string     `json:"display_name"`
	Email       string     `json:"email"`
	Password    string     `json:"password"`
	Role        store.Role `json:"role"`
	Sites       []string   `json:"sites"`
}

// CreateUser adds an account signing in with a password, for actor, an
// administrator.
func (s *Service) CreateUser(ctx context.Context, actor store.User, req NewUser) (store.User, error) {
	u := store.User{
		ID:          store.NewID(),
		Username:    req.Username,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Email:       strings.TrimSpace(req.Email),
		Role:        req.Role,
		Sites:       req.Sites,
	}
	errs := checkProfile(u)
	if err := checkPassword("password", req.Password); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, s.checkAccess(ctx, u)...)
	if len(errs) > 0 {
		return store.User{}, errs
	}
	u.PasswordHash = HashPassword(req.Password)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.add(ctx, u); err != nil {
		return store.User{}, err
	}
	s.log.InfoContext(ctx, "added a user", "user", u.Username, "role", u.Role, "by", actor.Username)
	return s.store.User(ctx, u.ID)
}

// checkAccess checks the role and the sites of u.
func (s *Service) checkAccess(ctx context.Context, u store.User) config.FieldErrors {
	var errs config.FieldErrors
	if !u.Role.Valid() {
		errs = append(errs, problem("role", InvalidRole, string(u.Role)))
	}
	if len(u.Sites) > 0 {
		sites, err := s.store.Sites(ctx)
		if err != nil {
			return append(errs, &config.FieldError{Field: "sites", Err: err})
		}
		for _, id := range u.Sites {
			if !slices.ContainsFunc(sites, func(site store.Site) bool { return site.ID == id }) {
				errs = append(errs, problem("sites", SiteNotFound, id))
			}
		}
	}
	return errs
}

// UserChange changes a user; nil fields stay as they are.
type UserChange struct {
	DisplayName *string     `json:"display_name"`
	Email       *string     `json:"email"`
	Role        *store.Role `json:"role"`
	Sites       *[]string   `json:"sites"`
	Disabled    *bool       `json:"disabled"`
	// Password sets a new password, and ends the user's sessions.
	Password *string `json:"password"`
}

// UpdateUser changes the user with the given ID for actor, an
// administrator, signed in with the session with the ID keep. Super
// administrators keep their role and cannot be disabled, and neither can
// actor change their own role or disable themselves. Disabling users and
// setting their password ends their sessions, but keep.
func (s *Service) UpdateUser(ctx context.Context, actor store.User, keep, id string, ch UserChange) (store.User, error) {
	var signOut bool
	u, err := s.modify(ctx, id, func(u *store.User) error {
		demoted := ch.Role != nil && *ch.Role != u.Role && u.Role == store.Admin
		disabled := ch.Disabled != nil && *ch.Disabled && !u.Disabled
		// Other administrators must not take over super administrators,
		// e.g. by setting their password.
		identity := ch.Password != nil || (ch.Email != nil && strings.TrimSpace(*ch.Email) != u.Email)
		switch {
		case u.Managed && (demoted || disabled || (identity && u.ID != actor.ID)):
			return ErrManagedUser
		case u.ID == actor.ID && ((ch.Role != nil && *ch.Role != u.Role) || disabled):
			return ErrOwnAccount
		case ch.Password != nil:
			if err := s.passwordChangeable(*u); err != nil {
				return err
			}
		}
		if ch.DisplayName != nil {
			u.DisplayName = strings.TrimSpace(*ch.DisplayName)
		}
		if email := strings.TrimSpace(deref(ch.Email)); ch.Email != nil && email != u.Email {
			u.Email, u.EmailVerified = email, false
		}
		if ch.Role != nil {
			u.Role = *ch.Role
		}
		if ch.Sites != nil {
			u.Sites = *ch.Sites
		}
		if ch.Disabled != nil {
			u.Disabled = *ch.Disabled
		}
		errs := checkProfile(*u)
		if ch.Role != nil || ch.Sites != nil {
			errs = append(errs, s.checkAccess(ctx, *u)...)
		}
		if ch.Password != nil {
			if err := checkPassword("password", *ch.Password); err != nil {
				errs = append(errs, err)
			} else {
				u.PasswordHash = HashPassword(*ch.Password)
			}
		}
		if len(errs) > 0 {
			return errs
		}
		if demoted || disabled {
			if err := s.keepAdmin(ctx, u.ID); err != nil {
				return err
			}
		}
		signOut = disabled || ch.Password != nil
		return nil
	})
	if err != nil {
		return store.User{}, err
	}
	if signOut {
		if err := s.store.DeleteSessions(ctx, u.ID, keep); err != nil {
			return store.User{}, err
		}
	}
	s.log.InfoContext(ctx, "changed a user", "user", u.Username, "role", u.Role, "disabled", u.Disabled,
		"password", ch.Password != nil, "by", actor.Username)
	return u, nil
}

// keepAdmin fails with ErrLastAdmin unless an enabled administrator other
// than the user with the given ID remains.
func (s *Service) keepAdmin(ctx context.Context, id string) error {
	users, err := s.store.Users(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(users, func(u store.User) bool { return u.ID != id && u.Role == store.Admin && !u.Disabled }) {
		return ErrLastAdmin
	}
	return nil
}

// DeleteUser deletes the user with the given ID for actor, an
// administrator, who cannot delete super administrators or themselves.
func (s *Service) DeleteUser(ctx context.Context, actor store.User, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, err := s.user(ctx, id)
	switch {
	case err != nil:
		return err
	case u.Managed:
		return ErrManagedUser
	case u.ID == actor.ID:
		return ErrOwnAccount
	case u.Role == store.Admin && !u.Disabled:
		if err := s.keepAdmin(ctx, u.ID); err != nil {
			return err
		}
	}
	if err := s.store.DeleteUser(ctx, id); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "deleted a user", "user", u.Username, "by", actor.Username)
	return nil
}

// applyConfig makes the super administrators administrators, and demotes
// those who no longer are.
func (s *Service) applyConfig(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	users, err := s.store.Users(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Managed && !s.listed(u) {
			u.Managed, u.Role = false, store.NoRole
			if err := s.store.UpdateUser(ctx, u); err != nil {
				return err
			}
			s.log.WarnContext(ctx, "no longer a super administrator, so no longer has a role", "user", u.Username)
		}
	}
	for _, a := range s.cfg.Admins {
		if a.Username != "" {
			if err := s.applyAdmin(ctx, a); err != nil {
				return err
			}
		}
	}
	if users, err = s.store.Users(ctx); err != nil {
		return err
	}
	for _, a := range s.cfg.Admins {
		for _, u := range users {
			if s.oidcAdmin(a, u.OIDCIssuer, u.OIDCSubject, u.Email, u.EmailVerified) && (!u.Managed || u.Role != store.Admin || u.Disabled) {
				u.Role, u.Managed, u.Disabled = store.Admin, true, false
				if err := s.store.UpdateUser(ctx, u); err != nil {
					return err
				}
				s.log.InfoContext(ctx, "made a super administrator", "user", u.Username)
			}
		}
	}
	if len(s.cfg.Admins) > 0 {
		return nil
	}
	// Without any administrator, nobody could manage the users.
	users, err = s.store.Users(ctx)
	if err != nil || slices.ContainsFunc(users, func(u store.User) bool { return u.Role == store.Admin && !u.Disabled }) {
		return err
	}
	name := "admin"
	for i := 2; slices.ContainsFunc(users, func(u store.User) bool { return strings.EqualFold(u.Username, name) }); i++ {
		name = fmt.Sprintf("admin-%d", i)
	}
	password := rand.Text()
	u := store.User{ID: store.NewID(), Username: name, Role: store.Admin, PasswordHash: HashPassword(password)}
	if err := s.store.AddUser(ctx, u); err != nil {
		return err
	}
	s.log.WarnContext(ctx, "there is no administrator, so one was created: sign in with this password and change it, "+
		"or list the super administrators in the configuration file", "user", name, "password", password)
	return nil
}

// applyAdmin makes the account of a super administrator with a username
// an administrator, creating it if needed, with the configured password.
func (s *Service) applyAdmin(ctx context.Context, a AdminConfig) error {
	u, err := s.store.UserByName(ctx, a.Username)
	created := errors.Is(err, store.ErrNotFound)
	if err != nil && !created {
		return err
	}
	if created {
		u = store.User{ID: store.NewID(), Username: a.Username}
	}
	if u.OIDCSubject != "" {
		return fmt.Errorf("the super administrator %q is an account of the OIDC provider; list it by email or subject instead", a.Username)
	}
	before := u
	u.Role, u.Managed, u.Disabled = store.Admin, true, false
	var generated string
	switch {
	case IsHash(a.Password):
		u.PasswordHash = a.Password
	case a.Password != "":
		if !CheckPassword(u.PasswordHash, a.Password) {
			u.PasswordHash = HashPassword(a.Password)
		}
	case !before.Managed:
		// Whoever signed up with this username may not be who the
		// configuration means, so they do not keep their password.
		generated = rand.Text()
		u.PasswordHash = HashPassword(generated)
	}
	switch {
	case created:
		err = s.store.AddUser(ctx, u)
	case u.Role != before.Role || u.Managed != before.Managed || u.Disabled != before.Disabled || u.PasswordHash != before.PasswordHash:
		err = s.store.UpdateUser(ctx, u)
		if err == nil && u.PasswordHash != before.PasswordHash {
			err = s.store.DeleteSessions(ctx, u.ID, "")
		}
	}
	if err != nil {
		return err
	}
	switch {
	case generated != "" && created:
		s.log.WarnContext(ctx, "created the super administrator with a random password: sign in with it and change it, "+
			"or set it in the configuration file", "user", u.Username, "password", generated)
	case generated != "":
		s.log.WarnContext(ctx, "made an existing account a super administrator, with a new random password: sign in with it "+
			"and change it, or set it in the configuration file", "user", u.Username, "password", generated)
	case created:
		s.log.InfoContext(ctx, "created the super administrator", "user", u.Username)
	case !before.Managed:
		s.log.InfoContext(ctx, "made a super administrator", "user", u.Username)
	}
	return nil
}

// listed reports whether u is a super administrator of the configuration.
func (s *Service) listed(u store.User) bool {
	for _, a := range s.cfg.Admins {
		if a.Username != "" && u.OIDCSubject == "" && asciiLower(a.Username) == asciiLower(u.Username) ||
			s.oidcAdmin(a, u.OIDCIssuer, u.OIDCSubject, u.Email, u.EmailVerified) {
			return true
		}
	}
	return false
}

// oidcAdmin reports whether a names the account with the given identity
// at the OIDC provider, by subject or verified email.
func (s *Service) oidcAdmin(a AdminConfig, issuer, subject, email string, verified bool) bool {
	if s.oidc == nil || subject == "" || issuer != s.oidc.cfg.Issuer {
		return false
	}
	return a.Subject != "" && a.Subject == subject ||
		a.Email != "" && verified && asciiLower(a.Email) == asciiLower(email)
}

// asciiLower returns s with ASCII letters in lower case, and others as they
// are: unlike strings.EqualFold, comparing such strings does not take e.g.
// "ſ" for "s".
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// TrustedProxy reports whether addr is a reverse proxy whose
// X-Forwarded-For header tells the address of the client.
func (s *Service) TrustedProxy(addr netip.Addr) bool {
	for _, p := range s.trusted {
		if p.Contains(addr.Unmap()) {
			return true
		}
	}
	return false
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
