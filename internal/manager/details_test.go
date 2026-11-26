package manager_test

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kiritow/simple-proxy/internal/apply"
	"github.com/kiritow/simple-proxy/internal/certs"
	"github.com/kiritow/simple-proxy/internal/certs/certstest"
	"github.com/kiritow/simple-proxy/internal/manager"
)

var (
	uuidV4   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	sha256Re = regexp.MustCompile(`^[0-9A-F]{2}(:[0-9A-F]{2}){31}$`)
)

// configs returns the configurations of the stored sites, by first server
// name, and their generations.
func (e *env) configs(t *testing.T) (map[string]string, map[string]int64) {
	t.Helper()
	cfgs, gens := map[string]string{}, map[string]int64{}
	for _, s := range e.stored(t) {
		var v struct {
			ServerName string `json:"server_name"`
		}
		if err := json.Unmarshal(s.Config, &v); err != nil {
			t.Fatal(err)
		}
		cfgs[v.ServerName], gens[v.ServerName] = string(s.Config), s.Generation
	}
	return cfgs, gens
}

func TestRenameCertificate(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	caCert, caKey := certstest.CA(t, "Test Root CA")
	cert, key := certstest.Leaf(t, caCert, caKey, "app.example.com")
	uploaded, err := e.m.UploadCertificate(ctx, manager.UploadRequest{Cert: cert, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if !uuidV4.MatchString(uploaded.ID) {
		t.Errorf("uploaded with ID %q, want a UUID", uploaded.ID)
	}
	for _, cfg := range []string{
		// By default, by cert_name, disabled, and using another certificate.
		`{"server_name": "app.example.com", "https": true, "default": {"port": 3000}}`,
		`{"server_name": "b.example.com", "cert_name": "app.example.com", "default": {"port": 3001}}`,
		`{"server_name": "c.example.com", "enable": false, "cert_name": "app.example.com", "gzip": true, "default": {"port": 3002}}`,
		`{"server_name": "d.example.com", "cert_name": "*.example.com", "default": {"port": 3003}}`,
	} {
		if _, err := e.m.CreateSite(ctx, json.RawMessage(cfg)); err != nil {
			t.Fatal(err)
		}
	}
	served := e.servedCert(t)

	for _, c := range []struct{ name, newName, code string }{
		{"app.example.com", "*.example.com", string(certs.NameInUse)}, // acme.sh's
		{"app.example.com", " ", "required"},
		{"app.example.com", "line\nbreak", string(certs.InvalidCertName)},
	} {
		_, err := e.m.RenameCertificate(ctx, c.name, c.newName)
		if field, code := problemOf(err); field != "name" || code != c.code {
			t.Errorf("RenameCertificate(%q, %q) = %v, want %s", c.name, c.newName, err, c.code)
		}
	}
	if _, err := e.m.RenameCertificate(ctx, "nope", "Nope"); !errors.Is(err, manager.ErrNoCertificate) {
		t.Errorf("renaming a missing certificate: %v", err)
	}
	if _, err := e.m.RenameCertificate(ctx, "*.example.com", "Wildcard"); !errors.Is(err, manager.ErrNoCertificate) {
		t.Errorf("renaming a certificate of acme.sh: %v", err)
	}

	// If nginx rejects the sites, nothing changes.
	before, _ := e.configs(t)
	if err := os.WriteFile(e.failFlag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var aerr *apply.Error
	if _, err := e.m.RenameCertificate(ctx, "app.example.com", "App"); !errors.As(err, &aerr) {
		t.Fatalf("renaming while nginx fails: %v, want an *apply.Error", err)
	}
	os.Remove(e.failFlag)
	if after, _ := e.configs(t); !maps.Equal(before, after) {
		t.Errorf("a rejected rename changed the sites: %v", after)
	}
	if list, _ := e.m.Certificates(ctx); list.Certificates[0].Name != "app.example.com" {
		t.Errorf("a rejected rename renamed the certificate: %+v", list.Certificates[0])
	}

	const name = "App – 2026"
	renamed, err := e.m.RenameCertificate(ctx, "app.example.com", " "+name+" ")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != name || renamed.ID != uploaded.ID || renamed.Created != uploaded.Created {
		t.Errorf("RenameCertificate = %+v, want %+v renamed", renamed, uploaded)
	}
	cfgs, gens := e.configs(t)
	want := map[string]string{
		"app.example.com": `{"server_name":"app.example.com","https":true,"default":{"port":3000},"cert_name":"App – 2026"}`,
		"b.example.com":   `{"server_name":"b.example.com","cert_name":"App – 2026","default":{"port":3001}}`,
		"c.example.com":   `{"server_name":"c.example.com","enable":false,"cert_name":"App – 2026","gzip":true,"default":{"port":3002}}`,
		"d.example.com":   `{"server_name":"d.example.com","cert_name":"*.example.com","default":{"port":3003}}`,
	}
	if !maps.Equal(cfgs, want) || gens["d.example.com"] != 1 || gens["b.example.com"] != 2 {
		t.Errorf("sites = %v, generations %v; want them to refer to %s", cfgs, gens, name)
	}
	if got := e.servedCert(t); got != served {
		t.Errorf("serving %s after renaming, want %s", got, served)
	}
	err = e.m.DeleteCertificate(ctx, name)
	if field, code := problemOf(err); field != "name" || code != string(certs.CertInUse) {
		t.Errorf("deleting the renamed certificate: %v, want it in use", err)
	}
	// The old name is free again, e.g. for acme.sh.
	if _, err := e.m.IssueCertificate(ctx, []string{"app.example.com"}, "dns_cf"); err != nil {
		t.Errorf("issuing for the old name: %v", err)
	}
	if same, err := e.m.RenameCertificate(ctx, name, name); err != nil || same.Name != name {
		t.Errorf("renaming to the same name = %+v, %v", same, err)
	}
	// Replacing it keeps its ID.
	cert2, key2 := certstest.Leaf(t, caCert, caKey, "app.example.com")
	replaced, err := e.m.UploadCertificate(ctx, manager.UploadRequest{Name: name, Cert: cert2, Key: key2, Replace: true})
	if err != nil || replaced.ID != uploaded.ID {
		t.Errorf("replacing it = %+v, %v; want ID %s", replaced, err, uploaded.ID)
	}
}

func TestCertificateDetails(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	caCert, caKey := certstest.CA(t, "Test Root CA")
	cert, key := certstest.Leaf(t, caCert, caKey, "app.example.com", "10.0.0.1")
	if _, err := e.m.UploadCertificate(ctx, manager.UploadRequest{Name: "App", Cert: cert, Key: key}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.CreateSite(ctx, json.RawMessage(`{"server_name": "app.example.com www.example.com", "cert_name": "App", "default": {"port": 1}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.CreateSite(ctx, json.RawMessage(`{"server_name": "old.example.com", "enable": false, "cert_name": "App", "default": {"port": 1}}`)); err != nil {
		t.Fatal(err)
	}

	d, err := e.m.CertificateDetails(ctx, "App")
	if err != nil {
		t.Fatal(err)
	}
	info := certs.Describe(certstest.Parse(t, cert, key))
	if d.Source != manager.SourceUploaded || !uuidV4.MatchString(d.ID) || d.Subject != "app.example.com" || d.Issuer != "Test Root CA" ||
		d.Serial != info.Serial || !sha256Re.MatchString(d.SHA256) || d.NotBefore == "" || d.Expires == "" || d.Updated == "" ||
		strings.Join(d.Domains, " ") != "app.example.com 10.0.0.1" || len(d.Chain) != 0 {
		t.Errorf("CertificateDetails = %+v", d)
	}
	if !strings.HasPrefix(d.CertFile, e.certDir) || d.CertFile != e.servedCert(t) || !strings.HasSuffix(d.KeyFile, "privkey.pem") {
		t.Errorf("files %s and %s, want those served", d.CertFile, d.KeyFile)
	}
	want := []manager.SiteRef{{Name: "app.example.com", Enabled: true}, {Name: "old.example.com"}}
	for i := range d.Sites {
		d.Sites[i].ID = ""
	}
	if !slices.Equal(d.Sites, want) {
		t.Errorf("sites = %+v, want %+v", d.Sites, want)
	}

	// Of acme.sh: only what it lists is known if its files cannot be read.
	acmeDetails, err := e.m.CertificateDetails(ctx, "*.example.com")
	if err != nil || acmeDetails.Source != manager.SourceACME || acmeDetails.ID != "" || acmeDetails.CertFile != "/c/full.cer" || acmeDetails.Problem == "" {
		t.Errorf("CertificateDetails of acme.sh = %+v, %v", acmeDetails, err)
	}
	if _, err := e.m.CertificateDetails(ctx, "nope"); !errors.Is(err, manager.ErrNoCertificate) {
		t.Errorf("details of a missing certificate: %v", err)
	}
}

func TestAuthorityDetailsAndRename(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	caCert, caKey := certstest.CA(t, "Home Root CA")
	ca, err := e.m.AddAuthority(ctx, manager.UploadRequest{Cert: caCert, Key: caKey})
	if err != nil {
		t.Fatal(err)
	}
	if !uuidV4.MatchString(ca.ID) {
		t.Errorf("added CA with ID %q, want a UUID", ca.ID)
	}
	req := manager.SignRequest{Authority: ca.Name, Domains: []string{"nas.lan"}, Days: 30, KeyType: certs.KeyEC}
	if _, err := e.m.SignCertificate(ctx, req); err != nil {
		t.Fatal(err)
	}
	otherCert, otherKey := certstest.CA(t, "Other CA")
	if _, err := e.m.AddAuthority(ctx, manager.UploadRequest{Cert: otherCert, Key: otherKey}); err != nil {
		t.Fatal(err)
	}

	d, err := e.m.AuthorityDetails(ctx, ca.Name)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != ca.ID || d.Subject != "Home Root CA" || !sha256Re.MatchString(d.SHA256) || d.Serial == "" || d.NotBefore == "" ||
		len(d.Chain) != 0 || !slices.Equal(d.Certificates, []string{"nas.lan"}) {
		t.Errorf("AuthorityDetails = %+v", d)
	}
	if _, err := e.m.AuthorityDetails(ctx, "nope"); !errors.Is(err, manager.ErrNoAuthority) {
		t.Errorf("details of a missing CA: %v", err)
	}

	_, err = e.m.RenameAuthority(ctx, ca.Name, "Other CA")
	if field, code := problemOf(err); field != "name" || code != string(certs.NameInUse) {
		t.Errorf("renaming to a taken name: %v", err)
	}
	if _, err := e.m.RenameAuthority(ctx, "nope", "Nope"); !errors.Is(err, manager.ErrNoAuthority) {
		t.Errorf("renaming a missing CA: %v", err)
	}
	renamed, err := e.m.RenameAuthority(ctx, ca.Name, " Home CA 2026 ")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "Home CA 2026" || renamed.ID != ca.ID || renamed.Signed != 1 {
		t.Errorf("RenameAuthority = %+v", renamed)
	}
	// The certificate it signed follows it, and can be renewed.
	list, err := e.m.Certificates(ctx)
	if err != nil || list.Certificates[0].CA != "Home CA 2026" {
		t.Errorf("Certificates = %+v, %v; want nas.lan signed by Home CA 2026", list, err)
	}
	req.Authority, req.Replace = "Home CA 2026", true
	if _, err := e.m.SignCertificate(ctx, req); err != nil {
		t.Errorf("renewing: %v", err)
	}
	stored, _ := e.store.Certs(ctx)
	if len(stored) != 1 || stored[0].CA != "Home CA 2026" {
		t.Errorf("stored %+v, want the renewed certificate signed by Home CA 2026", stored)
	}
}

// TestRenameCertificateInRawSites: raw sites refer to certificates in their
// raw configuration, whose references follow renamed certificates.
func TestRenameCertificateInRawSites(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	caCert, caKey := certstest.CA(t, "Root")
	cert, key := certstest.Leaf(t, caCert, caKey, "*.example.com")
	if _, err := e.m.UploadCertificate(ctx, manager.UploadRequest{Name: "Wildcard", Cert: cert, Key: key}); err != nil {
		t.Fatal(err)
	}
	cert2, key2 := certstest.Leaf(t, caCert, caKey, "other.example.org")
	if _, err := e.m.UploadCertificate(ctx, manager.UploadRequest{Name: "Other", Cert: cert2, Key: key2}); err != nil {
		t.Fatal(err)
	}
	const text = `server {
  listen 443 ssl;
  server_name a.example.com;
  ssl_certificate     {{cert "Wildcard"}};
  ssl_certificate_key {{ key "Wildcard" }};
  location / { proxy_pass http://127.0.0.1:3000; }
}
server {
  listen 443 ssl;
  server_name other.example.org;
  ssl_certificate     {{cert "Other"}};
  ssl_certificate_key {{key "Other"}};
}
`
	cfg, _ := json.Marshal(map[string]any{"enable": true, "raw": text})
	if _, err := e.m.CreateSite(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	d, err := e.m.CertificateDetails(ctx, "Wildcard")
	if err != nil || len(d.Sites) != 1 || d.Sites[0].Name != "a.example.com" || !d.Sites[0].Raw {
		t.Errorf("sites of the certificate = %+v, %v; want the raw site", d.Sites, err)
	}
	served := e.servedCert(t)

	const name = `Wildcard "2026" \ 商店`
	if _, err := e.m.RenameCertificate(ctx, "Wildcard", name); err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Enable bool   `json:"enable"`
		Raw    string `json:"raw"`
	}
	if err := json.Unmarshal(e.stored(t)[0].Config, &stored); err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`{{cert "Wildcard"}}`, `{{cert "Wildcard \"2026\" \\ 商店"}}`,
		`{{ key "Wildcard" }}`, `{{key "Wildcard \"2026\" \\ 商店"}}`,
	).Replace(text)
	if stored.Raw != want || !stored.Enable {
		t.Errorf("raw configuration after renaming:\n%s\nwant\n%s", stored.Raw, want)
	}
	if got := e.servedCert(t); got != served {
		t.Errorf("serving %s after renaming, want %s", got, served)
	}
	if d, err := e.m.CertificateDetails(ctx, name); err != nil || len(d.Sites) != 1 {
		t.Errorf("sites of the renamed certificate = %+v, %v", d.Sites, err)
	}
}
