// Package config defines the YAML schema describing the reverse proxies to
// generate. The schema is compatible with the configuration files of
// legacy-proxy-generator; see config.yaml.example in the repository root.
package config

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// DefaultUpstreamIP is the upstream address used when none is configured.
const DefaultUpstreamIP = "127.0.0.1"

// Config is the root of a configuration document.
type Config struct {
	Servers []Server `yaml:"servers"`
}

// Server is a virtual host whose requests are proxied to upstream services.
type Server struct {
	// ServerName is nginx's server_name. Several names may be separated by
	// spaces.
	ServerName string `yaml:"server_name"`
	// Enable controls whether the server is generated. Defaults to true.
	Enable *bool `yaml:"enable"`
	// ListenPort is the port nginx listens on. See Server.Port for its default.
	ListenPort int `yaml:"listen_port"`
	// HTTPS enables TLS. See Server.UseHTTPS for its default.
	HTTPS *bool `yaml:"https"`
	// CertName is the name of the acme.sh certificate to serve. See
	// Server.Certificate for its default.
	CertName string `yaml:"cert_name"`
	// Gzip enables compression of common text-based content types.
	Gzip bool `yaml:"gzip"`
	// Options are extra directives appended to the server block.
	Options []Directive `yaml:"options"`

	// Default is the location serving "/" unless another path is given. Its
	// IP and Port are inherited by Locations that leave them unset.
	Default Location `yaml:"default"`
	// Locations are additional proxied path prefixes.
	Locations []Location `yaml:"locations"`
}

// Enabled reports whether s should be generated.
func (s Server) Enabled() bool { return s.Enable == nil || *s.Enable }

// UseHTTPS reports whether s is served over TLS. Unless HTTPS is set
// explicitly, TLS is enabled exactly when CertName is set.
func (s Server) UseHTTPS() bool {
	if s.HTTPS != nil {
		return *s.HTTPS
	}
	return s.CertName != ""
}

// Port returns the port s listens on: ListenPort if set, otherwise 443 for
// HTTPS servers and 80 for the others.
func (s Server) Port() int {
	switch {
	case s.ListenPort != 0:
		return s.ListenPort
	case s.UseHTTPS():
		return 443
	default:
		return 80
	}
}

// Certificate returns the name of the certificate served by s: CertName if
// set, otherwise the first server name.
func (s Server) Certificate() string {
	if s.CertName != "" {
		return s.CertName
	}
	if names := strings.Fields(s.ServerName); len(names) > 0 {
		return names[0]
	}
	return ""
}

// Location is a URL path prefix proxied to an upstream HTTP service.
type Location struct {
	// Enable controls whether the location is generated. Defaults to true.
	Enable *bool `yaml:"enable"`
	// Path is the URL prefix to match. It defaults to "/" for Server.Default
	// and is required for other locations.
	Path string `yaml:"path"`
	// IP is the upstream host. It defaults to the IP of Server.Default, and
	// then to DefaultUpstreamIP.
	IP string `yaml:"ip"`
	// Port is the upstream port. It defaults to the port of Server.Default.
	Port int `yaml:"port"`
	// WebSocket enables proxying of WebSocket connections.
	WebSocket bool `yaml:"websocket"`
	// DisableCache prevents nginx and clients from caching responses.
	DisableCache bool `yaml:"disable_cache"`
	// Realtime disables response buffering, e.g. for server-sent events.
	Realtime bool `yaml:"realtime"`
	// Options are extra directives appended to the location block.
	Options []Directive `yaml:"options"`
}

// Enabled reports whether l should be generated.
func (l Location) Enabled() bool { return l.Enable == nil || *l.Enable }

// Directive is a raw nginx directive, such as `add_header X-Example "value"`.
//
// In YAML a directive is written either as a string, or as a mapping with
// "key" and "value" fields as accepted by legacy-proxy-generator.
type Directive struct {
	Name string
	Args string
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Directive) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		s := strings.TrimSuffix(strings.TrimSpace(node.Value), ";")
		d.Name, d.Args = s, ""
		if i := strings.IndexFunc(s, unicode.IsSpace); i >= 0 {
			d.Name, d.Args = s[:i], strings.TrimSpace(s[i:])
		}
		return nil
	case yaml.MappingNode:
		var kv struct {
			Key   string `yaml:"key"`
			Value string `yaml:"value"`
		}
		if err := node.Decode(&kv); err != nil {
			return err
		}
		d.Name, d.Args = kv.Key, kv.Value
		return nil
	default:
		return fmt.Errorf("line %d: an option must be a string or a mapping with key and value", node.Line)
	}
}

// Parse decodes a configuration from YAML (or JSON, which is a subset of
// YAML), fills in inherited defaults and validates the result.
//
// Unknown fields are rejected so that typos do not silently change the
// generated configuration. If the document decodes but is invalid, the error
// is a FieldErrors. In the returned Config, every Location has its Path and IP
// set, and every enabled Location has a valid Port.
func Parse(data []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("config is empty")
		}
		return nil, err
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	for i := range c.Servers {
		s := &c.Servers[i]
		s.Default.Path = cmp.Or(s.Default.Path, "/")
		s.Default.IP = cmp.Or(s.Default.IP, DefaultUpstreamIP)
		for j := range s.Locations {
			l := &s.Locations[j]
			l.IP = cmp.Or(l.IP, s.Default.IP)
			l.Port = cmp.Or(l.Port, s.Default.Port)
		}
	}
}

// FieldError reports a problem with one field of a configuration.
type FieldError struct {
	// Field is the path of the field, such as "servers[0].locations[1].port".
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Err.Error() }

func (e *FieldError) Unwrap() error { return e.Err }

// FieldErrors lists the problems found in a configuration.
type FieldErrors []*FieldError

func (e FieldErrors) Error() string {
	msgs := make([]string, len(e))
	for i, fe := range e {
		msgs[i] = fe.Error()
	}
	return strings.Join(msgs, "\n")
}

func (e FieldErrors) Unwrap() []error {
	errs := make([]error, len(e))
	for i, fe := range e {
		errs[i] = fe
	}
	return errs
}

func (e *FieldErrors) add(field, format string, args ...any) {
	*e = append(*e, &FieldError{Field: field, Err: fmt.Errorf(format, args...)})
}

// validate makes sure that c can be rendered without producing a broken
// nginx configuration. It reports every problem found, not just the first.
func (c *Config) validate() error {
	var errs FieldErrors
	for i, s := range c.Servers {
		s.validate(&errs, fmt.Sprintf("servers[%d]", i))
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

func (s Server) validate(p *FieldErrors, at string) {
	names := strings.Fields(s.ServerName)
	if len(names) == 0 {
		p.add(at+".server_name", "is required")
	}
	for _, name := range names {
		if !isWord(name) {
			p.add(at+".server_name", "invalid name %q", name)
		}
	}
	if s.ListenPort != 0 && !isPort(s.ListenPort) {
		p.add(at+".listen_port", "%d is not a valid port", s.ListenPort)
	}
	if s.CertName != "" && !isWord(s.CertName) {
		p.add(at+".cert_name", "invalid name %q", s.CertName)
	}
	validateDirectives(p, at+".options", s.Options)

	s.Default.validate(p, at+".default")
	for i, l := range s.Locations {
		l.validate(p, fmt.Sprintf("%s.locations[%d]", at, i))
	}
}

func (l Location) validate(p *FieldErrors, at string) {
	switch {
	case l.Path == "":
		p.add(at+".path", "is required")
	case !strings.HasPrefix(l.Path, "/") || !isWord(l.Path):
		p.add(at+".path", "%q is not a URL path starting with /", l.Path)
	}
	if !isHost(l.IP) {
		p.add(at+".ip", "%q is not an IP address or host name", l.IP)
	}
	switch {
	case l.Port == 0 && l.Enabled():
		p.add(at+".port", "is required unless the default location sets one")
	case l.Port != 0 && !isPort(l.Port):
		p.add(at+".port", "%d is not a valid port", l.Port)
	}
	validateDirectives(p, at+".options", l.Options)
}

var directiveName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func validateDirectives(p *FieldErrors, at string, ds []Directive) {
	for i, d := range ds {
		field := fmt.Sprintf("%s[%d]", at, i)
		if !directiveName.MatchString(d.Name) {
			p.add(field, "invalid directive name %q", d.Name)
		}
		if err := checkArgs(d.Args); err != nil {
			p.add(field, "%v", err)
		}
	}
}

// checkArgs makes sure that args can be emitted verbatim as the arguments of
// a single directive: it must fit on one line, and characters that would end
// the directive, open or close a block, or start a comment must be quoted or
// escaped.
func checkArgs(args string) error {
	var quote rune
	escaped := false
	for _, r := range args {
		switch {
		case r == '\n' || r == '\r':
			return errors.New("must not contain line breaks")
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case strings.ContainsRune(";{}#", r):
			return fmt.Errorf("%q must be quoted or escaped", r)
		}
	}
	switch {
	case escaped:
		return errors.New("ends with a backslash")
	case quote != 0:
		return errors.New("has an unterminated quote")
	}
	return nil
}

// isWord reports whether s can be emitted as a single unquoted nginx token.
func isWord(s string) bool {
	return s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(`;{}"'\#`, r)
	})
}

var hostName = regexp.MustCompile(`^[A-Za-z0-9_]([A-Za-z0-9_.-]*[A-Za-z0-9_])?$`)

func isHost(s string) bool { return net.ParseIP(s) != nil || hostName.MatchString(s) }

func isPort(n int) bool { return n > 0 && n <= 65535 }
