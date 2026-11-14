// Package nginx generates the nginx configuration of the reverse proxies
// described by a config.Config.
package nginx

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/kiritow/simple-proxy/internal/config"
)

// Default paths of the certificate served to requests for unknown hosts.
const (
	DefaultFallbackCert = "/certs/fallback.cer"
	DefaultFallbackKey  = "/certs/fallback.key"
)

// ErrCertNotFound reports that a certificate requested by the configuration
// does not exist. CertLocator implementations wrap it in their errors.
var ErrCertNotFound = errors.New("certificate not found")

// Certificate holds the paths of the files making up a TLS certificate.
type Certificate struct {
	Fullchain string // certificate followed by its intermediates
	Key       string // private key
	CA        string // CA chain used to verify OCSP responses
}

// CertLocator finds certificates by name.
type CertLocator interface {
	LocateCert(ctx context.Context, name string) (Certificate, error)
}

// Generator generates nginx configuration. Its zero value can only generate
// servers that do not use HTTPS.
type Generator struct {
	// Certs locates the certificates of HTTPS servers.
	Certs CertLocator
	// FallbackCert and FallbackKey are the paths of the certificate presented
	// by the catch-all server that rejects requests for unknown hosts.
	// They default to DefaultFallbackCert and DefaultFallbackKey.
	FallbackCert, FallbackKey string
	// ACMEWebroot, if set, is served at /.well-known/acme-challenge/ on port
	// 80, for every domain, so that acme.sh can obtain certificates with
	// HTTP-01 validation using --webroot.
	ACMEWebroot string
	// Now returns the generation time written in the output. It defaults to
	// time.Now.
	Now func() time.Time
}

//go:embed nginx.conf.tmpl
var confTemplateText string

var confTemplate = template.Must(template.New("nginx.conf").Parse(confTemplateText))

// Generate returns the configuration of cfg, to be included in nginx's http
// context. cfg must have been returned by config.Parse.
//
// If a certificate cannot be found, the error is a *config.FieldError for the
// server's cert_name field, wrapping ErrCertNotFound.
func (g *Generator) Generate(ctx context.Context, cfg *config.Config) ([]byte, error) {
	servers, err := g.servers(ctx, cfg)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}

	var buf bytes.Buffer
	err = confTemplate.Execute(&buf, confData{
		GeneratedAt:   now(),
		FallbackCert:  quote(cmp.Or(g.FallbackCert, DefaultFallbackCert)),
		FallbackKey:   quote(cmp.Or(g.FallbackKey, DefaultFallbackKey)),
		ACMEChallenge: g.acmeChallenge(),
		Servers:       servers,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type confData struct {
	GeneratedAt   time.Time
	FallbackCert  string
	FallbackKey   string
	ACMEChallenge *location // nil without ACMEWebroot
	Servers       []server
}

type server struct {
	Listen     string
	Name       string
	Directives []directive
	Locations  []location
}

type location struct {
	Match      string
	Directives []directive
}

type directive struct{ name, args string }

func (d directive) String() string {
	if d.args == "" {
		return d.name
	}
	return d.name + " " + d.args
}

func (g *Generator) servers(ctx context.Context, cfg *config.Config) ([]server, error) {
	var out []server
	// Servers often share a wildcard certificate, and lookups may be slow.
	certs := make(map[string]Certificate)

	for i, s := range cfg.Servers {
		if !s.Enabled() {
			continue
		}

		srv := server{Listen: strconv.Itoa(s.Port()), Name: s.ServerName}
		if s.UseHTTPS() {
			name := s.Certificate()
			cert, ok := certs[name]
			if !ok {
				var err error
				if cert, err = g.locateCert(ctx, name); err != nil {
					return nil, &config.FieldError{Field: fmt.Sprintf("servers[%d].cert_name", i), Err: err}
				}
				certs[name] = cert
			}
			srv.Listen += " ssl"
			srv.Directives = append(srv.Directives, tlsDirectives(cert)...)
		}
		if s.Gzip {
			srv.Directives = append(srv.Directives, gzipDirectives...)
		}
		srv.Directives = append(srv.Directives, userDirectives(s.Options)...)

		if s.Default.Enabled() {
			srv.Locations = append(srv.Locations, proxyLocation(s.Default))
		}
		for _, l := range s.Locations {
			if l.Enabled() {
				srv.Locations = append(srv.Locations, proxyLocation(l))
			}
		}

		if acme := g.acmeChallenge(); acme != nil && !s.UseHTTPS() && s.Port() == 80 {
			srv.Locations = append([]location{*acme}, srv.Locations...)
		}
		if s.UseHTTPS() {
			out = append(out, g.httpsRedirect(s))
		}
		out = append(out, srv)
	}
	return out, nil
}

func (g *Generator) locateCert(ctx context.Context, name string) (Certificate, error) {
	if g.Certs == nil {
		return Certificate{}, fmt.Errorf("%w: no certificate source configured", ErrCertNotFound)
	}
	return g.Certs.LocateCert(ctx, name)
}

// acmeChallenge returns the location answering ACME HTTP-01 challenges, or
// nil without ACMEWebroot.
func (g *Generator) acmeChallenge() *location {
	if g.ACMEWebroot == "" {
		return nil
	}
	return &location{
		Match:      "^~ /.well-known/acme-challenge/",
		Directives: []directive{{"root", quote(g.ACMEWebroot)}},
	}
}

// httpsRedirect returns a plain HTTP server redirecting to HTTPS server s.
func (g *Generator) httpsRedirect(s config.Server) server {
	target := "https://$host$request_uri"
	if port := s.Port(); port != 443 {
		target = fmt.Sprintf("https://$host:%d$request_uri", port)
	}
	redirect := directive{"return", "301 " + target}
	srv := server{Listen: "80", Name: s.ServerName}
	acme := g.acmeChallenge()
	if acme == nil {
		srv.Directives = []directive{redirect}
		return srv
	}
	// A return at the server level would also redirect the challenges.
	srv.Locations = []location{*acme, {Match: "/", Directives: []directive{redirect}}}
	return srv
}

func proxyLocation(l config.Location) location {
	upstream := "http://" + net.JoinHostPort(l.IP, strconv.Itoa(l.Port))
	ds := slices.Concat(proxyDirectives, []directive{{"proxy_pass", upstream}})
	if l.DisableCache {
		ds = append(ds, noCacheDirectives...)
	}
	if l.Realtime {
		ds = append(ds, realtimeDirectives...)
	}
	if l.WebSocket {
		ds = append(ds, websocketDirectives...)
	}
	ds = append(ds, userDirectives(l.Options)...)
	return location{Match: "^~ " + l.Path, Directives: ds}
}

func userDirectives(opts []config.Directive) []directive {
	ds := make([]directive, len(opts))
	for i, o := range opts {
		ds[i] = directive{o.Name, o.Args}
	}
	return ds
}

var quoter = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// quote returns s as a double-quoted nginx string.
func quote(s string) string { return `"` + quoter.Replace(s) + `"` }
