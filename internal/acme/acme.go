// Package acme looks up certificates issued by acme.sh
// (https://github.com/acmesh-official/acme.sh).
package acme

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kiritow/simple-proxy/internal/nginx"
)

// Locator finds certificates by running the acme.sh script. It implements
// nginx.CertLocator.
type Locator struct {
	bin string
}

// NewLocator returns a Locator running the acme.sh script at bin.
func NewLocator(bin string) *Locator { return &Locator{bin: bin} }

// LocateCert returns the files of the certificate whose main domain is name,
// such as "example.com" or "*.example.com". If acme.sh manages no such
// certificate, the error wraps nginx.ErrCertNotFound.
func (l *Locator) LocateCert(ctx context.Context, name string) (nginx.Certificate, error) {
	// Check name against the certificate list first, so that only names
	// reported by acme.sh itself are ever passed back to it as arguments.
	domains, err := l.ListCerts(ctx)
	if err != nil {
		return nginx.Certificate{}, err
	}
	if !slices.Contains(domains, name) {
		return nginx.Certificate{}, fmt.Errorf("%w: acme.sh has no certificate for %q", nginx.ErrCertNotFound, name)
	}

	dir, err := l.certDir(ctx, name)
	if err != nil {
		return nginx.Certificate{}, err
	}
	return nginx.Certificate{
		Fullchain: filepath.Join(dir, "fullchain.cer"),
		Key:       filepath.Join(dir, name+".key"),
		CA:        filepath.Join(dir, "ca.cer"),
	}, nil
}

// ListCerts returns the names, i.e. the main domains, of the certificates
// managed by acme.sh.
func (l *Locator) ListCerts(ctx context.Context) ([]string, error) {
	out, err := l.run(ctx, "--list", "--listraw")
	if err != nil {
		return nil, err
	}
	// The output is a "|"-separated table whose first line is a header:
	// Main_Domain|KeyLength|SAN_Domains|Profile|CA|Created|Renew
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var domains []string
	for _, line := range lines[1:] {
		domain, _, _ := strings.Cut(line, "|")
		if domain = strings.TrimSpace(domain); domain != "" {
			domains = append(domains, domain)
		}
	}
	return domains, nil
}

// certDir returns the directory holding the certificate files of domain.
func (l *Locator) certDir(ctx context.Context, domain string) (string, error) {
	out, err := l.run(ctx, "--info", "-d", domain)
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

func (l *Locator) run(ctx context.Context, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, l.bin, args...)
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
