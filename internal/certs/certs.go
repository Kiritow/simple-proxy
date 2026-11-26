// Package certs handles the certificates that simple-proxy keeps itself,
// besides those of acme.sh: certificates uploaded with their private keys,
// certificate authorities (CAs) uploaded to sign certificates, and the
// certificates they sign.
package certs

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kiritow/simple-proxy/internal/config"
)

// ProblemCode identifies a problem with a request, so that clients can
// describe it in their own words.
type ProblemCode string

const (
	NoCertificate      ProblemCode = "no_certificate"
	InvalidCertificate ProblemCode = "invalid_certificate"
	NoKey              ProblemCode = "no_key"
	EncryptedKey       ProblemCode = "encrypted_key"
	InvalidKey         ProblemCode = "invalid_key"
	KeyMismatch        ProblemCode = "key_mismatch"
	NotCA              ProblemCode = "not_ca"
	Expired            ProblemCode = "expired"
	InvalidCertName    ProblemCode = "invalid_cert_name"
	InvalidCAName      ProblemCode = "invalid_ca_name"
	NameInUse          ProblemCode = "name_in_use"
	CANotFound         ProblemCode = "ca_not_found"
	NoDomains          ProblemCode = "no_domains"
	InvalidDomain      ProblemCode = "invalid_domain"
	InvalidDays        ProblemCode = "invalid_days"
	BeyondCA           ProblemCode = "beyond_ca"
	InvalidKeyType     ProblemCode = "invalid_key_type"
	CertInUse          ProblemCode = "cert_in_use"
)

var problemText = map[ProblemCode]string{
	NoCertificate:      "contains no PEM certificate",
	InvalidCertificate: "is not a valid certificate: %s",
	NoKey:              "contains no PEM private key",
	EncryptedKey:       "is encrypted; remove its passphrase, e.g. with openssl pkey",
	InvalidKey:         "is not a valid private key: %s",
	KeyMismatch:        "does not belong to the certificate",
	NotCA:              "is not a CA certificate",
	Expired:            "expired on %s",
	InvalidCertName:    "%q is not a valid certificate name",
	InvalidCAName:      "%q is not a valid CA name",
	NameInUse:          "%q is already used by another certificate",
	CANotFound:         "there is no CA named %q",
	NoDomains:          "at least one domain is required",
	InvalidDomain:      "%q is not a domain name or IP address",
	InvalidDays:        "%s is not between 1 and 3650 days",
	BeyondCA:           "the CA expires on %s, before the certificate would",
	InvalidKeyType:     "%q is not a key type: use ec or rsa",
	CertInUse:          "is used by %s",
}

// Problem is a problem with a field of a request. It is reported wrapped in
// a *config.FieldError naming the field.
type Problem struct {
	Code  ProblemCode
	Value string
}

func (p *Problem) Error() string {
	text := problemText[p.Code]
	if strings.Contains(text, "%") {
		return fmt.Sprintf(text, p.Value)
	}
	return text
}

// FieldError returns the error reporting a problem with field.
func FieldError(field string, code ProblemCode, value string) error {
	return &config.FieldError{Field: field, Err: &Problem{Code: code, Value: value}}
}

// Pair is a certificate chain with the private key of its first
// certificate.
type Pair struct {
	// Chain starts with the certificate, which the other certificates in
	// it, if any, issued.
	Chain []*x509.Certificate
	Key   crypto.Signer
}

// Leaf returns the first certificate of the chain.
func (p *Pair) Leaf() *x509.Certificate { return p.Chain[0] }

// CertPEM returns the chain in PEM, as nginx's ssl_certificate expects it.
func (p *Pair) CertPEM() []byte {
	var buf bytes.Buffer
	for _, c := range p.Chain {
		pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	return buf.Bytes()
}

// KeyPEM returns the private key in PKCS #8 PEM.
func (p *Pair) KeyPEM() []byte {
	der, err := x509.MarshalPKCS8PrivateKey(p.Key)
	if err != nil {
		// Parse and Sign only produce keys that can be marshaled.
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// Parse reads a certificate chain and its private key from PEM, as pasted or
// uploaded. The certificates and the key may be in either text, in any
// order: the chain starts with the certificate whose public key matches the
// key, followed by its issuer, that one's issuer and so on, as far as they
// are given. Other certificates are dropped. Problems are reported for the
// field "cert" or "key".
func Parse(certPEM, keyPEM []byte) (*Pair, error) {
	var certs []*x509.Certificate
	var keys []crypto.Signer
	for _, text := range [][]byte{certPEM, keyPEM} {
		for rest := text; ; {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			switch {
			case block.Type == "CERTIFICATE":
				c, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					return nil, FieldError("cert", InvalidCertificate, err.Error())
				}
				if !slices.ContainsFunc(certs, c.Equal) {
					certs = append(certs, c)
				}
			case block.Type == "ENCRYPTED PRIVATE KEY", block.Headers["Proc-Type"] == "4,ENCRYPTED":
				return nil, FieldError("key", EncryptedKey, "")
			case strings.HasSuffix(block.Type, "PRIVATE KEY"):
				key, err := parseKey(block)
				if err != nil {
					return nil, FieldError("key", InvalidKey, err.Error())
				}
				keys = append(keys, key)
			}
		}
	}
	switch {
	case len(certs) == 0:
		return nil, FieldError("cert", NoCertificate, "")
	case len(keys) == 0:
		return nil, FieldError("key", NoKey, "")
	}
	key := keys[0]
	i := slices.IndexFunc(certs, func(c *x509.Certificate) bool { return matches(c, key) })
	if i < 0 {
		return nil, FieldError("key", KeyMismatch, "")
	}
	chain := []*x509.Certificate{certs[i]}
	for rest := slices.Delete(slices.Clone(certs), i, i+1); ; {
		last := chain[len(chain)-1]
		if selfSigned(last) {
			break
		}
		j := slices.IndexFunc(rest, func(c *x509.Certificate) bool { return issued(c, last) })
		if j < 0 {
			break
		}
		chain = append(chain, rest[j])
		rest = slices.Delete(rest, j, j+1)
	}
	return &Pair{Chain: chain, Key: key}, nil
}

// issued reports whether ca issued c.
func issued(ca, c *x509.Certificate) bool {
	return bytes.Equal(c.RawIssuer, ca.RawSubject) && c.CheckSignatureFrom(ca) == nil
}

// WithoutRoot returns p without the self-signed root at the end of its chain,
// unless that is all of it: clients must trust the root already, so servers
// do not send it.
func (p *Pair) WithoutRoot() *Pair {
	if n := len(p.Chain); n > 1 && selfSigned(p.Chain[n-1]) {
		return &Pair{Chain: p.Chain[:n-1], Key: p.Key}
	}
	return p
}

func parseKey(block *pem.Block) (crypto.Signer, error) {
	var key any
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	}
	if err != nil {
		return nil, err
	}
	switch key.(type) {
	case *rsa.PrivateKey, *ecdsa.PrivateKey, ed25519.PrivateKey:
		return key.(crypto.Signer), nil
	}
	return nil, fmt.Errorf("unsupported key type %T", key)
}

// matches reports whether key is the private key of c.
func matches(c *x509.Certificate, key crypto.Signer) bool {
	pub, ok := key.Public().(interface{ Equal(crypto.PublicKey) bool })
	return ok && pub.Equal(c.PublicKey)
}

// CheckCertificate checks a certificate for a site.
func CheckCertificate(p *Pair, now time.Time) error {
	if now.After(p.Leaf().NotAfter) {
		return FieldError("cert", Expired, p.Leaf().NotAfter.Format(time.DateOnly))
	}
	return nil
}

// CheckCA checks a CA certificate, which is to sign certificates.
func CheckCA(p *Pair, now time.Time) error {
	ca := p.Leaf()
	if !ca.BasicConstraintsValid || !ca.IsCA || (ca.KeyUsage != 0 && ca.KeyUsage&x509.KeyUsageCertSign == 0) {
		return FieldError("cert", NotCA, "")
	}
	if now.After(ca.NotAfter) {
		return FieldError("cert", Expired, ca.NotAfter.Format(time.DateOnly))
	}
	return nil
}

// Info describes a certificate.
type Info struct {
	Subject string // the common name, or the whole subject without one
	Issuer  string // likewise
	// Domains are the DNS names and IP addresses the certificate is valid
	// for, or its common name if it names none.
	Domains   []string
	Key       string // the algorithm and size of the key, such as "EC P-256"
	NotBefore time.Time
	NotAfter  time.Time
}

// Describe describes the first certificate of p.
func Describe(p *Pair) Info {
	c := p.Leaf()
	info := Info{
		Subject:   nameOf(c.Subject),
		Issuer:    nameOf(c.Issuer),
		Key:       keyName(c.PublicKey),
		NotBefore: c.NotBefore,
		NotAfter:  c.NotAfter,
	}
	info.Domains = slices.Clone(c.DNSNames)
	for _, ip := range c.IPAddresses {
		info.Domains = append(info.Domains, ip.String())
	}
	if len(info.Domains) == 0 && c.Subject.CommonName != "" {
		info.Domains = []string{c.Subject.CommonName}
	}
	return info
}

func nameOf(n pkix.Name) string {
	if n.CommonName != "" {
		return n.CommonName
	}
	return n.String()
}

func keyName(pub crypto.PublicKey) string {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return "RSA " + strconv.Itoa(k.N.BitLen())
	case *ecdsa.PublicKey:
		return "EC " + k.Curve.Params().Name
	case ed25519.PublicKey:
		return "Ed25519"
	}
	return "unknown"
}

// DefaultName returns the name of a certificate uploaded without one: its
// first domain, if that makes a valid name.
func DefaultName(p *Pair) string {
	if d := Describe(p).Domains; len(d) > 0 && ValidCertName(d[0]) {
		return strings.ToLower(d[0])
	}
	return ""
}

// DefaultCAName returns the name of a CA uploaded without one.
func DefaultCAName(p *Pair) string {
	if name := strings.TrimSpace(nameOf(p.Leaf().Subject)); ValidCAName(name) {
		return name
	}
	return ""
}

// certName are the names of certificates. Sites refer to certificates by
// name, and certificates are usually named after their first domain, such
// as "*.example.com" or "10.0.0.1".
var certName = regexp.MustCompile(`^[A-Za-z0-9*:_][A-Za-z0-9*:._-]{0,252}$`)

// ValidCertName reports whether name can name a certificate.
func ValidCertName(name string) bool {
	return certName.MatchString(name) && config.IsWord(name)
}

// ValidCAName reports whether name can name a CA: it is shown to people, so
// it may contain spaces, but it is also part of URLs.
func ValidCAName(name string) bool {
	return name != "" && len(name) <= 64 && name == strings.TrimSpace(name) &&
		!strings.ContainsFunc(name, func(r rune) bool { return r == '/' || !unicode.IsPrint(r) })
}

// SignRequest describes a certificate for a CA to sign.
type SignRequest struct {
	// Domains are DNS names, possibly wildcards, and IP addresses.
	Domains []string
	// Days is how long the certificate is valid.
	Days int
	// KeyType is the type of the certificate's new key: KeyEC or KeyRSA.
	KeyType string
}

// Key types of signed certificates.
const (
	KeyEC  = "ec"  // ECDSA P-256
	KeyRSA = "rsa" // RSA 2048, for old clients
)

// MaxDays is the longest validity of signed certificates. Apple devices
// reject certificates of private CAs that are valid for more than 825 days.
const MaxDays = 3650

var domainName = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9])?(\.[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9])?)*$`)

// Check checks req, normalizing its domains to lower case without
// duplicates. Problems are reported for the fields "domains", "days" and
// "key_type".
func (req *SignRequest) Check() error {
	var errs config.FieldErrors
	add := func(field string, code ProblemCode, value string) {
		errs = append(errs, &config.FieldError{Field: field, Err: &Problem{Code: code, Value: value}})
	}
	var domains []string
	for _, d := range req.Domains {
		d = strings.ToLower(strings.TrimSpace(d))
		switch {
		case d == "" || slices.Contains(domains, d):
			continue
		case net.ParseIP(d) == nil && (!domainName.MatchString(d) || len(d) > 253):
			add("domains", InvalidDomain, d)
		}
		domains = append(domains, d)
	}
	if len(domains) == 0 {
		add("domains", NoDomains, "")
	}
	req.Domains = domains
	if req.Days < 1 || req.Days > MaxDays {
		add("days", InvalidDays, strconv.Itoa(req.Days))
	}
	if req.KeyType != KeyEC && req.KeyType != KeyRSA {
		add("key_type", InvalidKeyType, req.KeyType)
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// Sign has ca sign a new certificate for a website, with a new key, as
// described by req. The chain of the result includes the CA's chain, except
// for its root, which clients have to trust anyway.
func Sign(ca *Pair, req SignRequest, now time.Time) (*Pair, error) {
	if err := req.Check(); err != nil {
		return nil, err
	}
	if err := CheckCA(ca, now); err != nil {
		return nil, err
	}
	notAfter := now.Add(time.Duration(req.Days) * 24 * time.Hour)
	if notAfter.After(ca.Leaf().NotAfter) {
		return nil, FieldError("days", BeyondCA, ca.Leaf().NotAfter.Format(time.DateOnly))
	}

	key, err := generateKey(req.KeyType)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: req.Domains[0]},
		// Allow for clocks running a little behind.
		NotBefore:             later(now.Add(-time.Hour), ca.Leaf().NotBefore),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if len(tmpl.Subject.CommonName) > 64 {
		tmpl.Subject.CommonName = "" // too long for a common name; the SAN suffices
	}
	if req.KeyType == KeyRSA {
		tmpl.KeyUsage |= x509.KeyUsageKeyEncipherment
	}
	for _, d := range req.Domains {
		if ip := net.ParseIP(d); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, d)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Leaf(), key.Public(), ca.Key)
	if err != nil {
		return nil, fmt.Errorf("signing the certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	chain := append([]*x509.Certificate{leaf}, ca.Chain...)
	return (&Pair{Chain: chain, Key: key}).WithoutRoot(), nil
}

func generateKey(keyType string) (crypto.Signer, error) {
	if keyType == KeyRSA {
		return rsa.GenerateKey(rand.Reader, 2048)
	}
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

func selfSigned(c *x509.Certificate) bool { return issued(c, c) }

// randomSerial returns a random positive serial number of 128 bits at most.
func randomSerial() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	return n.Add(n, big.NewInt(1)), nil
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// IsProblem reports whether err reports a problem with a request, as
// opposed to a failure.
func IsProblem(err error) bool {
	var p *Problem
	return errors.As(err, &p)
}
