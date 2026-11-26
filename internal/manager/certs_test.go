package manager_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kiritow/simple-proxy/internal/acme"
	"github.com/kiritow/simple-proxy/internal/apply"
	"github.com/kiritow/simple-proxy/internal/certs"
	"github.com/kiritow/simple-proxy/internal/certs/certstest"
	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/manager"
)

// problemOf returns the field and code of the problem err reports.
func problemOf(err error) (string, string) {
	var fe *config.FieldError
	if !errors.As(err, &fe) {
		return "", ""
	}
	var cp *certs.Problem
	var p *config.Problem
	switch {
	case errors.As(err, &cp):
		return fe.Field, string(cp.Code)
	case errors.As(err, &p):
		return fe.Field, string(p.Code)
	}
	return fe.Field, ""
}

var certPath = regexp.MustCompile(`ssl_certificate\s+"([^"]+/fullchain\.pem)"`)

// servedCert returns the path of the certificate file in the nginx
// configuration.
func (e *env) servedCert(t *testing.T) string {
	t.Helper()
	m := certPath.FindStringSubmatch(e.nginxConf(t))
	if m == nil {
		t.Fatalf("no ssl_certificate in\n%s", e.nginxConf(t))
	}
	return m[1]
}

func (e *env) certDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(e.certDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestUploadedCertificate(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	caCert, caKey := certstest.CA(t, "Test Root CA")
	cert, key := certstest.Leaf(t, caCert, caKey, "app.example.com")

	got, err := e.m.UploadCertificate(ctx, manager.UploadRequest{Cert: cert, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "app.example.com" || got.Source != manager.SourceUploaded || got.Issuer != "Test Root CA" || got.Expires == "" {
		t.Errorf("UploadCertificate = %+v", got)
	}
	if _, err := os.Stat(e.conf); !os.IsNotExist(err) {
		t.Error("nginx was reloaded for an unused certificate")
	}
	list, err := e.m.Certificates(ctx)
	if err != nil || len(list.Certificates) != 2 || list.Certificates[0].Name != "app.example.com" || list.Certificates[1].Source != manager.SourceACME {
		t.Errorf("Certificates = %+v, %v; want the uploaded one and that of acme.sh", list, err)
	}

	// A site named like the certificate uses it.
	if _, err := e.m.CreateSite(ctx, json.RawMessage(`{"server_name": "app.example.com", "https": true, "default": {"port": 3000}}`)); err != nil {
		t.Fatal(err)
	}
	first := e.servedCert(t)
	if !strings.HasPrefix(first, e.certDir) || strings.Contains(e.nginxConf(t), "ssl_stapling") {
		t.Errorf("served %s, want a file in %s, without OCSP stapling", first, e.certDir)
	}
	if b, err := os.ReadFile(first); err != nil || !strings.Contains(string(b), "BEGIN CERTIFICATE") {
		t.Errorf("reading %s: %v", first, err)
	}

	// Uploading again needs replace.
	cert2, key2 := certstest.Leaf(t, caCert, caKey, "app.example.com")
	_, err = e.m.UploadCertificate(ctx, manager.UploadRequest{Cert: cert2, Key: key2})
	if field, code := problemOf(err); field != "name" || code != string(certs.NameInUse) {
		t.Errorf("uploading a duplicate: %v", err)
	}

	// If nginx rejects the new certificate, nothing changes.
	if err := os.WriteFile(e.failFlag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var aerr *apply.Error
	_, err = e.m.UploadCertificate(ctx, manager.UploadRequest{Cert: cert2, Key: key2, Replace: true})
	if !errors.As(err, &aerr) {
		t.Fatalf("replacing while nginx fails: %v, want an *apply.Error", err)
	}
	if e.servedCert(t) != first || len(e.certDirs(t)) != 1 {
		t.Errorf("a rejected replacement changed the files: %v", e.certDirs(t))
	}
	stored, err := e.store.Certs(ctx)
	if err != nil || len(stored) != 1 || !certstest.Parse(t, string(stored[0].CertPEM), string(stored[0].KeyPEM)).Leaf().Equal(certstest.Parse(t, cert, key).Leaf()) {
		t.Errorf("a rejected replacement was stored (%v)", err)
	}
	os.Remove(e.failFlag)

	// Replacing it serves the new files, and removes the old ones.
	if _, err := e.m.UploadCertificate(ctx, manager.UploadRequest{Name: "app.example.com", Cert: cert2, Key: key2, Replace: true}); err != nil {
		t.Fatal(err)
	}
	second := e.servedCert(t)
	if second == first {
		t.Error("the configuration still serves the old certificate")
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("the old certificate was kept: %v", err)
	}

	// Sites using it keep it.
	err = e.m.DeleteCertificate(ctx, "app.example.com")
	if field, code := problemOf(err); field != "name" || code != string(certs.CertInUse) || !strings.Contains(err.Error(), "used by app.example.com") {
		t.Errorf("deleting a used certificate: %v", err)
	}
	if err := e.m.DeleteCertificate(ctx, "nope"); !errors.Is(err, manager.ErrNoCertificate) {
		t.Errorf("deleting a missing certificate: %v", err)
	}
	sites := e.stored(t)
	if err := e.m.DeleteSite(ctx, sites[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.m.DeleteCertificate(ctx, "app.example.com"); err != nil {
		t.Fatal(err)
	}
	if dirs := e.certDirs(t); len(dirs) != 0 {
		t.Errorf("files left behind: %v", dirs)
	}
}

func TestCertificateNames(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	caCert, caKey := certstest.CA(t, "Test Root CA")
	cert, key := certstest.Leaf(t, caCert, caKey, "*.example.com")

	// acme.sh has a certificate of that name.
	_, err := e.m.UploadCertificate(ctx, manager.UploadRequest{Cert: cert, Key: key})
	if field, code := problemOf(err); field != "name" || code != string(certs.NameInUse) {
		t.Errorf("shadowing acme.sh: %v", err)
	}
	_, err = e.m.UploadCertificate(ctx, manager.UploadRequest{Name: "line\nbreak", Cert: cert, Key: key})
	if field, code := problemOf(err); field != "name" || code != string(certs.InvalidCertName) {
		t.Errorf("invalid name: %v", err)
	}
	// Names describe certificates; they never reach nginx's configuration.
	const wild = `Wildcard {example.com}; "2026" / 商店`
	if _, err := e.m.UploadCertificate(ctx, manager.UploadRequest{Name: " " + wild + " ", Cert: cert, Key: key}); err != nil {
		t.Fatal(err)
	}
	// acme.sh cannot issue one hidden by it.
	if _, err := e.m.IssueCertificate(ctx, []string{wild}, "dns_cf"); !errors.Is(err, acme.ErrInvalidRequest) {
		t.Errorf("issuing a hidden certificate: %v", err)
	}
	// Sites find uploaded certificates before those of acme.sh, and both.
	site, _ := json.Marshal(map[string]any{"server_name": "a.example.com", "cert_name": wild, "default": map[string]int{"port": 1}})
	if _, err := e.m.CreateSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.CreateSite(ctx, json.RawMessage(`{"server_name": "b.example.com", "cert_name": "*.example.com", "default": {"port": 1}}`)); err != nil {
		t.Fatal(err)
	}
	conf := e.nginxConf(t)
	if !strings.Contains(conf, `ssl_certificate "/c/full.cer"`) || !strings.Contains(conf, `ssl_certificate "`+e.certDir) {
		t.Errorf("want certificates of both sources in\n%s", conf)
	}
	if strings.Contains(conf, "Wildcard") || !regexp.MustCompile(`/[0-9a-f]{32}/fullchain\.pem"`).MatchString(conf) {
		t.Errorf("want files named after hashes, not the certificate, in\n%s", conf)
	}
}

func TestPreviews(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	rootCert, rootKey := certstest.CA(t, "Home Root CA") // valid for a year
	cert, key := certstest.Leaf(t, rootCert, rootKey, "app.example.com", "10.0.0.1")
	codes := func(problems config.FieldErrors) []string {
		var out []string
		for _, fe := range problems {
			field, code := problemOf(fe)
			out = append(out, field+": "+code)
		}
		return out
	}

	// While the key is missing, the certificate is described.
	p, problems, err := e.m.PreviewUpload(ctx, manager.UploadRequest{Cert: rootCert + cert}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "app.example.com" || p.Issuer != "Home Root CA" || strings.Join(p.Domains, " ") != "app.example.com 10.0.0.1" ||
		p.NotAfter == "" || p.HasKey || len(p.Chain) != 1 || !p.Chain[0].Root || p.Chain[0].Subject != "Home Root CA" {
		t.Errorf("PreviewUpload = %+v", p)
	}
	if got := codes(problems); strings.Join(got, ", ") != "key: no_key" {
		t.Errorf("problems %v, want the missing key", got)
	}
	if p, problems, _ = e.m.PreviewUpload(ctx, manager.UploadRequest{Name: "App", Cert: cert, Key: key}, false); !p.HasKey || p.Name != "App" || len(problems) > 0 {
		t.Errorf("complete upload: %+v, %v", p, problems)
	}
	// A leaf is no CA; nothing is stored.
	if _, problems, _ = e.m.PreviewUpload(ctx, manager.UploadRequest{Cert: cert, Key: key}, true); strings.Join(codes(problems), ", ") != "cert: not_ca" {
		t.Errorf("a leaf as CA: %v", codes(problems))
	}
	if list, _ := e.m.Certificates(ctx); len(list.Certificates) != 1 { // that of acme.sh
		t.Errorf("previews stored certificates: %+v", list)
	}

	if _, err := e.m.AddAuthority(ctx, manager.UploadRequest{Cert: rootCert, Key: rootKey}); err != nil {
		t.Fatal(err)
	}
	if _, problems, _ = e.m.PreviewUpload(ctx, manager.UploadRequest{Cert: rootCert, Key: rootKey}, true); strings.Join(codes(problems), ", ") != "name: name_in_use" {
		t.Errorf("a CA of the same name: %v", codes(problems))
	}

	// Signing previews report every problem at once, and what they can.
	req := manager.SignRequest{Authority: "Home Root CA", Domains: []string{"NAS.lan", "a b"}, Days: 400, KeyType: certs.KeyRSA}
	p, problems, err = e.m.PreviewSign(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := codes(problems); strings.Join(got, ", ") != "domains: invalid_domain, days: beyond_ca" {
		t.Errorf("problems %v", got)
	}
	if p.Name != "nas.lan" || p.Subject != "nas.lan" || p.Issuer != "Home Root CA" || p.Key != "RSA 2048" || p.NotAfter == "" || !p.HasKey || len(p.Chain) != 1 {
		t.Errorf("PreviewSign = %+v", p)
	}
	if _, problems, _ = e.m.PreviewSign(ctx, manager.SignRequest{Domains: []string{"x.lan"}, Days: 1, KeyType: certs.KeyEC}); strings.Join(codes(problems), ", ") != "authority: required" {
		t.Errorf("without a CA: %v", codes(problems))
	}
	req = manager.SignRequest{Authority: "Home Root CA", Domains: []string{"nas.lan"}, Days: 30, KeyType: certs.KeyEC}
	if _, err := e.m.SignCertificate(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, problems, _ = e.m.PreviewSign(ctx, req); strings.Join(codes(problems), ", ") != "name: name_in_use" {
		t.Errorf("signing a duplicate: %v", codes(problems))
	}
	req.Replace = true
	if _, problems, _ = e.m.PreviewSign(ctx, req); len(problems) > 0 {
		t.Errorf("renewing: %v", codes(problems))
	}
}

func TestSignedCertificates(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	caCert, caKey := certstest.CA(t, "Home Root CA")

	ca, err := e.m.AddAuthority(ctx, manager.UploadRequest{Cert: caCert, Key: caKey})
	if err != nil {
		t.Fatal(err)
	}
	if ca.Name != "Home Root CA" || ca.Subject != "Home Root CA" || ca.Key != "EC P-256" {
		t.Errorf("AddAuthority = %+v", ca)
	}
	_, err = e.m.AddAuthority(ctx, manager.UploadRequest{Cert: caCert, Key: caKey})
	if field, code := problemOf(err); field != "name" || code != string(certs.NameInUse) {
		t.Errorf("adding it again: %v", err)
	}
	leafCert, leafKey := certstest.Leaf(t, caCert, caKey, "a.example.com")
	_, err = e.m.AddAuthority(ctx, manager.UploadRequest{Name: "Leaf", Cert: leafCert, Key: leafKey})
	if field, code := problemOf(err); field != "cert" || code != string(certs.NotCA) {
		t.Errorf("adding a leaf as CA: %v", err)
	}

	req := manager.SignRequest{Authority: "Home Root CA", Domains: []string{"nas.lan", "10.0.0.2"}, Days: 397, KeyType: certs.KeyRSA}
	_, err = e.m.SignCertificate(ctx, req)
	if field, code := problemOf(err); field != "days" || code != string(certs.BeyondCA) {
		t.Errorf("signing beyond the CA: %v", err)
	}
	req.Days = 90
	signed, err := e.m.SignCertificate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Name != "nas.lan" || signed.Source != manager.SourceSigned || signed.CA != "Home Root CA" ||
		signed.KeyType != certs.KeyRSA || signed.Days != 90 || signed.Key != "RSA 2048" || strings.Join(signed.Domains, " ") != "nas.lan 10.0.0.2" {
		t.Errorf("SignCertificate = %+v", signed)
	}
	if _, err := e.m.SignCertificate(ctx, req); err == nil {
		t.Error("signing a duplicate succeeded")
	}
	req.Replace = true // renewal
	if _, err := e.m.SignCertificate(ctx, req); err != nil {
		t.Fatal(err)
	}
	_, err = e.m.SignCertificate(ctx, manager.SignRequest{Authority: "nope", Domains: []string{"x.lan"}, Days: 1, KeyType: certs.KeyEC})
	if field, code := problemOf(err); field != "authority" || code != string(certs.CANotFound) {
		t.Errorf("signing with a missing CA: %v", err)
	}

	cas, err := e.m.Authorities(ctx)
	if err != nil || len(cas) != 1 || cas[0].Signed != 1 {
		t.Errorf("Authorities = %+v, %v; want one CA that signed one certificate", cas, err)
	}
	pem, err := e.m.AuthorityCert(ctx, "Home Root CA")
	if err != nil || !strings.Contains(string(pem), "BEGIN CERTIFICATE") || strings.Contains(string(pem), "PRIVATE") {
		t.Errorf("AuthorityCert = %s, %v", pem, err)
	}

	// The certificate stays when its CA goes.
	if err := e.m.DeleteAuthority(ctx, "Home Root CA"); err != nil {
		t.Fatal(err)
	}
	if err := e.m.DeleteAuthority(ctx, "Home Root CA"); !errors.Is(err, manager.ErrNoAuthority) {
		t.Errorf("deleting it again: %v", err)
	}
	if list, _ := e.m.Certificates(ctx); list.Certificates[0].Name != "nas.lan" {
		t.Errorf("Certificates = %+v", list)
	}
}

// TestStartupRewritesFiles: if the files of certificates are lost, applying
// the stored sites again writes them.
func TestStartupRewritesFiles(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	caCert, caKey := certstest.CA(t, "Test Root CA")
	cert, key := certstest.Leaf(t, caCert, caKey, "app.example.com")
	if _, err := e.m.UploadCertificate(ctx, manager.UploadRequest{Cert: cert, Key: key}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.CreateSite(ctx, json.RawMessage(`{"server_name": "app.example.com", "https": true, "default": {"port": 3000}}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(e.certDir); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(e.servedCert(t))); err != nil {
		t.Error(err)
	}
}

func TestRawSites(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	caCert, caKey := certstest.CA(t, "Root")
	cert, key := certstest.Leaf(t, caCert, caKey, "*.example.com")
	if _, err := e.m.UploadCertificate(ctx, manager.UploadRequest{Name: "Wildcard", Cert: cert, Key: key}); err != nil {
		t.Fatal(err)
	}
	b, err := e.m.CreateSite(ctx, site("b.example.com", 3000))
	if err != nil {
		t.Fatal(err)
	}

	// Raw sites take part in domain checks, by their server names.
	raw := func(text string) json.RawMessage {
		cfg, _ := json.Marshal(map[string]string{"raw": text})
		return cfg
	}
	const text = `server {
  listen 443 ssl;
  server_name a.example.com;
  ssl_certificate     {{cert "Wildcard"}};
  ssl_certificate_key {{key "Wildcard"}};
  location / { proxy_pass http://127.0.0.1:3000; }
}
`
	_, err = e.m.CreateSite(ctx, raw(strings.Replace(text, "a.example.com", "B.example.com", 1)))
	if got := fields(err); len(got) != 1 || got[0] != `servers[0].raw: "B.example.com" is used by another site` {
		t.Errorf("a raw site with a domain in use: %v", err)
	}
	a, err := e.m.CreateSite(ctx, raw(text))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.nginxConf(t), `ssl_certificate     "`+e.servedCert(t)+`";`) {
		t.Errorf("the reference was not replaced by the path:\n%s", e.nginxConf(t))
	}
	if _, err := e.m.UpdateSite(ctx, b.ID, 0, site("a.example.com", 3000)); len(fields(err)) != 1 {
		t.Errorf("a builder site with a domain of a raw site: %v", err)
	}

	// Its certificate is in use, and references are checked.
	if err := e.m.DeleteCertificate(ctx, "Wildcard"); !strings.Contains(fmt.Sprint(err), "is used by a.example.com") {
		t.Errorf("deleting a certificate used by a raw site: %v", err)
	}
	_, err = e.m.UpdateSite(ctx, a.ID, 0, raw(`ssl_certificate {{cert "nope"}};`))
	if got := fields(err); len(got) != 1 || !strings.HasPrefix(got[0], "servers[0].raw: certificate not found") {
		t.Errorf("a reference to a missing certificate: %v", err)
	}

	// Builder sites convert to raw configurations, which generate the same.
	converted, err := e.m.RawConfig(ctx, json.RawMessage(`{"server_name": "c.example.com", "https": true, "cert_name": "Wildcard", "enable": false, "default": {"port": 4000}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(converted, `ssl_certificate {{cert "Wildcard"}};`) || !strings.Contains(converted, "proxy_pass http://127.0.0.1:4000;") {
		t.Errorf("RawConfig = %s", converted)
	}
	if _, err := e.m.CreateSite(ctx, raw(converted)); err != nil {
		t.Errorf("storing the converted site: %v", err)
	}
}
