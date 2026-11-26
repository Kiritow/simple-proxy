// Package manager manages the proxied sites: it stores them and applies
// their configuration to nginx whenever they change, so that nginx always
// runs the stored sites.
package manager

import (
	"bytes"
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
	"github.com/kiritow/simple-proxy/internal/certs"
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
	// ErrNoACME reports that acme.sh is not available.
	ErrNoACME = errors.New("acme.sh is not available")
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
	Store *store.Store
	// Generator generates the nginx configuration. Its Certs, if any, find
	// the certificates that the Manager does not keep itself, i.e. those of
	// acme.sh.
	Generator *nginx.Generator
	// CertDir is where the files of the certificates kept by the Manager are
	// written for nginx.
	CertDir certs.Dir
	// Applier installs the configuration into nginx. If nil, sites are only
	// stored.
	Applier *apply.Applier
	// CA manages certificates with acme.sh. If nil, only certificates kept
	// by the Manager are available.
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
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	m := &Manager{opts: opts}
	if a := opts.Applier; a != nil {
		m.status.Applies = true
		m.status.ConfigPath = a.Path
		m.status.TestCommand = strings.Join(a.Test, " ")
		m.status.ReloadCommand = strings.Join(a.Reload, " ")
		m.status.VersionCommand = strings.Join(a.Version, " ")
	}
	m.status.ACME = opts.CA != nil
	m.status.ACMEWebroot = opts.ACMEWebroot
	m.status.CertDir = string(opts.CertDir)
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
	// Nginx describes nginx, as VersionCommand prints it, once known;
	// NginxError tells why it is not known otherwise. Both are found out
	// whenever the stored sites are applied again, e.g. on startup.
	VersionCommand string       `json:"version_command,omitempty"`
	Nginx          *apply.Nginx `json:"nginx,omitempty"`
	NginxError     string       `json:"nginx_error,omitempty"`
	// AppliedAt is when nginx last loaded the stored sites.
	AppliedAt *time.Time `json:"applied_at,omitempty"`
	// Error is set if nginx may not run the stored sites, because applying
	// them failed, e.g. on startup. Output is that of the failed command.
	Error  string `json:"error,omitempty"`
	Output string `json:"output,omitempty"`
	// ACME tells whether acme.sh can list and issue certificates; with
	// HTTP-01 validation only if ACMEWebroot is set.
	ACME        bool   `json:"acme"`
	ACMEWebroot string `json:"acme_webroot,omitempty"`
	// CertDir is where the files of uploaded and signed certificates are
	// written for nginx.
	CertDir string `json:"cert_dir"`
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
	err := m.change(ctx, func(st *state) error {
		if err := m.checkSite(ctx, site, st.sites, st.certs); err != nil {
			return err
		}
		st.sites = append(st.sites, site)
		return nil
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
	err := m.change(ctx, func(st *state) error {
		i, err := find(st.sites, id, generation)
		if err != nil {
			return err
		}
		others := slices.Delete(slices.Clone(st.sites), i, i+1)
		if err := m.checkSite(ctx, site, others, st.certs); err != nil {
			return err
		}
		st.sites[i] = site
		return nil
	})
	if err != nil {
		return store.Site{}, err
	}
	return m.stored(ctx, id)
}

// DeleteSite removes a site, if it is still at the given generation, like
// UpdateSite.
func (m *Manager) DeleteSite(ctx context.Context, id string, generation int64) error {
	return m.change(ctx, func(st *state) error {
		i, err := find(st.sites, id, generation)
		if err != nil {
			return err
		}
		st.sites = slices.Delete(st.sites, i, i+1)
		return nil
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
	// Site is the configuration generated for the site alone, as it appears
	// in Proposed, or would once enabled. It is empty if the site cannot be
	// generated, which only disabled sites get away with, as they are not
	// checked as thoroughly.
	Site string
	// Generation is the site's current generation; 0 for a new site.
	Generation int64
}

// Preview checks a site like CreateSite, if id is empty, or UpdateSite, and
// returns what saving it would change, without changing anything. Unlike
// UpdateSite, it does not check generations, but reports the current one.
//
// If visible is not nil, the configuration before and after saving only
// has the sites whose IDs it reports, and the site saved, for users who may
// not see the others. The site is checked against all sites regardless.
func (m *Manager) Preview(ctx context.Context, id string, cfg json.RawMessage, visible func(id string) bool) (Preview, error) {
	current, err := m.load(ctx)
	if err != nil {
		return Preview{}, err
	}
	site := store.Site{ID: id, Config: cfg}
	next := current.clone()
	var p Preview
	if id == "" {
		if err := m.checkSite(ctx, site, current.sites, current.certs); err != nil {
			return Preview{}, err
		}
		next.sites = append(next.sites, site)
	} else {
		i, err := find(current.sites, id, 0)
		if err != nil {
			return Preview{}, err
		}
		p.Generation = current.sites[i].Generation
		others := slices.Delete(slices.Clone(current.sites), i, i+1)
		if err := m.checkSite(ctx, site, others, current.certs); err != nil {
			return Preview{}, err
		}
		next.sites[i] = site
	}

	if visible != nil {
		hidden := func(s store.Site) bool { return s.ID != id && !visible(s.ID) }
		current.sites = slices.DeleteFunc(slices.Clone(current.sites), hidden)
		next.sites = slices.DeleteFunc(next.sites, hidden)
	}
	now := time.Now()
	if p.Current, err = m.renderAt(ctx, current, now); err != nil {
		return Preview{}, err
	}
	if p.Proposed, err = m.renderAt(ctx, next, now); err != nil {
		return Preview{}, err
	}
	p.Site = m.siteConfig(ctx, site, current.certs)
	return p, nil
}

// siteConfig returns the configuration generated for site alone, as it
// would be enabled, or "" if it cannot be generated.
func (m *Manager) siteConfig(ctx context.Context, site store.Site, certs []store.Cert) string {
	cfg, err := parse([]store.Site{site})
	if err != nil {
		return ""
	}
	cfg.Servers[0].Enable = nil
	text, err := m.generator(certs).Servers(ctx, cfg)
	if err != nil {
		return ""
	}
	return text
}

// ReplaceSites replaces all sites with new ones with the given
// configurations, e.g. imported from a configuration file. Problems are
// reported like for CreateSite, but for servers[i] for configs[i].
func (m *Manager) ReplaceSites(ctx context.Context, configs []json.RawMessage) ([]store.Site, error) {
	sites := make([]store.Site, len(configs))
	for i, cfg := range configs {
		sites[i] = store.Site{ID: rand.Text(), Config: cfg}
	}
	err := m.change(ctx, func(st *state) error {
		cfg, err := parse(sites)
		if err != nil {
			return err
		}
		var errs config.FieldErrors
		for i, server := range cfg.Servers {
			errs = append(errs, domainConflicts(server, sites[:i], fmt.Sprintf("servers[%d]", i))...)
		}
		if len(errs) > 0 {
			return errs
		}
		st.sites = sites
		return nil
	})
	if err != nil {
		return nil, err
	}
	return m.opts.Store.Sites(ctx)
}

// state is what the nginx configuration is generated from: the sites, and
// the certificates kept by the Manager.
type state struct {
	sites []store.Site
	certs []store.Cert
}

func (m *Manager) load(ctx context.Context) (state, error) {
	sites, err := m.opts.Store.Sites(ctx)
	if err != nil {
		return state{}, err
	}
	certs, err := m.opts.Store.Certs(ctx)
	if err != nil {
		return state{}, err
	}
	return state{sites: sites, certs: certs}, nil
}

func (st state) clone() state {
	return state{sites: slices.Clone(st.sites), certs: slices.Clone(st.certs)}
}

// change stores the state as changed by edit, after applying its
// configuration. Unless the sites changed, nginx is left alone if the
// configuration stays the same, e.g. when an unused certificate changes.
func (m *Manager) change(ctx context.Context, edit func(*state) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	current, err := m.load(ctx)
	if err != nil {
		return err
	}
	next := current.clone()
	if err := edit(&next); err != nil {
		return err
	}
	now := time.Now()
	conf, err := m.renderAt(ctx, next, now)
	if err != nil {
		return err
	}
	apply := m.opts.Applier != nil
	if apply && slices.EqualFunc(current.sites, next.sites, sameSite) {
		before, err := m.renderAt(ctx, current, now)
		apply = err != nil || !bytes.Equal(before, conf)
	}

	if err := m.writeCerts(next); err != nil {
		return err
	}
	if apply {
		// If nginx rejects the configuration, Apply restores the previous
		// one, which nginx keeps running.
		if err := m.opts.Applier.Apply(ctx, conf); err != nil {
			m.pruneCerts(current)
			return err
		}
	}
	if err := m.opts.Store.Replace(ctx, next.sites, next.certs); err != nil {
		// nginx runs sites that are not stored: go back to the stored ones.
		if apply {
			m.applied(m.applyLocked(ctx, current))
		}
		m.pruneCerts(current)
		return err
	}
	m.pruneCerts(next)
	if apply {
		m.applied(nil)
	}
	return nil
}

func sameSite(a, b store.Site) bool { return a.ID == b.ID && bytes.Equal(a.Config, b.Config) }

// writeCerts writes the files of the certificates of st for nginx.
func (m *Manager) writeCerts(st state) error {
	for _, c := range st.certs {
		if err := m.opts.CertDir.Write(c.CertPEM, c.KeyPEM); err != nil {
			return fmt.Errorf("writing the files of certificate %s: %w", c.Name, err)
		}
	}
	return nil
}

// pruneCerts removes the files of certificates other than those of st.
func (m *Manager) pruneCerts(st state) {
	keep := make([]nginx.Certificate, len(st.certs))
	for i, c := range st.certs {
		keep[i] = m.opts.CertDir.Files(c.CertPEM, c.KeyPEM)
	}
	if err := m.opts.CertDir.Prune(keep); err != nil {
		m.opts.Logger.Warn("removing the files of old certificates", "err", err)
	}
}

// Apply applies the stored sites again, e.g. after nginx restarted, and
// finds out nginx's version, which may have changed then.
func (m *Manager) Apply(ctx context.Context) error {
	m.describeNginx(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	st, err := m.load(ctx)
	if err != nil {
		return err
	}
	err = m.applyLocked(ctx, st)
	if err == nil {
		m.pruneCerts(st)
	}
	m.applied(err)
	return err
}

// describeNginx records the version of nginx in the status.
func (m *Manager) describeNginx(ctx context.Context) {
	if m.opts.Applier == nil || len(m.opts.Applier.Version) == 0 {
		return
	}
	n, err := m.opts.Applier.Nginx(ctx)
	if err != nil {
		m.opts.Logger.WarnContext(ctx, "finding out nginx's version", "err", err)
	}
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	if err != nil {
		m.status.Nginx, m.status.NginxError = nil, err.Error()
		return
	}
	m.status.Nginx, m.status.NginxError = &n, ""
}

func (m *Manager) applyLocked(ctx context.Context, st state) error {
	conf, err := m.renderAt(ctx, st, time.Now())
	if err != nil {
		return err
	}
	if err := m.writeCerts(st); err != nil {
		return err
	}
	if m.opts.Applier == nil {
		return nil
	}
	return m.opts.Applier.Apply(ctx, conf)
}

// NginxConfig returns the configuration generated from the stored sites.
func (m *Manager) NginxConfig(ctx context.Context) ([]byte, error) {
	st, err := m.load(ctx)
	if err != nil {
		return nil, err
	}
	return m.renderAt(ctx, st, time.Now())
}

// RawConfig returns the raw configuration of a site, one entry of servers in
// JSON: the configuration generated for it, to be edited by hand, with
// references to its certificate. Problems are reported like for
// CreateSite.
func (m *Manager) RawConfig(ctx context.Context, cfg json.RawMessage) (string, error) {
	parsed, err := parse([]store.Site{{Config: cfg}})
	if err != nil {
		return "", err
	}
	server := parsed.Servers[0]
	if server.Raw != nil {
		return *server.Raw, nil
	}
	server.Enable = nil // the configuration it would have when enabled
	parsed.Servers[0] = server
	return m.opts.Generator.Raw(ctx, parsed)
}

// Generate generates the configuration of cfg, which is not stored, with
// the certificates that are.
func (m *Manager) Generate(ctx context.Context, cfg *config.Config) ([]byte, error) {
	certs, err := m.opts.Store.Certs(ctx)
	if err != nil {
		return nil, err
	}
	return m.generator(certs).Generate(ctx, cfg)
}

// generator returns the Generator, finding certificates among certs before
// asking acme.sh.
func (m *Manager) generator(certs []store.Cert) *nginx.Generator {
	gen := *m.opts.Generator
	gen.Certs = locator{dir: m.opts.CertDir, certs: certs, next: m.opts.Generator.Certs}
	return &gen
}

// locator finds certificates kept by the Manager, and then those of next,
// if not nil.
type locator struct {
	dir   certs.Dir
	certs []store.Cert
	next  nginx.CertLocator
}

func (l locator) LocateCert(ctx context.Context, name string) (nginx.Certificate, error) {
	for _, c := range l.certs {
		if c.Name == name {
			return l.dir.Files(c.CertPEM, c.KeyPEM), nil
		}
	}
	if l.next != nil {
		cert, err := l.next.LocateCert(ctx, name)
		if !errors.Is(err, nginx.ErrCertNotFound) {
			return cert, err
		}
	}
	return nginx.Certificate{}, fmt.Errorf("%w: there is no certificate named %q", nginx.ErrCertNotFound, name)
}

// renderAt generates the configuration of the valid sites of st, with the
// given time written into it.
func (m *Manager) renderAt(ctx context.Context, st state, now time.Time) ([]byte, error) {
	valid := slices.DeleteFunc(slices.Clone(st.sites), func(s store.Site) bool {
		_, err := parse([]store.Site{s})
		return err != nil
	})
	cfg, err := parse(valid)
	if err != nil {
		return nil, err
	}
	gen := m.generator(st.certs)
	gen.Now = func() time.Time { return now }
	conf, err := gen.Generate(ctx, cfg)
	if err != nil {
		// Paths into the combined configuration mean nothing to callers, so
		// do not wrap the error: the message names the certificate.
		return nil, fmt.Errorf("generating the nginx configuration: %v", err)
	}
	return conf, nil
}

// checkSite checks site on its own and against the other sites, with the
// certificates kept by the Manager.
func (m *Manager) checkSite(ctx context.Context, site store.Site, others []store.Site, certs []store.Cert) error {
	cfg, err := parse([]store.Site{site})
	if err != nil {
		return err
	}
	server := cfg.Servers[0]
	if !server.Enabled() {
		return nil // disabled sites may be incomplete in other ways
	}
	if errs := domainConflicts(server, others, "servers[0]"); len(errs) > 0 {
		return errs
	}
	// Generating the site alone finds missing certificates, reported for
	// servers[0].cert_name.
	_, err = m.generator(certs).Generate(ctx, cfg)
	return err
}

// domainConflicts reports the domains of server, at the given path such as
// "servers[0]", that enabled other sites use.
func domainConflicts(server config.Server, others []store.Site, at string) config.FieldErrors {
	if !server.Enabled() {
		return nil
	}
	field := at + ".server_name"
	if server.Raw != nil {
		field = at + ".raw"
	}
	taken := map[string]bool{}
	for _, other := range others {
		s, err := config.DecodeServer(other.Config)
		if err != nil || !s.Enabled() {
			continue
		}
		for _, name := range s.Names() {
			taken[strings.ToLower(name)] = true
		}
	}
	var errs config.FieldErrors
	for _, name := range server.Names() {
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
