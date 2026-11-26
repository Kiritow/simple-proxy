// Package certstest creates certificates for tests.
package certstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/kiritow/simple-proxy/internal/certs"
)

// CA returns a new self-signed CA certificate with the given common name,
// valid for a year, and its key, in PEM.
func CA(t testing.TB, name string) (certPEM, keyPEM string) {
	t.Helper()
	return Issue(t, &x509.Certificate{
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}, "", "")
}

// Issue creates the certificate described by tmpl with a new key, signed by
// the given CA, or self-signed if caCert is empty, and returns it and its
// key in PEM.
func Issue(t testing.TB, tmpl *x509.Certificate, caCert, caKey string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl.SerialNumber = big.NewInt(time.Now().UnixNano())
	parent, signer := tmpl, any(key)
	if caCert != "" {
		ca := Parse(t, caCert, caKey)
		parent, signer = ca.Leaf(), ca.Key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, key.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

// Leaf returns a certificate for domains signed by the given CA, valid for
// 30 days, and its key, in PEM.
func Leaf(t testing.TB, caCert, caKey string, domains ...string) (certPEM, keyPEM string) {
	t.Helper()
	pair, err := certs.Sign(Parse(t, caCert, caKey), certs.SignRequest{Domains: domains, Days: 30, KeyType: certs.KeyEC}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return string(pair.CertPEM()), string(pair.KeyPEM())
}

// Parse parses a certificate and its key.
func Parse(t testing.TB, certPEM, keyPEM string) *certs.Pair {
	t.Helper()
	pair, err := certs.Parse([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	return pair
}
