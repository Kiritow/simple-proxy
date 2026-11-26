package manager

import (
	"context"
	"crypto/x509"
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
	// ID identifies a certificate kept by the Manager for good, while its
	// name may change. acme.sh's certificates have none.
	ID string `json:"id,omitempty"`
	// Name identifies the certificate too, which sites refer to by name.
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
		list.Certificates = append(list.Certificates, describeACME(c))
	}
	return list, nil
}

// describeACME describes a certificate of acme.sh, as acme.sh lists it.
func describeACME(c acme.Cert) Certificate {
	return Certificate{
		Name:    c.Name,
		Source:  SourceACME,
		Domains: c.Domains,
		Key:     c.KeyLength,
		Issuer:  c.CA,
		Created: c.Created,
		Renews:  c.Renew,
	}
}

func describeCert(c store.Cert) (Certificate, error) {
	pair, err := certs.Parse(c.CertPEM, c.KeyPEM)
	if err != nil {
		return Certificate{}, fmt.Errorf("stored certificate %s: %w", c.Name, err)
	}
	info := certs.Describe(pair)
	cert := Certificate{
		ID:      c.ID,
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
	u, err := m.readUpload(ctx, req, false)
	if err != nil {
		return Certificate{}, err
	}
	if len(u.problems) > 0 {
		return Certificate{}, u.problems
	}
	served := u.pair.WithoutRoot()
	return m.putCert(ctx, store.Cert{Name: u.name, CertPEM: served.CertPEM(), KeyPEM: served.KeyPEM()}, req.Replace)
}

// upload is an upload as read, with the problems that keep it from being
// stored.
type upload struct {
	pair     *certs.Pair // its chain is empty if no certificate could be read
	name     string      // the name it would be stored under
	problems config.FieldErrors
}

// readUpload reads an upload of a certificate, or of a CA if authority is
// set, and finds all the problems with it.
func (m *Manager) readUpload(ctx context.Context, req UploadRequest, authority bool) (upload, error) {
	pair, err := certs.Read([]byte(req.Cert), []byte(req.Key))
	u := upload{pair: pair, name: strings.TrimSpace(req.Name), problems: fieldErrors(err)}
	if len(pair.Chain) > 0 {
		check, name := certs.CheckCertificate, certs.DefaultName
		if authority {
			check, name = certs.CheckCA, certs.DefaultCAName
		}
		u.problems = append(u.problems, fieldErrors(check(pair, time.Now()))...)
		if u.name == "" {
			u.name = name(pair)
		}
	}
	if u.name == "" && len(pair.Chain) == 0 {
		return u, nil // named after the certificate, once there is one
	}
	problems, err := m.checkName(ctx, u.name, authority, req.Replace)
	u.problems = append(u.problems, problems...)
	return u, err
}

// checkName checks the name of a new certificate, or CA if authority is set,
// which may replace the certificate of the same name if replace is set.
func (m *Manager) checkName(ctx context.Context, name string, authority, replace bool) (config.FieldErrors, error) {
	switch {
	case name == "":
		return config.FieldErrors{{Field: "name", Err: &config.Problem{Code: config.Required}}}, nil
	case !config.ValidCertName(name):
		return fieldErrors(certs.FieldError("name", certs.InvalidCertName, name)), nil
	}
	var taken bool
	if authority {
		_, err := m.authority(ctx, name)
		if err != nil && !errors.Is(err, ErrNoAuthority) {
			return nil, err
		}
		taken = err == nil
	} else if !replace {
		stored, err := m.opts.Store.Certs(ctx)
		if err != nil {
			return nil, err
		}
		// A certificate kept here would hide that of acme.sh from sites.
		taken = slices.ContainsFunc(stored, func(c store.Cert) bool { return c.Name == name }) || m.acmeHas(ctx, name)
	}
	if taken {
		return fieldErrors(certs.FieldError("name", certs.NameInUse, name)), nil
	}
	return nil, nil
}

// fieldErrors returns the problems that err reports, if any.
func fieldErrors(err error) config.FieldErrors {
	var list config.FieldErrors
	var one *config.FieldError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &list):
		return list
	case errors.As(err, &one):
		return config.FieldErrors{one}
	}
	return nil
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
	now := time.Now()
	s, err := m.readSign(ctx, req, now)
	if err != nil {
		return Certificate{}, err
	}
	if len(s.problems) > 0 {
		return Certificate{}, s.problems
	}
	pair, err := certs.Sign(s.ca, s.req, now)
	if err != nil {
		return Certificate{}, err
	}
	return m.putCert(ctx, store.Cert{
		Name:    s.name,
		CertPEM: pair.CertPEM(),
		KeyPEM:  pair.KeyPEM(),
		CA:      s.caName,
		KeyType: s.req.KeyType,
		Days:    s.req.Days,
	}, req.Replace)
}

// signing is a signing request as checked, with the problems that keep the
// certificate from being signed.
type signing struct {
	req                 certs.SignRequest // normalized
	ca                  *certs.Pair       // nil if missing
	caName              string
	name                string // the name it would be stored under
	notBefore, notAfter time.Time
	problems            config.FieldErrors
}

func (m *Manager) readSign(ctx context.Context, req SignRequest, now time.Time) (signing, error) {
	s := signing{
		req:  certs.SignRequest{Domains: req.Domains, Days: req.Days, KeyType: req.KeyType},
		name: strings.TrimSpace(req.Name),
	}
	ca, err := m.authority(ctx, req.Authority)
	switch {
	case req.Authority == "":
		s.problems = append(s.problems, &config.FieldError{Field: "authority", Err: &config.Problem{Code: config.Required}})
	case errors.Is(err, ErrNoAuthority):
		s.problems = append(s.problems, fieldErrors(certs.FieldError("authority", certs.CANotFound, req.Authority))...)
	case err != nil:
		return s, err
	default:
		if s.ca, err = certs.Parse(ca.CertPEM, ca.KeyPEM); err != nil {
			return s, fmt.Errorf("stored CA %s: %w", ca.Name, err)
		}
		s.caName = ca.Name
	}
	s.notBefore, s.notAfter, err = certs.Plan(s.ca, &s.req, now)
	s.problems = append(s.problems, fieldErrors(err)...)
	if s.name == "" && len(s.req.Domains) > 0 {
		s.name = s.req.Domains[0]
	}
	if s.name == "" {
		return s, nil // reported missing domains suffice
	}
	problems, err := m.checkName(ctx, s.name, false, req.Replace)
	s.problems = append(s.problems, problems...)
	return s, err
}

// CertificatePreview describes the certificate that an upload or a signing
// request would store, as far as it can tell, without storing anything.
type CertificatePreview struct {
	Name      string   `json:"name"` // as it would be stored
	Subject   string   `json:"subject"`
	Issuer    string   `json:"issuer"`
	Domains   []string `json:"domains"`
	Key       string   `json:"key"`
	NotBefore string   `json:"not_before,omitempty"` // RFC 3339
	NotAfter  string   `json:"not_after,omitempty"`
	// Chain describes the issuers following the certificate, as far as
	// they are known.
	Chain []ChainEntry `json:"chain"`
	// HasKey tells whether the private key of the certificate is known.
	HasKey bool `json:"has_key"`
}

// ChainEntry describes a certificate of a chain.
type ChainEntry struct {
	Subject  string `json:"subject"`
	NotAfter string `json:"not_after"` // RFC 3339
	// Root marks a self-signed certificate, which clients must trust
	// already; sites do not serve it.
	Root bool `json:"root"`
}

// PreviewUpload reads an upload of a certificate, or of a CA if authority is
// set, like UploadCertificate or AddAuthority, and describes what it would
// store, along with the problems that keep it from being stored. The error
// reports failures to check it.
func (m *Manager) PreviewUpload(ctx context.Context, req UploadRequest, authority bool) (CertificatePreview, config.FieldErrors, error) {
	u, err := m.readUpload(ctx, req, authority)
	if err != nil {
		return CertificatePreview{}, nil, err
	}
	p := CertificatePreview{Name: u.name, Domains: []string{}, Chain: chainOf(u.pair.Chain, 1)}
	if len(u.pair.Chain) > 0 {
		info := certs.Describe(u.pair)
		p.Subject, p.Issuer, p.Domains, p.Key = info.Subject, info.Issuer, info.Domains, info.Key
		p.NotBefore, p.NotAfter = info.NotBefore.Format(time.RFC3339), info.NotAfter.Format(time.RFC3339)
		p.HasKey = u.pair.Key != nil
	}
	return p, u.problems, nil
}

// PreviewSign checks a signing request like SignCertificate, and describes
// the certificate it would sign, without signing it.
func (m *Manager) PreviewSign(ctx context.Context, req SignRequest) (CertificatePreview, config.FieldErrors, error) {
	s, err := m.readSign(ctx, req, time.Now())
	if err != nil {
		return CertificatePreview{}, nil, err
	}
	p := CertificatePreview{
		Name:    s.name,
		Domains: s.req.Domains,
		Chain:   []ChainEntry{},
		HasKey:  true, // a new one
	}
	if p.Domains == nil {
		p.Domains = []string{}
	} else if len(p.Domains) > 0 {
		p.Subject = p.Domains[0]
	}
	if s.req.KeyType == certs.KeyEC || s.req.KeyType == certs.KeyRSA {
		p.Key = certs.KeyName(s.req.KeyType)
	}
	if s.req.Days >= 1 && s.req.Days <= certs.MaxDays {
		p.NotBefore, p.NotAfter = s.notBefore.Format(time.RFC3339), s.notAfter.Format(time.RFC3339)
	}
	if s.ca != nil {
		p.Issuer = certs.Describe(s.ca).Subject
		p.Chain = chainOf(s.ca.Chain, 0)
	}
	return p, s.problems, nil
}

// chainOf describes chain from its certificate at index from.
func chainOf(chain []*x509.Certificate, from int) []ChainEntry {
	out := []ChainEntry{}
	for i := from; i < len(chain); i++ {
		info := certs.DescribeCert(chain[i])
		out = append(out, ChainEntry{Subject: info.Subject, NotAfter: info.NotAfter.Format(time.RFC3339), Root: info.SelfSigned})
	}
	return out
}

// putCert stores c, adding it with a new ID or, if replace is set,
// replacing the certificate of the same name, whose ID it keeps. Its name
// must have been checked.
func (m *Manager) putCert(ctx context.Context, c store.Cert, replace bool) (Certificate, error) {
	err := m.change(ctx, func(st *state) error {
		i := slices.IndexFunc(st.certs, func(s store.Cert) bool { return s.Name == c.Name })
		switch {
		case i < 0:
			c.ID = store.NewID()
			st.certs = append(st.certs, c)
		case replace:
			c.ID = st.certs[i].ID
			st.certs[i] = c
		default: // added meanwhile
			return certs.FieldError("name", certs.NameInUse, c.Name)
		}
		return nil
	})
	if err != nil {
		return Certificate{}, err
	}
	stored, err := m.storedCert(ctx, func(s store.Cert) bool { return s.ID == c.ID })
	if err != nil {
		return Certificate{}, err
	}
	return describeCert(stored)
}

// storedCert returns the first certificate kept by the Manager for which
// match is true.
func (m *Manager) storedCert(ctx context.Context, match func(store.Cert) bool) (store.Cert, error) {
	stored, err := m.opts.Store.Certs(ctx)
	if err != nil {
		return store.Cert{}, err
	}
	i := slices.IndexFunc(stored, match)
	if i < 0 {
		return store.Cert{}, ErrNoCertificate // e.g. deleted meanwhile
	}
	return stored[i], nil
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
	for _, ref := range sitesServing(sites, cert) {
		if ref.Enabled && ref.Name != "" {
			users = append(users, ref.Name)
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
	ID      string `json:"id"` // like that of a Certificate
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
		ID:      ca.ID,
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
	req.Replace = false
	u, err := m.readUpload(ctx, req, true)
	if err != nil {
		return Authority{}, err
	}
	if len(u.problems) > 0 {
		return Authority{}, u.problems
	}
	ca := store.Authority{ID: store.NewID(), Name: u.name, CertPEM: u.pair.CertPEM(), KeyPEM: u.pair.KeyPEM()}
	err = m.opts.Store.AddAuthority(ctx, ca)
	if errors.Is(err, store.ErrExists) { // added meanwhile
		return Authority{}, certs.FieldError("name", certs.NameInUse, u.name)
	}
	if err != nil {
		return Authority{}, err
	}
	if ca, err = m.authority(ctx, u.name); err != nil {
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
