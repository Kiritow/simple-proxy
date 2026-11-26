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
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

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
	InvalidCertName:    "%q is not a valid name: it must fit on one line, with at most 128 characters",
	NameInUse:          "the name %q is taken",
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

// Read reads a certificate chain and its private key from PEM, as pasted or
// uploaded, as far as it can. The certificates and the key may be in either
// text, in any order: the chain starts with the certificate whose public key
// matches the key, or else with one that issued none of the others, followed
// by its issuer, that one's issuer and so on, as far as they are given.
// Other certificates are dropped.
//
// The chain is empty if no certificate could be read, and the key nil unless
// it belongs to the first certificate. All problems found are reported, as
// config.FieldErrors for the fields "cert" and "key", e.g. for previews.
func Read(certPEM, keyPEM []byte) (*Pair, error) {
	var errs config.FieldErrors
	add := func(field string, code ProblemCode, value string) {
		errs = append(errs, &config.FieldError{Field: field, Err: &Problem{Code: code, Value: value}})
	}
	var certs []*x509.Certificate
	var keys []crypto.Signer
	badCert, badKey := false, false
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
					add("cert", InvalidCertificate, err.Error())
					badCert = true
				} else if !slices.ContainsFunc(certs, c.Equal) {
					certs = append(certs, c)
				}
			case block.Type == "ENCRYPTED PRIVATE KEY", block.Headers["Proc-Type"] == "4,ENCRYPTED":
				add("key", EncryptedKey, "")
				badKey = true
			case strings.HasSuffix(block.Type, "PRIVATE KEY"):
				key, err := parseKey(block)
				if err != nil {
					add("key", InvalidKey, err.Error())
					badKey = true
				} else {
					keys = append(keys, key)
				}
			}
		}
	}
	if len(certs) == 0 && !badCert {
		add("cert", NoCertificate, "")
	}
	if len(keys) == 0 && !badKey {
		add("key", NoKey, "")
	}
	p := &Pair{}
	if len(certs) == 0 {
		return p, orNil(errs)
	}

	first := -1
	for _, key := range keys {
		if i := slices.IndexFunc(certs, func(c *x509.Certificate) bool { return matches(c, key) }); i >= 0 {
			first, p.Key = i, key
			break
		}
	}
	if first < 0 {
		if len(keys) > 0 {
			add("key", KeyMismatch, "")
		}
		first = endEntity(certs)
	}
	p.Chain = []*x509.Certificate{certs[first]}
	for rest := slices.Delete(slices.Clone(certs), first, first+1); ; {
		last := p.Chain[len(p.Chain)-1]
		if selfSigned(last) {
			break
		}
		j := slices.IndexFunc(rest, func(c *x509.Certificate) bool { return issued(c, last) })
		if j < 0 {
			break
		}
		p.Chain = append(p.Chain, rest[j])
		rest = slices.Delete(rest, j, j+1)
	}
	return p, orNil(errs)
}

// Parse is Read, failing on any problem.
func Parse(certPEM, keyPEM []byte) (*Pair, error) {
	p, err := Read(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// endEntity returns the index of the first certificate that issued none of
// the others.
func endEntity(certs []*x509.Certificate) int {
	for i, c := range certs {
		if !slices.ContainsFunc(certs, func(o *x509.Certificate) bool { return o != c && issued(c, o) }) {
			return i
		}
	}
	return 0
}

func orNil(errs config.FieldErrors) error {
	if len(errs) == 0 {
		return nil
	}
	return errs
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
	if fe := checkCA(p, now, "cert"); fe != nil {
		return fe
	}
	return nil
}

// checkCA is CheckCA, reporting problems for the given field.
func checkCA(p *Pair, now time.Time, field string) *config.FieldError {
	problem := func(code ProblemCode, value string) *config.FieldError {
		return &config.FieldError{Field: field, Err: &Problem{Code: code, Value: value}}
	}
	ca := p.Leaf()
	if !ca.BasicConstraintsValid || !ca.IsCA || (ca.KeyUsage != 0 && ca.KeyUsage&x509.KeyUsageCertSign == 0) {
		return problem(NotCA, "")
	}
	if now.After(ca.NotAfter) {
		return problem(Expired, ca.NotAfter.Format(time.DateOnly))
	}
	return nil
}

// Info describes a certificate.
type Info struct {
	Subject string // the common name, or the whole subject without one
	Issuer  string // likewise
	// Domains are the DNS names and IP addresses the certificate is valid
	// for, or its common name if it names none.
	Domains    []string
	Key        string // the algorithm and size of the key, such as "EC P-256"
	NotBefore  time.Time
	NotAfter   time.Time
	SelfSigned bool // a root, which clients must trust already
	// Serial is the serial number, and SHA256 the SHA-256 fingerprint of the
	// certificate, as colon-separated hex bytes like browsers show them.
	Serial, SHA256 string
}

// Describe describes the first certificate of p.
func Describe(p *Pair) Info { return DescribeCert(p.Leaf()) }

// DescribeCert describes c.
func DescribeCert(c *x509.Certificate) Info {
	sum := sha256.Sum256(c.Raw)
	info := Info{
		Subject:    nameOf(c.Subject),
		Issuer:     nameOf(c.Issuer),
		Key:        keyName(c.PublicKey),
		NotBefore:  c.NotBefore,
		NotAfter:   c.NotAfter,
		SelfSigned: selfSigned(c),
		Serial:     hexBytes(c.SerialNumber.Bytes()),
		SHA256:     hexBytes(sum[:]),
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

// hexBytes formats b like "04:A1:FF".
func hexBytes(b []byte) string {
	if len(b) == 0 {
		return "00"
	}
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02X", x)
	}
	return strings.Join(parts, ":")
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
// first domain, which sites use by default to find their certificate.
// Names are config.ValidCertName; it returns "" if that makes none.
func DefaultName(p *Pair) string {
	if d := Describe(p).Domains; len(d) > 0 && config.ValidCertName(strings.ToLower(d[0])) {
		return strings.ToLower(d[0])
	}
	return ""
}

// DefaultCAName returns the name of a CA uploaded without one: its subject,
// or "" if that makes no valid name.
func DefaultCAName(p *Pair) string {
	if name := strings.TrimSpace(nameOf(p.Leaf().Subject)); config.ValidCertName(name) {
		return name
	}
	return ""
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
func (req *SignRequest) Check() error { return orNil(req.check()) }

func (req *SignRequest) check() config.FieldErrors {
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
	return errs
}

// KeyName describes the key of a certificate signed with the given key
// type, like Info.Key.
func KeyName(keyType string) string {
	if keyType == KeyRSA {
		return "RSA 2048"
	}
	return "EC P-256"
}

// Plan checks that ca can sign the certificate described by req at now,
// normalizing req like Check, and returns when the certificate would be
// valid. All problems found are reported: those of Check, and those with
// the CA for the field "authority". ca may be nil if it is missing, which
// the caller reports.
func Plan(ca *Pair, req *SignRequest, now time.Time) (notBefore, notAfter time.Time, err error) {
	errs := req.check()
	// Allow for clocks running a little behind.
	notBefore = now.Add(-time.Hour)
	notAfter = now.Add(time.Duration(req.Days) * 24 * time.Hour)
	if ca != nil {
		if fe := checkCA(ca, now, "authority"); fe != nil {
			errs = append(errs, fe)
		} else {
			notBefore = later(notBefore, ca.Leaf().NotBefore)
			if req.Days >= 1 && req.Days <= MaxDays && notAfter.After(ca.Leaf().NotAfter) {
				errs = append(errs, &config.FieldError{Field: "days", Err: &Problem{Code: BeyondCA, Value: ca.Leaf().NotAfter.Format(time.DateOnly)}})
			}
		}
	}
	return notBefore, notAfter, orNil(errs)
}

// Sign has ca sign a new certificate for a website, with a new key, as
// described by req. The chain of the result includes the CA's chain, except
// for its root, which clients have to trust anyway.
func Sign(ca *Pair, req SignRequest, now time.Time) (*Pair, error) {
	notBefore, notAfter, err := Plan(ca, &req, now)
	if err != nil {
		return nil, err
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
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: req.Domains[0]},
		NotBefore:             notBefore,
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
