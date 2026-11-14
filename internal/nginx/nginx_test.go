package nginx_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/nginx"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// fakeCerts serves certificates from a map and counts lookups.
type fakeCerts struct {
	certs   map[string]nginx.Certificate
	lookups int
}

func (f *fakeCerts) LocateCert(_ context.Context, name string) (nginx.Certificate, error) {
	f.lookups++
	if c, ok := f.certs[name]; ok {
		return c, nil
	}
	return nginx.Certificate{}, fmt.Errorf("%w: %s", nginx.ErrCertNotFound, name)
}

func newGenerator(certs nginx.CertLocator) *nginx.Generator {
	return &nginx.Generator{
		Certs: certs,
		Now:   func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
}

func testCerts() *fakeCerts {
	return &fakeCerts{certs: map[string]nginx.Certificate{
		"*.example.com": {
			Fullchain: "/acme/*.example.com_ecc/fullchain.cer",
			Key:       "/acme/*.example.com_ecc/*.example.com.key",
			CA:        "/acme/*.example.com_ecc/ca.cer",
		},
	}}
}

// TestGenerateGolden renders every testdata/*.yaml and compares the result
// with the matching .conf file. Run "go test -update" to rewrite them.
// Inputs named acme-*.yaml are rendered with an ACME webroot.
func TestGenerateGolden(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) == 0 {
		t.Fatal("no test inputs")
	}
	for _, input := range inputs {
		t.Run(filepath.Base(input), func(t *testing.T) {
			data, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			gen := newGenerator(testCerts())
			if strings.HasPrefix(filepath.Base(input), "acme-") {
				gen.ACMEWebroot = "/var/www/acme"
			}
			got, err := gen.Generate(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}

			golden := strings.TrimSuffix(input, ".yaml") + ".conf"
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("output differs from %s:\n%s", golden, got)
			}
		})
	}
}

func TestGenerateCertNotFound(t *testing.T) {
	cfg, err := config.Parse([]byte(`
servers:
- server_name: missing.example.org
  https: true
  default: {port: 3000}
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		certs nginx.CertLocator
	}{
		{"unknown certificate", testCerts()},
		{"no locator", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newGenerator(tc.certs).Generate(t.Context(), cfg)
			if !errors.Is(err, nginx.ErrCertNotFound) {
				t.Errorf("Generate error = %v, want ErrCertNotFound", err)
			}
			var fe *config.FieldError
			if !errors.As(err, &fe) || fe.Field != "servers[0].cert_name" {
				t.Errorf("Generate error = %v, want a FieldError for servers[0].cert_name", err)
			}
		})
	}
}

func TestGenerateLooksUpSharedCertificateOnce(t *testing.T) {
	cfg, err := config.Parse([]byte(`
servers:
- server_name: a.example.com
  cert_name: "*.example.com"
  default: {port: 3000}
- server_name: b.example.com
  cert_name: "*.example.com"
  default: {port: 3001}
`))
	if err != nil {
		t.Fatal(err)
	}
	certs := testCerts()
	if _, err := newGenerator(certs).Generate(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	if certs.lookups != 1 {
		t.Errorf("certificate looked up %d times, want 1", certs.lookups)
	}
}
