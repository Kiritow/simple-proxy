// Package acme manages certificates with acme.sh
// (https://github.com/acmesh-official/acme.sh).
package acme

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kiritow/simple-proxy/internal/nginx"
)

// IssueTimeout bounds Issue: DNS validation waits for records to propagate.
const IssueTimeout = 5 * time.Minute

// listTTL is how long Certs reuses the certificate list. acme.sh is a large
// shell script, and generating a configuration looks up every certificate.
const listTTL = 10 * time.Second

// Client runs the acme.sh script. It implements nginx.CertLocator. It is
// safe for concurrent use.
type Client struct {
	bin string

	mu       sync.Mutex
	certs    []Cert // cached listing
	listedAt time.Time
	dirs     map[string]string // certificate directories by name
}

// NewClient returns a Client running the acme.sh script at bin.
func NewClient(bin string) *Client { return &Client{bin: bin, dirs: map[string]string{}} }

// forget drops cached information, after certificates changed.
func (c *Client) forget() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.certs = nil
	clear(c.dirs)
}

// Cert describes a certificate managed by acme.sh.
type Cert struct {
	// Name is the main domain, which identifies the certificate.
	Name string `json:"name"`
	// Domains are the main domain followed by the alternative names.
	Domains   []string `json:"domains"`
	KeyLength string   `json:"key_length"` // such as "ec-256" or "2048"
	CA        string   `json:"ca"`
	Created   string   `json:"created"` // as reported by acme.sh
	Renew     string   `json:"renew"`   // the next renewal
}

// Certs returns the certificates managed by acme.sh.
func (c *Client) Certs(ctx context.Context) ([]Cert, error) {
	c.mu.Lock()
	if c.certs != nil && time.Since(c.listedAt) < listTTL {
		defer c.mu.Unlock()
		return slices.Clone(c.certs), nil
	}
	c.mu.Unlock()

	certs, err := c.list(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.certs, c.listedAt = certs, time.Now()
	c.mu.Unlock()
	return slices.Clone(certs), nil
}

func (c *Client) list(ctx context.Context) ([]Cert, error) {
	out, err := c.run(ctx, "--list", "--listraw")
	if err != nil {
		return nil, err
	}
	// The output is a "|"-separated table with a header line, such as
	// Main_Domain|KeyLength|SAN_Domains|Profile|CA|Created|Renew. Columns
	// vary between versions, so they are found by name.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	columns := strings.Split(lines[0], "|")
	certs := []Cert{}
	for _, line := range lines[1:] {
		fields := strings.Split(line, "|")
		get := func(name string) string {
			if i := slices.Index(columns, name); i >= 0 && i < len(fields) {
				return strings.Trim(strings.TrimSpace(fields[i]), `"`)
			}
			return ""
		}
		cert := Cert{
			Name:      get("Main_Domain"),
			KeyLength: get("KeyLength"),
			CA:        get("CA"),
			Created:   get("Created"),
			Renew:     get("Renew"),
		}
		if cert.Name == "" {
			continue
		}
		cert.Domains = []string{cert.Name}
		if san := get("SAN_Domains"); san != "" && san != "no" {
			cert.Domains = append(cert.Domains, strings.Split(san, ",")...)
		}
		certs = append(certs, cert)
	}
	return certs, nil
}

// LocateCert returns the files of the certificate whose main domain is name,
// such as "example.com" or "*.example.com". If acme.sh manages no such
// certificate, the error wraps nginx.ErrCertNotFound.
func (c *Client) LocateCert(ctx context.Context, name string) (nginx.Certificate, error) {
	// Check name against the certificate list first, so that only names
	// reported by acme.sh itself are ever passed back to it as arguments.
	certs, err := c.Certs(ctx)
	if err != nil {
		return nginx.Certificate{}, err
	}
	if !slices.ContainsFunc(certs, func(cert Cert) bool { return cert.Name == name }) {
		return nginx.Certificate{}, fmt.Errorf("%w: acme.sh has no certificate for %q", nginx.ErrCertNotFound, name)
	}

	dir, err := c.certDir(ctx, name)
	if err != nil {
		return nginx.Certificate{}, err
	}
	return nginx.Certificate{
		Fullchain: filepath.Join(dir, "fullchain.cer"),
		Key:       filepath.Join(dir, name+".key"),
		CA:        filepath.Join(dir, "ca.cer"),
	}, nil
}

// IssueRequest describes a certificate to issue. Exactly one of Webroot and
// DNS selects how acme.sh proves control of the domains.
type IssueRequest struct {
	// Domains are the names to cover; the first one names the certificate.
	Domains []string
	// Webroot is a directory that nginx serves at /.well-known/acme-challenge/
	// for every domain, for HTTP-01 validation.
	Webroot string
	// DNS is an acme.sh DNS API, such as "dns_cf", for DNS-01 validation. Its
	// credentials must already be known to acme.sh. Wildcards require DNS.
	DNS string
	// RenewHook is a command that acme.sh runs after renewing the
	// certificate, such as one reloading nginx.
	RenewHook string
}

var (
	domainName  = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)+$`)
	dnsProvider = regexp.MustCompile(`^dns_[a-z0-9_]+$`)
)

// ErrInvalidRequest is wrapped by the errors of Issue for invalid requests.
var ErrInvalidRequest = errors.New("invalid certificate request")

func (r IssueRequest) check() error {
	if len(r.Domains) == 0 {
		return fmt.Errorf("%w: no domains", ErrInvalidRequest)
	}
	for _, d := range r.Domains {
		if !domainName.MatchString(d) {
			return fmt.Errorf("%w: %q is not a domain name", ErrInvalidRequest, d)
		}
	}
	switch {
	case (r.Webroot == "") == (r.DNS == ""):
		return fmt.Errorf("%w: choose either HTTP or DNS validation", ErrInvalidRequest)
	case r.DNS != "" && !dnsProvider.MatchString(r.DNS):
		return fmt.Errorf("%w: %q is not an acme.sh DNS API such as dns_cf", ErrInvalidRequest, r.DNS)
	case r.Webroot != "" && slices.ContainsFunc(r.Domains, func(d string) bool { return strings.HasPrefix(d, "*.") }):
		return fmt.Errorf("%w: wildcard certificates require DNS validation", ErrInvalidRequest)
	}
	return nil
}

// Issue has acme.sh issue the certificate described by req, and returns the
// output of acme.sh, which explains failures. If the certificate exists and
// is not due for renewal, acme.sh skips it, and Issue succeeds.
func (c *Client) Issue(ctx context.Context, req IssueRequest) (string, error) {
	if err := req.check(); err != nil {
		return "", err
	}
	args := []string{"--issue"}
	for _, d := range req.Domains {
		args = append(args, "-d", d)
	}
	if req.Webroot != "" {
		args = append(args, "--webroot", req.Webroot)
	} else {
		args = append(args, "--dns", req.DNS)
	}
	if req.RenewHook != "" {
		args = append(args, "--renew-hook", req.RenewHook)
	}

	ctx, cancel := context.WithTimeout(ctx, IssueTimeout)
	defer cancel()
	defer c.forget()
	out, err := exec.CommandContext(ctx, c.bin, args...).CombinedOutput()
	output := string(bytes.TrimSpace(out))
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 2 {
		return output, nil // skipped: already issued and not due for renewal
	}
	if err != nil {
		return output, fmt.Errorf("acme.sh --issue: %w", err)
	}
	return output, nil
}

// certDir returns the directory holding the certificate files of domain.
func (c *Client) certDir(ctx context.Context, domain string) (string, error) {
	c.mu.Lock()
	dir, ok := c.dirs[domain]
	c.mu.Unlock()
	if ok {
		return dir, nil
	}
	dir, err := c.info(ctx, domain)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.dirs[domain] = dir
	c.mu.Unlock()
	return dir, nil
}

func (c *Client) info(ctx context.Context, domain string) (string, error) {
	out, err := c.run(ctx, "--info", "-d", domain)
	if err != nil {
		return "", err
	}
	for line := range strings.Lines(string(out)) {
		if conf, ok := strings.CutPrefix(strings.TrimSpace(line), "DOMAIN_CONF="); ok {
			return filepath.Dir(strings.Trim(conf, `"'`)), nil
		}
	}
	return "", fmt.Errorf("acme.sh --info -d %s: output has no DOMAIN_CONF", domain)
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		err = fmt.Errorf("running acme.sh %s: %w", strings.Join(args, " "), err)
		if msg := bytes.TrimSpace(stderr.Bytes()); len(msg) > 0 {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}
