package acme_test

import (
	"errors"
	"os"
	"path/filepath"
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
	got, err := acme.NewLocator(bin).LocateCert(t.Context(), "*.example.org")
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

func TestLocateCertNotFound(t *testing.T) {
	bin, calls := fakeAcme(t, listAndInfo)
	_, err := acme.NewLocator(bin).LocateCert(t.Context(), "--uninstall")
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
	_, err := acme.NewLocator(bin).LocateCert(t.Context(), "example.com")
	if err == nil || errors.Is(err, nginx.ErrCertNotFound) {
		t.Fatalf("LocateCert error = %v, want a non-ErrCertNotFound error", err)
	}
	if !strings.Contains(err.Error(), "something broke") {
		t.Errorf("error %q does not include acme.sh's stderr", err)
	}
}

func TestLocateCertMissingConf(t *testing.T) {
	bin, _ := fakeAcme(t, strings.Replace(listAndInfo, `echo "DOMAIN_CONF=`, `echo "OTHER=`, 1))
	_, err := acme.NewLocator(bin).LocateCert(t.Context(), "example.com")
	if err == nil || !strings.Contains(err.Error(), "DOMAIN_CONF") {
		t.Fatalf("LocateCert error = %v, want missing DOMAIN_CONF", err)
	}
}
