package auth

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// CallbackPath is where the OIDC provider sends users back to, relative to
// the console.
const CallbackPath = "api/auth/oidc/callback"

// Config is how users sign in, as the configuration file of simple-proxy
// sets it.
type Config struct {
	// Admins are the super administrators. They are always administrators,
	// which the console cannot change.
	Admins []AdminConfig `yaml:"admins"`
	// PasswordLogin enables signing in with a username and a password; it
	// defaults to true.
	PasswordLogin *bool `yaml:"password_login"`
	// OIDC enables signing in with an OpenID Connect provider.
	OIDC *OIDCConfig `yaml:"oidc"`
	// TrustedProxies are the addresses or networks (CIDR) of the reverse
	// proxies in front of the console, whose X-Forwarded-For headers tell
	// the addresses of clients, by which failed attempts to sign in are
	// limited. Without them, all clients behind a proxy share its address.
	TrustedProxies []string `yaml:"trusted_proxies"`

	trusted []netip.Prefix // parsed TrustedProxies
}

// AdminConfig names a super administrator: an account signing in with a
// password by its Username, or one signing in with OIDC by its Email or
// Subject.
type AdminConfig struct {
	Username string `yaml:"username"`
	// Password is the password of the account, or its hash, see
	// HashPassword. If empty, the account keeps its password, and if it is
	// created, gets a random one, which is logged.
	Password string `yaml:"password"`
	// Email is the address that the OIDC provider verified, and Subject the
	// subject (sub) of the account at the provider.
	Email   string `yaml:"email"`
	Subject string `yaml:"subject"`
}

// OIDCConfig is an OpenID Connect provider to sign in with.
type OIDCConfig struct {
	// Name is shown on the button signing in, e.g. "Keycloak"; it defaults
	// to the host of the Issuer.
	Name   string `yaml:"name"`
	Issuer string `yaml:"issuer"`
	// ClientID and ClientSecret are those of simple-proxy at the provider.
	// The secret may be empty for public clients, as PKCE protects the
	// flow.
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	// RedirectURL is the URL of the console's api/auth/oidc/callback, as the
	// provider knows it, e.g. https://proxy.example.com/api/auth/oidc/callback.
	RedirectURL string `yaml:"redirect_url"`
	// Scopes default to openid, profile and email; openid is always added.
	Scopes []string `yaml:"scopes"`
}

// LoadConfig reads the configuration file at path.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg, err := ParseConfig(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// ParseConfig parses a configuration file in YAML, and checks it.
func ParseConfig(data []byte) (Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, err
	}
	if err := cfg.check(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// PasswordEnabled reports whether users may sign in with a password.
func (c Config) PasswordEnabled() bool { return c.PasswordLogin == nil || *c.PasswordLogin }

// check checks the configuration, and fills in defaults.
func (c *Config) check() error {
	var errs []error
	if o := c.OIDC; o != nil {
		if err := o.check(); err != nil {
			errs = append(errs, fmt.Errorf("oidc: %w", err))
		}
	}
	if !c.PasswordEnabled() && c.OIDC == nil {
		errs = append(errs, errors.New("password_login is false, but there is no oidc provider to sign in with"))
	}
	c.trusted = nil
	for _, t := range c.TrustedProxies {
		prefix, err := netip.ParsePrefix(t)
		if addr, aerr := netip.ParseAddr(t); aerr == nil {
			prefix, err = addr.Unmap().Prefix(addr.Unmap().BitLen())
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("trusted_proxies: %q is not an IP address or network", t))
			continue
		}
		c.trusted = append(c.trusted, prefix.Masked())
	}
	usernames := map[string]bool{}
	for i, a := range c.Admins {
		at := fmt.Sprintf("admins[%d]", i)
		switch {
		case a.Username == "" && a.Email == "" && a.Subject == "":
			errs = append(errs, fmt.Errorf("%s: set the username of an account signing in with a password, or the email or subject of one signing in with OIDC", at))
		case a.Username != "" && (a.Email != "" || a.Subject != ""):
			errs = append(errs, fmt.Errorf("%s: username names an account signing in with a password, email and subject one signing in with OIDC; list them separately", at))
		case a.Username != "":
			if err := checkUsername(a.Username); err != nil {
				errs = append(errs, fmt.Errorf("%s.username: %w", at, err))
			}
			if usernames[strings.ToLower(a.Username)] {
				errs = append(errs, fmt.Errorf("%s.username: %q is listed twice", at, a.Username))
			}
			usernames[strings.ToLower(a.Username)] = true
			if a.Password != "" && !IsHash(a.Password) && len([]rune(a.Password)) < MinPasswordLength {
				errs = append(errs, fmt.Errorf("%s.password: needs at least %d characters", at, MinPasswordLength))
			}
		default:
			if a.Password != "" {
				errs = append(errs, fmt.Errorf("%s.password: accounts signing in with OIDC have no password", at))
			}
			if c.OIDC == nil {
				errs = append(errs, fmt.Errorf("%s: names an account signing in with OIDC, but there is no oidc provider", at))
			}
		}
	}
	return errors.Join(errs...)
}

func (o *OIDCConfig) check() error {
	var errs []error
	issuer, err := url.Parse(o.Issuer)
	if o.Issuer == "" || err != nil || (issuer.Scheme != "https" && issuer.Scheme != "http") || issuer.Host == "" {
		errs = append(errs, fmt.Errorf("issuer: %q is not an http(s) URL", o.Issuer))
	} else if o.Name == "" {
		o.Name = issuer.Host
	}
	if o.ClientID == "" {
		errs = append(errs, errors.New("client_id is required"))
	}
	redirect, err := url.Parse(o.RedirectURL)
	switch {
	case o.RedirectURL == "":
		errs = append(errs, fmt.Errorf("redirect_url is required, e.g. https://proxy.example.com/%s", CallbackPath))
	case err != nil || (redirect.Scheme != "https" && redirect.Scheme != "http") || redirect.Host == "":
		errs = append(errs, fmt.Errorf("redirect_url: %q is not an http(s) URL", o.RedirectURL))
	case !strings.HasSuffix(redirect.Path, "/"+CallbackPath) || redirect.RawQuery != "" || redirect.Fragment != "":
		errs = append(errs, fmt.Errorf("redirect_url: %q must end with /%s", o.RedirectURL, CallbackPath))
	}
	if len(o.Scopes) == 0 {
		o.Scopes = []string{"openid", "profile", "email"}
	} else if !slices.Contains(o.Scopes, "openid") {
		o.Scopes = append([]string{"openid"}, o.Scopes...)
	}
	return errors.Join(errs...)
}

// ConsoleURL returns the URL of the console, which the redirect URL is
// relative to; users go back there once signed in.
func (o *OIDCConfig) ConsoleURL() string {
	return strings.TrimSuffix(o.RedirectURL, CallbackPath)
}
