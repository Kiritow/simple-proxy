package manager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kiritow/simple-proxy/internal/acme"
	"github.com/kiritow/simple-proxy/internal/certs"
	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/store"
)

var (
	// ErrNoCertificate reports that a certificate kept by the Manager does
	// not exist. (Sites refering to missing certificates are reported with
	// nginx.ErrCertNotFound instead.)
	ErrNoCertificate = errors.New("certificate not found")
	// ErrNoAuthority reports that a CA does not exist.
	ErrNoAuthority = errors.New("CA not found")
)

// Sources of certificates.
const (
	SourceACME     = "acme"     // managed by acme.sh
	SourceUploaded = "uploaded" // uploaded with its key
	SourceSigned   = "signed"   // signed by a CA kept by the Manager
)

// Certificate describes a certificate that sites can use.
type Certificate struct {
	// Name identifies the certificate, which sites refer to by name.
	Name    string   `json:"name"`
	Source  string   `json:"source"`
	Domains []string `json:"domains"`
	// Key describes the key, such as "EC P-256", or "ec-256" for acme.sh.
	Key     string `json:"key"`
	Issuer  string `json:"issuer"`
	Created string `json:"created,omitempty"` // RFC 3339, or acme.sh's format
	Expires string `json:"expires,omitempty"` // RFC 3339; unknown for acme.sh
	Renews  string `json:"renews,omitempty"`  // acme.sh's next renewal
	// CA, KeyType and Days describe how a signed certificate was signed, so
	// that it can be signed again.
	CA      string `json:"ca,omitempty"`
	KeyType string `json:"key_type,omitempty"`
	Days    int    `json:"days,omitempty"`
}

// CertificateList lists the certificates that sites can use.
type CertificateList struct {
	Certificates []Certificate `json:"certificates"`
	// ACMEError is set if acme.sh is available but failed to list its
	// certificates, which are missing from the list then.
	ACMEError string `json:"acme_error,omitempty"`
}

// Certificates returns the certificates that sites can use: those kept by
// the Manager, and those of acme.sh.
func (m *Manager) Certificates(ctx context.Context) (CertificateList, error) {
	stored, err := m.opts.Store.Certs(ctx)
	if err != nil {
		return CertificateList{}, err
	}
	list := CertificateList{Certificates: []Certificate{}}
	for _, c := range stored {
		cert, err := describeCert(c)
		if err != nil {
			return CertificateList{}, err
		}
		list.Certificates = append(list.Certificates, cert)
	}
	if m.opts.CA == nil {
		return list, nil
	}
	issued, err := m.opts.CA.Certs(ctx)
	if err != nil {
		m.opts.Logger.WarnContext(ctx, "listing the certificates of acme.sh", "err", err)
		list.ACMEError = err.Error()
		return list, nil
	}
	for _, c := range issued {
		list.Certificates = append(list.Certificates, Certificate{
			Name:    c.Name,
			Source:  SourceACME,
			Domains: c.Domains,
			Key:     c.KeyLength,
			Issuer:  c.CA,
			Created: c.Created,
			Renews:  c.Renew,
		})
	}
	return list, nil
}

func describeCert(c store.Cert) (Certificate, error) {
	pair, err := certs.Parse(c.CertPEM, c.KeyPEM)
	if err != nil {
		return Certificate{}, fmt.Errorf("stored certificate %s: %w", c.Name, err)
	}
	info := certs.Describe(pair)
	cert := Certificate{
		Name:    c.Name,
		Source:  SourceUploaded,
		Domains: info.Domains,
		Key:     info.Key,
		Issuer:  info.Issuer,
		Created: c.CreatedAt.Format(time.RFC3339),
		Expires: info.NotAfter.Format(time.RFC3339),
	}
	if c.CA != "" {
		cert.Source, cert.CA, cert.KeyType, cert.Days = SourceSigned, c.CA, c.KeyType, c.Days
	}
	return cert, nil
}

// UploadRequest uploads a certificate or a CA: its PEM chain and private
// key. Without a Name, it is named after the certificate.
type UploadRequest struct {
	Name string `json:"name"`
	Cert string `json:"cert"`
	Key  string `json:"key"`
	// Replace allows replacing the certificate of the same name.
	Replace bool `json:"replace"`
}

// UploadCertificate stores a certificate for sites to use. Problems with the
// request are reported as config.FieldErrors for the fields "name", "cert"
// and "key". Replacing a certificate that sites use applies them, so that
// nginx serves the new one; if nginx rejects it, the error is an
// *apply.Error, and nothing changes.
func (m *Manager) UploadCertificate(ctx context.Context, req UploadRequest) (Certificate, error) {
	pair, err := certs.Parse([]byte(req.Cert), []byte(req.Key))
	if err != nil {
		return Certificate{}, err
	}
	if err := certs.CheckCertificate(pair, time.Now()); err != nil {
		return Certificate{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = certs.DefaultName(pair)
	}
	served := pair.WithoutRoot()
	return m.putCert(ctx, store.Cert{Name: name, CertPEM: served.CertPEM(), KeyPEM: served.KeyPEM()}, req.Replace)
}

// SignRequest has a CA kept by the Manager sign a certificate, like
// certs.SignRequest. Without a Name, it is named after its first domain.
type SignRequest struct {
	Name      string   `json:"name"`
	Authority string   `json:"authority"`
	Domains   []string `json:"domains"`
	Days      int      `json:"days"`
	KeyType   string   `json:"key_type"`
	// Replace allows replacing the certificate of the same name, e.g. to
	// renew it.
	Replace bool `json:"replace"`
}

// SignCertificate has a CA sign a certificate with a new key, and stores it
// like UploadCertificate. Problems are reported for the fields "name",
// "authority", "domains", "days" and "key_type".
func (m *Manager) SignCertificate(ctx context.Context, req SignRequest) (Certificate, error) {
	sreq := certs.SignRequest{Domains: req.Domains, Days: req.Days, KeyType: req.KeyType}
	if err := sreq.Check(); err != nil {
		return Certificate{}, err
	}
	ca, err := m.authority(ctx, req.Authority)
	if errors.Is(err, ErrNoAuthority) {
		return Certificate{}, certs.FieldError("authority", certs.CANotFound, req.Authority)
	}
	if err != nil {
		return Certificate{}, err
	}
	caPair, err := certs.Parse(ca.CertPEM, ca.KeyPEM)
	if err != nil {
		return Certificate{}, fmt.Errorf("stored CA %s: %w", ca.Name, err)
	}
	pair, err := certs.Sign(caPair, sreq, time.Now())
	if err != nil {
		return Certificate{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" && certs.ValidCertName(sreq.Domains[0]) {
		name = sreq.Domains[0]
	}
	return m.putCert(ctx, store.Cert{
		Name:    name,
		CertPEM: pair.CertPEM(),
		KeyPEM:  pair.KeyPEM(),
		CA:      ca.Name,
		KeyType: sreq.KeyType,
		Days:    sreq.Days,
	}, req.Replace)
}

// putCert stores c, adding it or, if replace is set, replacing the
// certificate of the same name.
func (m *Manager) putCert(ctx context.Context, c store.Cert, replace bool) (Certificate, error) {
	switch {
	case c.Name == "":
		return Certificate{}, &config.FieldError{Field: "name", Err: &config.Problem{Code: config.Required}}
	case !certs.ValidCertName(c.Name):
		return Certificate{}, certs.FieldError("name", certs.InvalidCertName, c.Name)
	}
	if !replace && m.acmeHas(ctx, c.Name) {
		// It would hide the certificate of acme.sh from sites.
		return Certificate{}, certs.FieldError("name", certs.NameInUse, c.Name)
	}
	err := m.change(ctx, func(st *state) error {
		i := slices.IndexFunc(st.certs, func(s store.Cert) bool { return s.Name == c.Name })
		switch {
		case i < 0:
			st.certs = append(st.certs, c)
		case replace:
			st.certs[i] = c
		default:
			return certs.FieldError("name", certs.NameInUse, c.Name)
		}
		return nil
	})
	if err != nil {
		return Certificate{}, err
	}
	stored, err := m.opts.Store.Certs(ctx)
	if err != nil {
		return Certificate{}, err
	}
	i := slices.IndexFunc(stored, func(s store.Cert) bool { return s.Name == c.Name })
	if i < 0 {
		return Certificate{}, ErrNoCertificate // deleted meanwhile
	}
	return describeCert(stored[i])
}

// acmeHas reports whether acme.sh has a certificate of the given name, as
// far as it can tell.
func (m *Manager) acmeHas(ctx context.Context, name string) bool {
	if m.opts.CA == nil {
		return false
	}
	issued, err := m.opts.CA.Certs(ctx)
	return err == nil && slices.ContainsFunc(issued, func(c acme.Cert) bool { return c.Name == name })
}

// DeleteCertificate removes a certificate kept by the Manager. Certificates
// that enabled sites use cannot be removed; that is reported as a
// config.FieldError for the field "name".
func (m *Manager) DeleteCertificate(ctx context.Context, name string) error {
	return m.change(ctx, func(st *state) error {
		i := slices.IndexFunc(st.certs, func(c store.Cert) bool { return c.Name == name })
		if i < 0 {
			return ErrNoCertificate
		}
		if users := usersOf(st.sites, name); len(users) > 0 {
			return certs.FieldError("name", certs.CertInUse, strings.Join(users, ", "))
		}
		st.certs = slices.Delete(st.certs, i, i+1)
		return nil
	})
}

// usersOf returns the first domains of the enabled sites using the
// certificate of the given name.
func usersOf(sites []store.Site, cert string) []string {
	var users []string
	for _, site := range sites {
		s, err := config.DecodeServer(site.Config)
		if err != nil || !s.Enabled() || !s.UseHTTPS() || s.Certificate() != cert {
			continue
		}
		if names := strings.Fields(s.ServerName); len(names) > 0 {
			users = append(users, names[0])
		}
	}
	return users
}

// IssueCertificate has acme.sh issue a certificate for domains, validated
// with the acme.sh DNS API dnsAPI, or over HTTP if dnsAPI is empty. It
// returns the output of acme.sh, which explains failures.
func (m *Manager) IssueCertificate(ctx context.Context, domains []string, dnsAPI string) (string, error) {
	if m.opts.CA == nil {
		return "", ErrNoACME
	}
	if len(domains) > 0 {
		stored, err := m.opts.Store.Certs(ctx)
		if err != nil {
			return "", err
		}
		if slices.ContainsFunc(stored, func(c store.Cert) bool { return c.Name == domains[0] }) {
			// Sites would keep using the one kept here.
			return "", fmt.Errorf("%w: a certificate named %s was uploaded or signed here; delete it first", acme.ErrInvalidRequest, domains[0])
		}
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

// Authority describes a CA kept by the Manager to sign certificates.
type Authority struct {
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Issuer  string `json:"issuer"` // the subject, if self-signed
	Key     string `json:"key"`
	Created string `json:"created"` // RFC 3339
	Expires string `json:"expires"` // RFC 3339
	// Signed counts the certificates kept that the CA signed.
	Signed int `json:"signed"`
}

// Authorities returns the CAs kept by the Manager.
func (m *Manager) Authorities(ctx context.Context) ([]Authority, error) {
	cas, err := m.opts.Store.Authorities(ctx)
	if err != nil {
		return nil, err
	}
	stored, err := m.opts.Store.Certs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Authority, len(cas))
	for i, ca := range cas {
		if out[i], err = describeAuthority(ca, stored); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func describeAuthority(ca store.Authority, stored []store.Cert) (Authority, error) {
	pair, err := certs.Parse(ca.CertPEM, ca.KeyPEM)
	if err != nil {
		return Authority{}, fmt.Errorf("stored CA %s: %w", ca.Name, err)
	}
	info := certs.Describe(pair)
	a := Authority{
		Name:    ca.Name,
		Subject: info.Subject,
		Issuer:  info.Issuer,
		Key:     info.Key,
		Created: ca.CreatedAt.Format(time.RFC3339),
		Expires: info.NotAfter.Format(time.RFC3339),
	}
	for _, c := range stored {
		if c.CA == ca.Name {
			a.Signed++
		}
	}
	return a, nil
}

// AddAuthority stores a CA, which must not replace another. Problems are
// reported like for UploadCertificate.
func (m *Manager) AddAuthority(ctx context.Context, req UploadRequest) (Authority, error) {
	pair, err := certs.Parse([]byte(req.Cert), []byte(req.Key))
	if err != nil {
		return Authority{}, err
	}
	if err := certs.CheckCA(pair, time.Now()); err != nil {
		return Authority{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = certs.DefaultCAName(pair)
	}
	switch {
	case name == "":
		return Authority{}, &config.FieldError{Field: "name", Err: &config.Problem{Code: config.Required}}
	case !certs.ValidCAName(name):
		return Authority{}, certs.FieldError("name", certs.InvalidCAName, name)
	}
	ca := store.Authority{Name: name, CertPEM: pair.CertPEM(), KeyPEM: pair.KeyPEM()}
	err = m.opts.Store.AddAuthority(ctx, ca)
	if errors.Is(err, store.ErrExists) {
		return Authority{}, certs.FieldError("name", certs.NameInUse, name)
	}
	if err != nil {
		return Authority{}, err
	}
	ca, err = m.authority(ctx, name)
	if err != nil {
		return Authority{}, err
	}
	return describeAuthority(ca, nil)
}

// DeleteAuthority removes a CA. The certificates it signed stay, but cannot
// be renewed.
func (m *Manager) DeleteAuthority(ctx context.Context, name string) error {
	err := m.opts.Store.DeleteAuthority(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNoAuthority
	}
	return err
}

// AuthorityCert returns the certificate chain of a CA in PEM, for clients
// to trust.
func (m *Manager) AuthorityCert(ctx context.Context, name string) ([]byte, error) {
	ca, err := m.authority(ctx, name)
	if err != nil {
		return nil, err
	}
	return ca.CertPEM, nil
}

func (m *Manager) authority(ctx context.Context, name string) (store.Authority, error) {
	cas, err := m.opts.Store.Authorities(ctx)
	if err != nil {
		return store.Authority{}, err
	}
	i := slices.IndexFunc(cas, func(a store.Authority) bool { return a.Name == name })
	if i < 0 {
		return store.Authority{}, ErrNoAuthority
	}
	return cas[i], nil
}
