package manager

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kiritow/simple-proxy/internal/acme"
	"github.com/kiritow/simple-proxy/internal/certs"
	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/store"
)

// CertificateDetails describes a certificate in full, for people to inspect.
type CertificateDetails struct {
	Certificate
	Subject   string `json:"subject,omitempty"`
	NotBefore string `json:"not_before,omitempty"` // RFC 3339
	// Serial is the serial number and SHA256 the SHA-256 fingerprint of the
	// certificate, as colon-separated hex bytes.
	Serial string `json:"serial,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	// Chain describes the issuers following the certificate, as far as they
	// are known.
	Chain []ChainEntry `json:"chain"`
	// Updated is when the certificate kept by the Manager last changed, in
	// RFC 3339.
	Updated string `json:"updated,omitempty"`
	// CertFile and KeyFile are the files that nginx reads.
	CertFile string `json:"cert_file,omitempty"`
	KeyFile  string `json:"key_file,omitempty"`
	// Sites are the sites that serve the certificate, enabled or not.
	Sites []SiteRef `json:"sites"`
	// Problem is set if the files of a certificate of acme.sh could not be
	// read, so that only what acme.sh lists about it is known.
	Problem string `json:"problem,omitempty"`
}

// SiteRef refers to a site.
type SiteRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"` // its first server name, if any
	Enabled bool   `json:"enabled"`
	Raw     bool   `json:"raw,omitempty"` // configured by a raw configuration
}

// CertificateDetails describes the certificate of the given name, kept by
// the Manager or by acme.sh, in full. If there is none, it fails with
// ErrNoCertificate.
func (m *Manager) CertificateDetails(ctx context.Context, name string) (CertificateDetails, error) {
	st, err := m.load(ctx)
	if err != nil {
		return CertificateDetails{}, err
	}
	d := CertificateDetails{Chain: []ChainEntry{}, Sites: sitesServing(st.sites, name)}
	if i := slices.IndexFunc(st.certs, named(name)); i >= 0 {
		c := st.certs[i]
		if d.Certificate, err = describeCert(c); err != nil {
			return CertificateDetails{}, err
		}
		pair, err := certs.Parse(c.CertPEM, c.KeyPEM)
		if err != nil {
			return CertificateDetails{}, fmt.Errorf("stored certificate %s: %w", c.Name, err)
		}
		d.describeChain(pair.Chain)
		d.Updated = c.UpdatedAt.Format(time.RFC3339)
		files := m.opts.CertDir.Files(c.CertPEM, c.KeyPEM)
		d.CertFile, d.KeyFile = files.Fullchain, files.Key
		return d, nil
	}

	if m.opts.CA == nil {
		return CertificateDetails{}, ErrNoCertificate
	}
	issued, err := m.opts.CA.Certs(ctx)
	if err != nil {
		return CertificateDetails{}, err
	}
	i := slices.IndexFunc(issued, func(c acme.Cert) bool { return c.Name == name })
	if i < 0 {
		return CertificateDetails{}, ErrNoCertificate
	}
	d.Certificate = describeACME(issued[i])
	if err := m.readACME(ctx, &d); err != nil {
		d.Problem = err.Error()
	}
	return d, nil
}

// readACME completes the details of a certificate of acme.sh from its
// files, which describe it better than acme.sh's list.
func (m *Manager) readACME(ctx context.Context, d *CertificateDetails) error {
	if m.opts.Generator == nil || m.opts.Generator.Certs == nil {
		return errors.New("the files of acme.sh's certificates are unknown")
	}
	files, err := m.opts.Generator.Certs.LocateCert(ctx, d.Name)
	if err != nil {
		return err
	}
	d.CertFile, d.KeyFile = files.Fullchain, files.Key
	data, err := os.ReadFile(files.Fullchain)
	if err != nil {
		return err
	}
	pair, _ := certs.Read(data, nil) // without the key, which is not needed
	if len(pair.Chain) == 0 {
		return fmt.Errorf("%s holds no certificate", files.Fullchain)
	}
	info := d.describeChain(pair.Chain)
	d.Domains, d.Key, d.Issuer = info.Domains, info.Key, info.Issuer
	d.Expires = info.NotAfter.Format(time.RFC3339)
	return nil
}

// describeChain describes the certificate chain, starting with the
// certificate itself, and returns the description of the latter.
func (d *CertificateDetails) describeChain(chain []*x509.Certificate) certs.Info {
	info := certs.DescribeCert(chain[0])
	d.Subject = info.Subject
	d.NotBefore = info.NotBefore.Format(time.RFC3339)
	d.Serial, d.SHA256 = info.Serial, info.SHA256
	d.Chain = chainOf(chain, 1)
	return info
}

// sitesServing returns the sites, enabled or not, that serve the certificate
// of the given name, including raw sites referring to it.
func sitesServing(sites []store.Site, cert string) []SiteRef {
	refs := []SiteRef{}
	for _, site := range sites {
		s, err := config.DecodeServer(site.Config)
		if err != nil || !s.UsesCert(cert) {
			continue
		}
		ref := SiteRef{ID: site.ID, Enabled: s.Enabled(), Raw: s.Raw != nil}
		if names := s.Names(); len(names) > 0 {
			ref.Name = names[0]
		}
		refs = append(refs, ref)
	}
	return refs
}

// AuthorityDetails describes a CA in full.
type AuthorityDetails struct {
	Authority
	NotBefore string       `json:"not_before"` // RFC 3339
	Serial    string       `json:"serial"`     // like CertificateDetails'
	SHA256    string       `json:"sha256"`
	Chain     []ChainEntry `json:"chain"` // the issuers following it, if known
	// Certificates are the names of the certificates kept that it signed.
	Certificates []string `json:"certificates"`
}

// AuthorityDetails describes the CA of the given name in full. If there is
// none, it fails with ErrNoAuthority.
func (m *Manager) AuthorityDetails(ctx context.Context, name string) (AuthorityDetails, error) {
	ca, err := m.authority(ctx, name)
	if err != nil {
		return AuthorityDetails{}, err
	}
	stored, err := m.opts.Store.Certs(ctx)
	if err != nil {
		return AuthorityDetails{}, err
	}
	a, err := describeAuthority(ca, stored)
	if err != nil {
		return AuthorityDetails{}, err
	}
	pair, err := certs.Parse(ca.CertPEM, ca.KeyPEM)
	if err != nil {
		return AuthorityDetails{}, fmt.Errorf("stored CA %s: %w", ca.Name, err)
	}
	info := certs.Describe(pair)
	d := AuthorityDetails{
		Authority:    a,
		NotBefore:    info.NotBefore.Format(time.RFC3339),
		Serial:       info.Serial,
		SHA256:       info.SHA256,
		Chain:        chainOf(pair.Chain, 1),
		Certificates: []string{},
	}
	for _, c := range stored {
		if c.CA == ca.Name {
			d.Certificates = append(d.Certificates, c.Name)
		}
	}
	return d, nil
}

// RenameCertificate renames a certificate kept by the Manager. Sites that
// refer to it, by cert_name or by default, refer to it by its new name
// then, so that they keep serving it; if nginx rejects them, the error is an
// *apply.Error, and nothing changes. Problems with the new name are reported
// as config.FieldErrors for the field "name".
func (m *Manager) RenameCertificate(ctx context.Context, name, newName string) (Certificate, error) {
	newName = strings.TrimSpace(newName)
	c, err := m.storedCert(ctx, named(name))
	if err != nil {
		return Certificate{}, err
	}
	if newName == name {
		return describeCert(c)
	}
	problems, err := m.checkName(ctx, newName, false, false)
	if err != nil {
		return Certificate{}, err
	}
	if len(problems) > 0 {
		return Certificate{}, problems
	}
	err = m.change(ctx, func(st *state) error {
		i := slices.IndexFunc(st.certs, named(name))
		if i < 0 {
			return ErrNoCertificate // deleted or renamed meanwhile
		}
		if slices.ContainsFunc(st.certs, named(newName)) { // added meanwhile
			return certs.FieldError("name", certs.NameInUse, newName)
		}
		for j, site := range st.sites {
			cfg, err := renameCert(site.Config, name, newName)
			if err != nil {
				return fmt.Errorf("site %s: %w", site.ID, err)
			}
			st.sites[j].Config = cfg
		}
		st.certs[i].Name = newName
		return nil
	})
	if err != nil {
		return Certificate{}, err
	}
	if c, err = m.storedCert(ctx, func(s store.Cert) bool { return s.ID == c.ID }); err != nil {
		return Certificate{}, err
	}
	return describeCert(c)
}

func named(name string) func(store.Cert) bool {
	return func(c store.Cert) bool { return c.Name == name }
}

// renameCert returns the configuration of a site, cfg, referring to the
// certificate it refers to as old, by cert_name, by default, or in its raw
// configuration, as new. Other sites are returned as they are.
func renameCert(cfg json.RawMessage, old, new string) (json.RawMessage, error) {
	s, err := config.DecodeServer(cfg)
	if err != nil {
		return cfg, nil // not applied anyway
	}
	if s.Raw != nil {
		raw, changed := renameRefs(*s.Raw, old, new)
		if !changed {
			return cfg, nil
		}
		return setField(cfg, "raw", raw)
	}
	byDefault := s.CertName == "" && s.UseHTTPS() && s.Certificate() == old
	if s.CertName != old && !byDefault {
		return cfg, nil
	}
	return setField(cfg, "cert_name", new)
}

// renameRefs returns the raw configuration text with its references to the
// certificate old, such as {{cert "old"}}, referring to new, and whether
// there were any. Invalid configurations, which are not applied, are left
// alone.
func renameRefs(text, old, new string) (string, bool) {
	info, problem := config.ParseRaw(text)
	if problem != nil {
		return text, false
	}
	var b strings.Builder
	last, changed := 0, false
	for _, ref := range info.Refs {
		if ref.Name != old {
			continue
		}
		b.WriteString(text[last:ref.Start])
		b.WriteString(config.RawCertRef(new, ref.Kind == "key"))
		last, changed = ref.End, true
	}
	b.WriteString(text[last:])
	return b.String(), changed
}

// setField sets the field key of the JSON object obj to value, keeping the
// other fields in order.
func setField(obj json.RawMessage, key string, value any) (json.RawMessage, error) {
	encode := func(v any) ([]byte, error) {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		err := enc.Encode(v)
		return bytes.TrimSuffix(b.Bytes(), []byte("\n")), err
	}
	encoded, err := encode(value)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(obj))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("the configuration is not a JSON object")
	}
	var out bytes.Buffer
	out.WriteByte('{')
	found := false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		if k == key {
			v, found = encoded, true
		}
		if out.Len() > 1 {
			out.WriteByte(',')
		}
		name, err := encode(k)
		if err != nil {
			return nil, err
		}
		out.Write(name)
		out.WriteByte(':')
		out.Write(v)
	}
	if !found {
		if out.Len() > 1 {
			out.WriteByte(',')
		}
		name, _ := encode(key)
		out.Write(name)
		out.WriteByte(':')
		out.Write(encoded)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// RenameAuthority renames a CA. The certificates it signed refer to it by
// its new name then, so that it can renew them. Problems with the new name
// are reported like for RenameCertificate.
func (m *Manager) RenameAuthority(ctx context.Context, name, newName string) (Authority, error) {
	newName = strings.TrimSpace(newName)
	// Changes to the certificates must not put back the old name of their CA
	// meanwhile.
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.authority(ctx, name); err != nil {
		return Authority{}, err
	}
	if newName != name {
		problems, err := m.checkName(ctx, newName, true, false)
		if err != nil {
			return Authority{}, err
		}
		if len(problems) > 0 {
			return Authority{}, problems
		}
		err = m.opts.Store.RenameAuthority(ctx, name, newName)
		switch {
		case errors.Is(err, store.ErrNotFound): // deleted meanwhile
			return Authority{}, ErrNoAuthority
		case errors.Is(err, store.ErrExists): // added meanwhile
			return Authority{}, certs.FieldError("name", certs.NameInUse, newName)
		case err != nil:
			return Authority{}, err
		}
	}
	ca, err := m.authority(ctx, newName)
	if err != nil {
		return Authority{}, err
	}
	stored, err := m.opts.Store.Certs(ctx)
	if err != nil {
		return Authority{}, err
	}
	return describeAuthority(ca, stored)
}
