package certs_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kiritow/simple-proxy/internal/certs"
	"github.com/kiritow/simple-proxy/internal/certs/certstest"
	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/nginx"
)

// problem returns the field and code of the problem err reports.
func problem(err error) (string, certs.ProblemCode) {
	var fe *config.FieldError
	var p *certs.Problem
	if !errors.As(err, &fe) || !errors.As(err, &p) {
		return "", ""
	}
	return fe.Field, p.Code
}

func TestParse(t *testing.T) {
	caCert, caKey := certstest.CA(t, "Test Root CA")
	cert, key := certstest.Leaf(t, caCert, caKey, "example.com", "www.example.com")
	_, otherKey := certstest.Leaf(t, caCert, caKey, "other.example.com")

	for _, tc := range []struct {
		name      string
		cert, key string
	}{
		{"separate", cert, key},
		{"combined", cert + key, ""},
		{"key first", key + cert, ""},
		{"swapped", key, cert},
		{"chain in any order, with text", "Bag Attributes\n" + caCert + cert, key},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := certs.Parse([]byte(tc.cert), []byte(tc.key))
			if err != nil {
				t.Fatal(err)
			}
			info := certs.Describe(p)
			if !slices.Equal(info.Domains, []string{"example.com", "www.example.com"}) || info.Issuer != "Test Root CA" || info.Key != "EC P-256" {
				t.Errorf("Describe = %+v", info)
			}
			if certs.DefaultName(p) != "example.com" {
				t.Errorf("DefaultName = %q", certs.DefaultName(p))
			}
			// The output parses back to the same.
			again, err := certs.Parse(p.CertPEM(), p.KeyPEM())
			if err != nil || !again.Leaf().Equal(p.Leaf()) || len(again.Chain) != len(p.Chain) {
				t.Errorf("parsing the output: %v", err)
			}
		})
	}

	encrypted := string(pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte{1}}))
	legacyEncrypted := string(pem.EncodeToMemory(&pem.Block{
		Type: "EC PRIVATE KEY", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-128-CBC,00"}, Bytes: []byte{1},
	}))
	for _, tc := range []struct {
		name      string
		cert, key string
		field     string
		code      certs.ProblemCode
	}{
		{"empty", "", "", "cert", certs.NoCertificate},
		{"no key", cert, "", "key", certs.NoKey},
		{"other key", cert, otherKey, "key", certs.KeyMismatch},
		{"encrypted", cert, encrypted, "key", certs.EncryptedKey},
		{"legacy encrypted", cert, legacyEncrypted, "key", certs.EncryptedKey},
		{"garbage key", cert, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}})), "key", certs.InvalidKey},
		{"garbage cert", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}})), key, "cert", certs.InvalidCertificate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := certs.Parse([]byte(tc.cert), []byte(tc.key))
			if field, code := problem(err); field != tc.field || code != tc.code {
				t.Errorf("Parse error = %v, want %s on %s", err, tc.code, tc.field)
			}
		})
	}
}

// TestParseOrdersChains: uploads often have their chain in the wrong order,
// with the root, or with unrelated certificates.
func TestParseOrdersChains(t *testing.T) {
	rootCert, rootKey := certstest.CA(t, "Root")
	interCert, interKey := certstest.Issue(t, &x509.Certificate{
		Subject:               pkix.Name{CommonName: "Intermediate"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(0, 6, 0),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}, rootCert, rootKey)
	leafCert, leafKey := certstest.Leaf(t, interCert, interKey, "example.com")
	otherCert, _ := certstest.CA(t, "Unrelated")

	p, err := certs.Parse([]byte(otherCert+rootCert+interCert+leafCert), []byte(leafKey))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range p.Chain {
		names = append(names, c.Subject.CommonName)
	}
	if strings.Join(names, ", ") != "example.com, Intermediate, Root" {
		t.Errorf("chain %v, want the leaf, the intermediate and the root", names)
	}
	if served := p.WithoutRoot(); len(served.Chain) != 2 {
		t.Errorf("WithoutRoot kept %d certificates, want 2", len(served.Chain))
	}
	root := certstest.Parse(t, rootCert, rootKey)
	if root.WithoutRoot() != root {
		t.Error("WithoutRoot dropped a lone root")
	}
}

// TestParseKeyFormats parses keys in the formats openssl writes.
func TestParseKeyFormats(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		block *pem.Block
		pub   any
		want  string
	}{
		{&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)}, rsaKey.Public(), "RSA 2048"},
		{&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecDER}, ecKey.Public(), "EC P-384"},
	} {
		tmpl := &x509.Certificate{
			SerialNumber: big1, Subject: pkix.Name{CommonName: "example.com"},
			NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour),
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, tc.pub, signerOf(tc.block, rsaKey, ecKey))
		if err != nil {
			t.Fatal(err)
		}
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		p, err := certs.Parse(certPEM, pem.EncodeToMemory(tc.block))
		if err != nil {
			t.Fatalf("%s: %v", tc.block.Type, err)
		}
		if got := certs.Describe(p); got.Key != tc.want || !slices.Equal(got.Domains, []string{"example.com"}) {
			t.Errorf("%s: Describe = %+v, want key %s and the common name as domain", tc.block.Type, got, tc.want)
		}
	}
}

func signerOf(b *pem.Block, rsaKey *rsa.PrivateKey, ecKey *ecdsa.PrivateKey) any {
	if b.Type == "RSA PRIVATE KEY" {
		return rsaKey
	}
	return ecKey
}

var big1 = new(big.Int).SetInt64(1)

func leafPair(t *testing.T, caCert, caKey string, domains ...string) *certs.Pair {
	t.Helper()
	cert, key := certstest.Leaf(t, caCert, caKey, domains...)
	return certstest.Parse(t, cert, key)
}

func TestCheck(t *testing.T) {
	now := time.Now()
	caCert, caKey := certstest.CA(t, "Test Root CA")
	ca := certstest.Parse(t, caCert, caKey)
	leaf := leafPair(t, caCert, caKey, "example.com")

	if err := certs.CheckCA(ca, now); err != nil {
		t.Errorf("CheckCA(CA) = %v", err)
	}
	if _, code := problem(certs.CheckCA(leaf, now)); code != certs.NotCA {
		t.Errorf("CheckCA(leaf) = %v, want not_ca", certs.CheckCA(leaf, now))
	}
	if err := certs.CheckCertificate(leaf, now); err != nil {
		t.Errorf("CheckCertificate = %v", err)
	}
	later := now.AddDate(2, 0, 0)
	if _, code := problem(certs.CheckCertificate(leaf, later)); code != certs.Expired {
		t.Errorf("CheckCertificate(expired) = %v", certs.CheckCertificate(leaf, later))
	}
	if _, code := problem(certs.CheckCA(ca, later)); code != certs.Expired {
		t.Errorf("CheckCA(expired) = %v", certs.CheckCA(ca, later))
	}
}

func TestSign(t *testing.T) {
	now := time.Now()
	rootCert, rootKey := certstest.CA(t, "Test Root CA")
	root := certstest.Parse(t, rootCert, rootKey)
	roots := x509.NewCertPool()
	roots.AddCert(root.Leaf())

	// An intermediate CA, uploaded with its chain.
	interCert, interKey := certstest.Issue(t, &x509.Certificate{
		Subject:               pkix.Name{CommonName: "Test Intermediate CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(0, 6, 0),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}, rootCert, rootKey)
	inter := certstest.Parse(t, interCert+rootCert, interKey)

	for _, tc := range []struct {
		name    string
		ca      *certs.Pair
		keyType string
		chain   int
	}{
		{"root, EC", root, certs.KeyEC, 1},
		{"intermediate, RSA", inter, certs.KeyRSA, 2}, // without the root
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := certs.SignRequest{Domains: []string{"App.Example.com", "*.example.com", "10.0.0.1", "app.example.com"}, Days: 90, KeyType: tc.keyType}
			p, err := certs.Sign(tc.ca, req, now)
			if err != nil {
				t.Fatal(err)
			}
			leaf := p.Leaf()
			if len(p.Chain) != tc.chain {
				t.Errorf("chain of %d certificates, want %d", len(p.Chain), tc.chain)
			}
			inters := x509.NewCertPool()
			for _, c := range p.Chain[1:] {
				inters.AddCert(c)
			}
			for _, name := range []string{"app.example.com", "x.example.com", "10.0.0.1"} {
				if _, err := leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: roots, Intermediates: inters}); err != nil {
					t.Errorf("verifying for %s: %v", name, err)
				}
			}
			if !slices.Equal(leaf.DNSNames, []string{"app.example.com", "*.example.com"}) || len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("10.0.0.1")) {
				t.Errorf("names %v %v", leaf.DNSNames, leaf.IPAddresses)
			}
			if leaf.Subject.CommonName != "app.example.com" || leaf.IsCA {
				t.Errorf("subject %v, CA %t", leaf.Subject, leaf.IsCA)
			}
			if days := leaf.NotAfter.Sub(now).Hours() / 24; days < 89.9 || days > 90.1 {
				t.Errorf("valid for %.1f days, want 90", days)
			}
			if _, isRSA := p.Key.(*rsa.PrivateKey); isRSA != (tc.keyType == certs.KeyRSA) {
				t.Errorf("key %T for type %s", p.Key, tc.keyType)
			}
		})
	}

	for _, tc := range []struct {
		name  string
		ca    *certs.Pair
		req   certs.SignRequest
		field string
		code  certs.ProblemCode
	}{
		{"no domains", root, certs.SignRequest{Domains: []string{" "}, Days: 1, KeyType: certs.KeyEC}, "domains", certs.NoDomains},
		{"bad domain", root, certs.SignRequest{Domains: []string{"a b"}, Days: 1, KeyType: certs.KeyEC}, "domains", certs.InvalidDomain},
		{"too long", root, certs.SignRequest{Domains: []string{"a.example.com"}, Days: certs.MaxDays + 1, KeyType: certs.KeyEC}, "days", certs.InvalidDays},
		{"beyond the CA", inter, certs.SignRequest{Domains: []string{"a.example.com"}, Days: 365, KeyType: certs.KeyEC}, "days", certs.BeyondCA},
		{"key type", root, certs.SignRequest{Domains: []string{"a.example.com"}, Days: 1, KeyType: "dsa"}, "key_type", certs.InvalidKeyType},
		{"not a CA", leafPair(t, rootCert, rootKey, "a.example.com"), certs.SignRequest{Domains: []string{"b.example.com"}, Days: 1, KeyType: certs.KeyEC}, "authority", certs.NotCA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := certs.Sign(tc.ca, tc.req, now)
			if field, code := problem(err); field != tc.field || code != tc.code {
				t.Errorf("Sign error = %v, want %s on %s", err, tc.code, tc.field)
			}
		})
	}
}

func TestDefaultNames(t *testing.T) {
	caCert, caKey := certstest.CA(t, "  Home Lab Root CA ")
	if got := certs.DefaultCAName(certstest.Parse(t, caCert, caKey)); got != "Home Lab Root CA" {
		t.Errorf("DefaultCAName = %q", got)
	}
	if got := certs.DefaultName(leafPair(t, caCert, caKey, "Shop.Example.com", "10.0.0.1")); got != "shop.example.com" {
		t.Errorf("DefaultName = %q", got)
	}
}

// TestRead reads what it can of incomplete or wrong uploads, for previews.
func TestRead(t *testing.T) {
	caCert, caKey := certstest.CA(t, "Root")
	cert, key := certstest.Leaf(t, caCert, caKey, "example.com")
	_, otherKey := certstest.Leaf(t, caCert, caKey, "other.example.com")
	garbage := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}}))

	for _, tc := range []struct {
		name      string
		cert, key string
		leaf      string // common name of the first certificate; "" for none
		hasKey    bool
		problems  []string
	}{
		{"complete", caCert + cert, key, "example.com", true, nil},
		{"no key yet", caCert + cert, "", "example.com", false, []string{"key: no_key"}},
		{"wrong key", cert + caCert, otherKey, "example.com", false, []string{"key: key_mismatch"}},
		{"nothing yet", "", "", "", false, []string{"cert: no_certificate", "key: no_key"}},
		{"garbage and an encrypted key", garbage, "-----BEGIN ENCRYPTED PRIVATE KEY-----\nAQ==\n-----END ENCRYPTED PRIVATE KEY-----\n", "", false,
			[]string{"cert: invalid_certificate", "key: encrypted_key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := certs.Read([]byte(tc.cert), []byte(tc.key))
			var got []string
			for _, fe := range fieldErrorsOf(err) {
				_, code := problem(fe)
				got = append(got, fe.Field+": "+string(code))
			}
			if !slices.Equal(got, tc.problems) {
				t.Errorf("problems %v, want %v", got, tc.problems)
			}
			leaf := ""
			if len(p.Chain) > 0 {
				leaf = p.Leaf().Subject.CommonName
			}
			if leaf != tc.leaf || (p.Key != nil) != tc.hasKey {
				t.Errorf("first certificate %q with key %t, want %q with key %t", leaf, p.Key != nil, tc.leaf, tc.hasKey)
			}
			if _, err := certs.Parse([]byte(tc.cert), []byte(tc.key)); (err == nil) != (tc.problems == nil) {
				t.Errorf("Parse error = %v", err)
			}
		})
	}
}

func fieldErrorsOf(err error) config.FieldErrors {
	var errs config.FieldErrors
	errors.As(err, &errs)
	return errs
}

// TestPlan reports all problems with a signing request at once, for
// previews.
func TestPlan(t *testing.T) {
	now := time.Now()
	caCert, caKey := certstest.CA(t, "Root") // valid for a year
	ca := certstest.Parse(t, caCert, caKey)
	req := certs.SignRequest{Domains: []string{"a b", "B.example.com"}, Days: 400, KeyType: "dsa"}
	_, _, err := certs.Plan(ca, &req, now)
	var got []string
	for _, fe := range fieldErrorsOf(err) {
		_, code := problem(fe)
		got = append(got, fe.Field+": "+string(code))
	}
	if want := []string{"domains: invalid_domain", "key_type: invalid_key_type", "days: beyond_ca"}; !slices.Equal(got, want) {
		t.Errorf("problems %v, want %v", got, want)
	}
	if !slices.Equal(req.Domains, []string{"a b", "b.example.com"}) {
		t.Errorf("domains %v, want them normalized", req.Domains)
	}

	req = certs.SignRequest{Domains: []string{"a.example.com"}, Days: 30, KeyType: certs.KeyEC}
	notBefore, notAfter, err := certs.Plan(ca, &req, now)
	if err != nil || notBefore.After(now) || notAfter.Sub(now) != 30*24*time.Hour {
		t.Errorf("Plan = %v, %v, %v", notBefore, notAfter, err)
	}
	_, _, err = certs.Plan(ca, &req, now.AddDate(2, 0, 0))
	if field, code := problem(err); field != "authority" || code != certs.Expired {
		t.Errorf("Plan with an expired CA: %v, want expired on authority", err)
	}
}

func TestDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX permissions")
	}
	root := t.TempDir()
	d := certs.Dir(filepath.Join(root, "certs"))
	// Files that are not ours must survive pruning.
	if err := os.MkdirAll(filepath.Join(string(d), "mine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(string(d), "fallback.cer"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// A directory of the first version, named after the certificate.
	legacy := filepath.Join(string(d), "_.example.com-0123456789abcdef")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}

	a := d.Files([]byte("cert"), []byte("key"))
	b := d.Files([]byte("cert 2"), []byte("key"))
	if a.Fullchain == b.Fullchain || len(filepath.Base(filepath.Dir(a.Fullchain))) != 32 || a.CA != "" {
		t.Errorf("Files = %+v and %+v, want distinct directories named after hashes", a, b)
	}
	for _, c := range []string{"cert", "cert 2"} {
		if err := d.Write([]byte(c), []byte("key")); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Write([]byte("cert"), []byte("key")); err != nil {
		t.Errorf("writing existing files: %v", err)
	}
	if got, err := os.ReadFile(a.Fullchain); err != nil || string(got) != "cert" {
		t.Errorf("fullchain = %q, %v", got, err)
	}
	info, err := os.Stat(a.Key)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("key: %v, %v; want -rw-------", info.Mode(), err)
	}

	if err := d.Prune([]nginx.Certificate{b}); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{a.Fullchain, legacy} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s was kept: %v", gone, err)
		}
	}
	for _, kept := range []string{b.Key, filepath.Join(string(d), "mine"), filepath.Join(string(d), "fallback.cer")} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("Prune removed %s: %v", kept, err)
		}
	}
	if err := certs.Dir(filepath.Join(root, "missing")).Prune(nil); err != nil {
		t.Errorf("pruning a missing directory: %v", err)
	}
}
