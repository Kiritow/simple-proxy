package acme_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/kiritow/simple-proxy/internal/acme"
	"github.com/kiritow/simple-proxy/internal/nginx"
)

// fakeAcme writes a shell script imitating acme.sh and returns its path and
// the path of the file logging its invocations.
func fakeAcme(t *testing.T, script string) (bin, calls string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	bin = filepath.Join(dir, "acme.sh")
	calls = filepath.Join(dir, "calls")
	script = "#!/bin/sh\necho \"$*\" >> " + calls + "\n" + script
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

const listAndInfo = `
case "$1" in
--list)
  echo 'Main_Domain|KeyLength|SAN_Domains|Profile|CA|Created|Renew'
  echo 'example.com|"ec-256"|www.example.com||ZeroSSL.com|2026-01-01T00:00:00Z|2026-03-01T00:00:00Z'
  echo '*.example.org|"2048"|no||LetsEncrypt.org|2026-01-01T00:00:00Z|2026-03-01T00:00:00Z'
  ;;
--info)
  echo "DOMAIN_CONF=/home/u/.acme.sh/$3_ecc/$3.conf"
  echo "Le_Domain=$3"
  ;;
*)
  exit 1
  ;;
esac
`

func TestLocateCert(t *testing.T) {
	bin, _ := fakeAcme(t, listAndInfo)
	got, err := acme.NewClient(bin).LocateCert(t.Context(), "*.example.org")
	if err != nil {
		t.Fatal(err)
	}
	want := nginx.Certificate{
		Fullchain: "/home/u/.acme.sh/*.example.org_ecc/fullchain.cer",
		Key:       "/home/u/.acme.sh/*.example.org_ecc/*.example.org.key",
		CA:        "/home/u/.acme.sh/*.example.org_ecc/ca.cer",
	}
	if got != want {
		t.Errorf("LocateCert = %+v, want %+v", got, want)
	}
}

func TestCerts(t *testing.T) {
	bin, _ := fakeAcme(t, listAndInfo)
	got, err := acme.NewClient(bin).Certs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []acme.Cert{
		{
			Name: "example.com", Domains: []string{"example.com", "www.example.com"},
			KeyLength: "ec-256", CA: "ZeroSSL.com", Created: "2026-01-01T00:00:00Z", Renew: "2026-03-01T00:00:00Z",
		},
		{
			Name: "*.example.org", Domains: []string{"*.example.org"},
			KeyLength: "2048", CA: "LetsEncrypt.org", Created: "2026-01-01T00:00:00Z", Renew: "2026-03-01T00:00:00Z",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Certs =\n%+v\nwant\n%+v", got, want)
	}
}

func TestCertsOlderColumns(t *testing.T) {
	// Older acme.sh versions have no Profile column.
	bin, _ := fakeAcme(t, `echo 'Main_Domain|KeyLength|SAN_Domains|CA|Created|Renew'
echo 'a.example.com|"ec-256"|b.example.com,c.example.com|LetsEncrypt.org|2026-01-01|2026-03-01'
`)
	got, err := acme.NewClient(bin).Certs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].CA != "LetsEncrypt.org" || len(got[0].Domains) != 3 {
		t.Errorf("Certs = %+v", got)
	}
}

func TestCertsEmpty(t *testing.T) {
	bin, _ := fakeAcme(t, "echo 'Main_Domain|KeyLength|SAN_Domains|Profile|CA|Created|Renew'\n")
	got, err := acme.NewClient(bin).Certs(t.Context())
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("Certs = %#v, %v; want an empty list", got, err)
	}
}

func TestIssue(t *testing.T) {
	bin, calls := fakeAcme(t, "echo 'Cert success.'\n")
	c := acme.NewClient(bin)
	for _, tc := range []struct {
		req  acme.IssueRequest
		want string
	}{
		{
			acme.IssueRequest{Domains: []string{"example.com", "www.example.com"}, Webroot: "/var/www/acme", RenewHook: "nginx -s reload"},
			"--issue -d example.com -d www.example.com --webroot /var/www/acme --renew-hook nginx -s reload",
		},
		{
			acme.IssueRequest{Domains: []string{"*.example.com"}, DNS: "dns_cf"},
			"--issue -d *.example.com --dns dns_cf",
		},
	} {
		os.Remove(calls)
		out, err := c.Issue(t.Context(), tc.req)
		if err != nil || out != "Cert success." {
			t.Fatalf("Issue(%+v) = %q, %v", tc.req, out, err)
		}
		log, _ := os.ReadFile(calls)
		if got := strings.TrimSpace(string(log)); got != tc.want {
			t.Errorf("acme.sh %s, want %s", got, tc.want)
		}
	}
}

func TestIssueSkipped(t *testing.T) {
	// acme.sh exits with 2 if the certificate is not due for renewal.
	bin, _ := fakeAcme(t, "echo 'Skipping. Next renewal time is: 2026-03-01'\nexit 2\n")
	out, err := acme.NewClient(bin).Issue(t.Context(), acme.IssueRequest{Domains: []string{"example.com"}, Webroot: "/w"})
	if err != nil || !strings.Contains(out, "Skipping") {
		t.Errorf("Issue = %q, %v; want success", out, err)
	}
}

func TestIssueFailure(t *testing.T) {
	bin, _ := fakeAcme(t, "echo 'Verify error: Invalid response from http://example.com/.well-known/acme-challenge/x: 404'\nexit 1\n")
	out, err := acme.NewClient(bin).Issue(t.Context(), acme.IssueRequest{Domains: []string{"example.com"}, Webroot: "/w"})
	if err == nil || !strings.Contains(out, "Invalid response") {
		t.Errorf("Issue = %q, %v; want an error with acme.sh's explanation", out, err)
	}
}

func TestIssueRejectsInvalidRequests(t *testing.T) {
	bin, calls := fakeAcme(t, "exit 0\n")
	c := acme.NewClient(bin)
	for name, req := range map[string]acme.IssueRequest{
		"no domains":            {Webroot: "/w"},
		"option as domain":      {Domains: []string{"--uninstall"}, Webroot: "/w"},
		"bare word":             {Domains: []string{"localhost"}, Webroot: "/w"},
		"no method":             {Domains: []string{"example.com"}},
		"two methods":           {Domains: []string{"example.com"}, Webroot: "/w", DNS: "dns_cf"},
		"bad provider":          {Domains: []string{"example.com"}, DNS: "--debug"},
		"wildcard over HTTP-01": {Domains: []string{"*.example.com"}, Webroot: "/w"},
	} {
		if _, err := c.Issue(t.Context(), req); !errors.Is(err, acme.ErrInvalidRequest) {
			t.Errorf("%s: Issue error = %v, want ErrInvalidRequest", name, err)
		}
	}
	if _, err := os.Stat(calls); err == nil {
		t.Error("acme.sh was run for an invalid request")
	}
}

func TestLocateCertNotFound(t *testing.T) {
	bin, calls := fakeAcme(t, listAndInfo)
	_, err := acme.NewClient(bin).LocateCert(t.Context(), "--uninstall")
	if !errors.Is(err, nginx.ErrCertNotFound) {
		t.Fatalf("LocateCert error = %v, want ErrCertNotFound", err)
	}
	log, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(log), "--list --listraw\n"; got != want {
		t.Errorf("acme.sh invocations = %q, want %q", got, want)
	}
}

func TestLocateCertFailure(t *testing.T) {
	bin, _ := fakeAcme(t, "echo 'something broke' >&2\nexit 3\n")
	_, err := acme.NewClient(bin).LocateCert(t.Context(), "example.com")
	if err == nil || errors.Is(err, nginx.ErrCertNotFound) {
		t.Fatalf("LocateCert error = %v, want a non-ErrCertNotFound error", err)
	}
	if !strings.Contains(err.Error(), "something broke") {
		t.Errorf("error %q does not include acme.sh's stderr", err)
	}
}

func TestLocateCertMissingConf(t *testing.T) {
	bin, _ := fakeAcme(t, strings.Replace(listAndInfo, `echo "DOMAIN_CONF=`, `echo "OTHER=`, 1))
	_, err := acme.NewClient(bin).LocateCert(t.Context(), "example.com")
	if err == nil || !strings.Contains(err.Error(), "DOMAIN_CONF") {
		t.Fatalf("LocateCert error = %v, want missing DOMAIN_CONF", err)
	}
}

func TestCertsAreCached(t *testing.T) {
	bin, calls := fakeAcme(t, listAndInfo)
	c := acme.NewClient(bin)
	for range 3 {
		if _, err := c.LocateCert(t.Context(), "example.com"); err != nil {
			t.Fatal(err)
		}
	}
	log, _ := os.ReadFile(calls)
	if got, want := string(log), "--list --listraw\n--info -d example.com\n"; got != want {
		t.Errorf("acme.sh invocations = %q, want %q", got, want)
	}
}
