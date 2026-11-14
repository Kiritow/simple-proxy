// Package manager manages the proxied sites: it stores them and applies
// their configuration to nginx whenever they change, so that nginx always
// runs the stored sites.
package manager

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kiritow/simple-proxy/internal/acme"
	"github.com/kiritow/simple-proxy/internal/apply"
	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/nginx"
	"github.com/kiritow/simple-proxy/internal/store"
)

var (
	// ErrNotFound reports that a site does not exist.
	ErrNotFound = errors.New("site not found")
	// ErrConflict reports a change based on an outdated generation of a
	// site, which someone else changed in the meantime.
	ErrConflict = errors.New("the site was changed by someone else")
	// ErrNoCA reports that certificates cannot be managed.
	ErrNoCA = errors.New("certificates are not available: acme.sh is not configured")
	// ErrNoWebroot reports that HTTP validation is not configured.
	ErrNoWebroot = errors.New("HTTP validation needs an ACME webroot (-acme-webroot); use DNS validation instead")
)

// CertAuthority lists and issues certificates. *acme.Client implements it.
type CertAuthority interface {
	Certs(ctx context.Context) ([]acme.Cert, error)
	Issue(ctx context.Context, req acme.IssueRequest) (string, error)
}

// Options configure a Manager.
type Options struct {
	Store     *store.Store
	Generator *nginx.Generator
	// Applier installs the configuration into nginx. If nil, sites are only
	// stored.
	Applier *apply.Applier
	// CA manages certificates. If nil, certificates are not available.
	CA CertAuthority
	// ACMEWebroot is the directory that the Generator serves for HTTP-01
	// validation. If empty, certificates can only be issued with DNS-01.
	ACMEWebroot string
	// RenewHook is the command that acme.sh runs after renewing a
	// certificate it issued for the Manager, such as one reloading nginx.
	RenewHook string
	Logger    *slog.Logger
}

// Manager manages sites. It is safe for concurrent use.
type Manager struct {
	opts Options

	// mu serializes changes and applying, so that nginx runs the stored
	// sites.
	mu sync.Mutex
	// issueMu has acme.sh issue one certificate at a time. It is separate
	// from mu, since issuing can take minutes.
	issueMu sync.Mutex

	statusMu sync.Mutex
	status   Status
}

// New returns a Manager.
func New(opts Options) *Manager {
	m := &Manager{opts: opts}
	if a := opts.Applier; a != nil {
		m.status.Applies = true
		m.status.ConfigPath = a.Path
		m.status.TestCommand = strings.Join(a.Test, " ")
		m.status.ReloadCommand = strings.Join(a.Reload, " ")
	}
	m.status.Certificates = opts.CA != nil
	m.status.ACMEWebroot = opts.ACMEWebroot
	return m
}

// Status describes how the Manager applies configuration.
type Status struct {
	// Applies tells whether configuration is applied to nginx, with the
	// following file and commands. Otherwise sites are only stored.
	Applies       bool   `json:"applies"`
	ConfigPath    string `json:"config_path,omitempty"`
	TestCommand   string `json:"test_command,omitempty"`
	ReloadCommand string `json:"reload_command,omitempty"`
	// AppliedAt is when nginx last loaded the stored sites.
	AppliedAt *time.Time `json:"applied_at,omitempty"`
	// Error is set if nginx may not run the stored sites, because applying
	// them failed, e.g. on startup. Output is that of the failed command.
	Error  string `json:"error,omitempty"`
	Output string `json:"output,omitempty"`
	// Certificates tells whether certificates can be listed and issued;
	// with HTTP-01 validation only if ACMEWebroot is set.
	Certificates bool   `json:"certificates"`
	ACMEWebroot  string `json:"acme_webroot,omitempty"`
}

func (m *Manager) Status() Status {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	return m.status
}

// applied records the outcome of applying the stored sites.
func (m *Manager) applied(err error) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if err == nil {
		now := time.Now()
		m.status.AppliedAt = &now
		m.status.Error, m.status.Output = "", ""
		return
	}
	m.status.Error, m.status.Output = err.Error(), ""
	var aerr *apply.Error
	if errors.As(err, &aerr) {
		m.status.Error = fmt.Sprintf("%s failed: %v", aerr.Step, aerr.Err)
		m.status.Output = aerr.Output
	}
}

// SiteInfo is a stored site. Problem is set if the site is invalid, which
// only happens for sites stored by earlier versions; they are not applied.
type SiteInfo struct {
	store.Site
	Problem error
}

func (m *Manager) Sites(ctx context.Context) ([]SiteInfo, error) {
	sites, err := m.opts.Store.Sites(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]SiteInfo, len(sites))
	for i, site := range sites {
		_, err := parse([]store.Site{site})
		infos[i] = SiteInfo{Site: site, Problem: err}
	}
	return infos, nil
}

// CreateSite adds a site with the given configuration, one entry of servers
// in JSON. Problems with the site are reported as config.FieldErrors for
// servers[0], or as a config.FieldError for servers[0].cert_name wrapping
// nginx.ErrCertNotFound. If nginx rejects the configuration, the error is
// an *apply.Error. In both cases nothing changes.
func (m *Manager) CreateSite(ctx context.Context, cfg json.RawMessage) (store.Site, error) {
	site := store.Site{ID: rand.Text(), Config: cfg}
	err := m.change(ctx, func(sites []store.Site) ([]store.Site, error) {
		if err := m.checkSite(ctx, site, sites); err != nil {
			return nil, err
		}
		return append(sites, site), nil
	})
	if err != nil {
		return store.Site{}, err
	}
	return m.stored(ctx, site.ID)
}

// UpdateSite changes the configuration of a site, reporting problems like
// CreateSite. The change is based on the given generation of the site; if
// the site has changed since, UpdateSite fails with ErrConflict. A
// generation of 0 changes the site whatever its generation.
func (m *Manager) UpdateSite(ctx context.Context, id string, generation int64, cfg json.RawMessage) (store.Site, error) {
	site := store.Site{ID: id, Config: cfg}
	err := m.change(ctx, func(sites []store.Site) ([]store.Site, error) {
		i, err := find(sites, id, generation)
		if err != nil {
			return nil, err
		}
		others := slices.Delete(slices.Clone(sites), i, i+1)
		if err := m.checkSite(ctx, site, others); err != nil {
			return nil, err
		}
		sites[i] = site
		return sites, nil
	})
	if err != nil {
		return store.Site{}, err
	}
	return m.stored(ctx, id)
}

// DeleteSite removes a site, if it is still at the given generation, like
// UpdateSite.
func (m *Manager) DeleteSite(ctx context.Context, id string, generation int64) error {
	return m.change(ctx, func(sites []store.Site) ([]store.Site, error) {
		i, err := find(sites, id, generation)
		if err != nil {
			return nil, err
		}
		return slices.Delete(sites, i, i+1), nil
	})
}

// find returns the index of the site with the given ID, checking that it is
// at the given generation unless that is 0.
func find(sites []store.Site, id string, generation int64) (int, error) {
	i := slices.IndexFunc(sites, func(s store.Site) bool { return s.ID == id })
	switch {
	case i < 0:
		return 0, ErrNotFound
	case generation != 0 && sites[i].Generation != generation:
		return 0, fmt.Errorf("%w: it is at generation %d, not %d", ErrConflict, sites[i].Generation, generation)
	}
	return i, nil
}

// stored returns the stored site with the given ID.
func (m *Manager) stored(ctx context.Context, id string) (store.Site, error) {
	sites, err := m.opts.Store.Sites(ctx)
	if err != nil {
		return store.Site{}, err
	}
	i := slices.IndexFunc(sites, func(s store.Site) bool { return s.ID == id })
	if i < 0 {
		return store.Site{}, ErrNotFound
	}
	return sites[i], nil
}

// Preview shows what saving a site would change in the configuration.
type Preview struct {
	// Current and Proposed are the configuration before and after saving,
	// generated at the same time.
	Current, Proposed []byte
	// Generation is the site's current generation; 0 for a new site.
	Generation int64
}

// Preview checks a site like CreateSite, if id is empty, or UpdateSite, and
// returns what saving it would change, without changing anything. Unlike
// UpdateSite, it does not check generations, but reports the current one.
func (m *Manager) Preview(ctx context.Context, id string, cfg json.RawMessage) (Preview, error) {
	sites, err := m.opts.Store.Sites(ctx)
	if err != nil {
		return Preview{}, err
	}
	site := store.Site{ID: id, Config: cfg}
	next := slices.Clone(sites)
	var p Preview
	if id == "" {
		if err := m.checkSite(ctx, site, sites); err != nil {
			return Preview{}, err
		}
		next = append(next, site)
	} else {
		i, err := find(sites, id, 0)
		if err != nil {
			return Preview{}, err
		}
		p.Generation = sites[i].Generation
		others := slices.Delete(slices.Clone(sites), i, i+1)
		if err := m.checkSite(ctx, site, others); err != nil {
			return Preview{}, err
		}
		next[i] = site
	}

	now := time.Now()
	if p.Current, err = m.renderAt(ctx, sites, now); err != nil {
		return Preview{}, err
	}
	if p.Proposed, err = m.renderAt(ctx, next, now); err != nil {
		return Preview{}, err
	}
	return p, nil
}

// ReplaceSites replaces all sites with new ones with the given
// configurations, e.g. imported from a configuration file. Problems are
// reported like for CreateSite, but for servers[i] for configs[i].
func (m *Manager) ReplaceSites(ctx context.Context, configs []json.RawMessage) ([]store.Site, error) {
	sites := make([]store.Site, len(configs))
	for i, cfg := range configs {
		sites[i] = store.Site{ID: rand.Text(), Config: cfg}
	}
	err := m.change(ctx, func([]store.Site) ([]store.Site, error) {
		cfg, err := parse(sites)
		if err != nil {
			return nil, err
		}
		var errs config.FieldErrors
		for i, server := range cfg.Servers {
			field := fmt.Sprintf("servers[%d].server_name", i)
			errs = append(errs, domainConflicts(server, sites[:i], field)...)
		}
		if len(errs) > 0 {
			return nil, errs
		}
		return sites, nil
	})
	if err != nil {
		return nil, err
	}
	return m.opts.Store.Sites(ctx)
}

// change replaces the stored sites with those returned by edit, after
// applying their configuration.
func (m *Manager) change(ctx context.Context, edit func([]store.Site) ([]store.Site, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	current, err := m.opts.Store.Sites(ctx)
	if err != nil {
		return err
	}
	next, err := edit(slices.Clone(current))
	if err != nil {
		return err
	}
	conf, err := m.render(ctx, next)
	if err != nil {
		return err
	}
	if a := m.opts.Applier; a != nil {
		// If nginx rejects the configuration, Apply restores the previous
		// one, which nginx keeps running.
		if err := a.Apply(ctx, conf); err != nil {
			return err
		}
	}
	if err := m.opts.Store.ReplaceSites(ctx, next); err != nil {
		// nginx runs sites that are not stored: go back to the stored ones.
		m.applied(m.applyLocked(ctx, current))
		return err
	}
	if m.opts.Applier != nil {
		m.applied(nil)
	}
	return nil
}

// Apply applies the stored sites again, e.g. after nginx restarted.
func (m *Manager) Apply(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sites, err := m.opts.Store.Sites(ctx)
	if err != nil {
		return err
	}
	err = m.applyLocked(ctx, sites)
	m.applied(err)
	return err
}

func (m *Manager) applyLocked(ctx context.Context, sites []store.Site) error {
	conf, err := m.render(ctx, sites)
	if err != nil {
		return err
	}
	if m.opts.Applier == nil {
		return nil
	}
	return m.opts.Applier.Apply(ctx, conf)
}

// NginxConfig returns the configuration generated from the stored sites.
func (m *Manager) NginxConfig(ctx context.Context) ([]byte, error) {
	sites, err := m.opts.Store.Sites(ctx)
	if err != nil {
		return nil, err
	}
	return m.render(ctx, sites)
}

// render generates the configuration of the valid sites.
func (m *Manager) render(ctx context.Context, sites []store.Site) ([]byte, error) {
	return m.renderAt(ctx, sites, time.Now())
}

// renderAt is render, with the time written into the configuration.
func (m *Manager) renderAt(ctx context.Context, sites []store.Site, now time.Time) ([]byte, error) {
	valid := slices.DeleteFunc(slices.Clone(sites), func(s store.Site) bool {
		_, err := parse([]store.Site{s})
		return err != nil
	})
	cfg, err := parse(valid)
	if err != nil {
		return nil, err
	}
	gen := *m.opts.Generator
	gen.Now = func() time.Time { return now }
	conf, err := gen.Generate(ctx, cfg)
	if err != nil {
		// Paths into the combined configuration mean nothing to callers, so
		// do not wrap the error: the message names the certificate.
		return nil, fmt.Errorf("generating the nginx configuration: %v", err)
	}
	return conf, nil
}

// checkSite checks site on its own and against the other sites.
func (m *Manager) checkSite(ctx context.Context, site store.Site, others []store.Site) error {
	cfg, err := parse([]store.Site{site})
	if err != nil {
		return err
	}
	server := cfg.Servers[0]
	if !server.Enabled() {
		return nil // disabled sites may be incomplete in other ways
	}
	if errs := domainConflicts(server, others, "servers[0].server_name"); len(errs) > 0 {
		return errs
	}
	// Generating the site alone finds missing certificates, reported for
	// servers[0].cert_name.
	_, err = m.opts.Generator.Generate(ctx, cfg)
	return err
}

// domainConflicts reports the domains of server that enabled other sites
// use.
func domainConflicts(server config.Server, others []store.Site, field string) config.FieldErrors {
	if !server.Enabled() {
		return nil
	}
	taken := map[string]bool{}
	for _, other := range others {
		s, err := config.DecodeServer(other.Config)
		if err != nil || !s.Enabled() {
			continue
		}
		for _, name := range strings.Fields(s.ServerName) {
			taken[strings.ToLower(name)] = true
		}
	}
	var errs config.FieldErrors
	for _, name := range strings.Fields(server.ServerName) {
		if taken[strings.ToLower(name)] {
			problem := &config.Problem{Code: config.DomainInUse, Value: name}
			errs = append(errs, &config.FieldError{Field: field, Err: problem})
		}
	}
	return errs
}

// parse parses sites as one configuration, in which servers[i] is sites[i].
func parse(sites []store.Site) (*config.Config, error) {
	doc := struct {
		Servers []json.RawMessage `json:"servers"`
	}{Servers: make([]json.RawMessage, len(sites))}
	for i, site := range sites {
		doc.Servers[i] = site.Config
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return config.Parse(data)
}

// Certificates returns the certificates that sites can use.
func (m *Manager) Certificates(ctx context.Context) ([]acme.Cert, error) {
	if m.opts.CA == nil {
		return nil, ErrNoCA
	}
	return m.opts.CA.Certs(ctx)
}

// IssueCertificate has acme.sh issue a certificate for domains, validated
// with the acme.sh DNS API dnsAPI, or over HTTP if dnsAPI is empty. It
// returns the output of acme.sh, which explains failures.
func (m *Manager) IssueCertificate(ctx context.Context, domains []string, dnsAPI string) (string, error) {
	if m.opts.CA == nil {
		return "", ErrNoCA
	}
	req := acme.IssueRequest{Domains: domains, DNS: dnsAPI}
	if dnsAPI == "" {
		if m.opts.ACMEWebroot == "" {
			return "", ErrNoWebroot
		}
		req.Webroot = m.opts.ACMEWebroot
	}
	if m.opts.Applier != nil {
		req.RenewHook = m.opts.RenewHook
	}
	m.issueMu.Lock()
	defer m.issueMu.Unlock()
	return m.opts.CA.Issue(ctx, req)
}
