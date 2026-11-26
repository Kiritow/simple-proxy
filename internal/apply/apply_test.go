package apply_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kiritow/simple-proxy/internal/apply"
)

// The fake nginx accepts configurations without the word "bad".
func newApplier(t *testing.T, reload []string) (*apply.Applier, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "proxy.conf")
	test := []string{"sh", "-c", `if grep -q bad "$0"; then echo "nginx: [emerg] bad config" >&2; exit 1; fi`, path}
	return &apply.Applier{Path: path, Test: test, Reload: reload}, path
}

func content(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApply(t *testing.T) {
	a, path := newApplier(t, []string{"true"})
	if err := a.Apply(t.Context(), []byte("good 1")); err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(t.Context(), []byte("good 2")); err != nil {
		t.Fatal(err)
	}
	if got := content(t, path); got != "good 2" {
		t.Errorf("file = %q, want %q", got, "good 2")
	}
}

func TestApplyKeepsTheFile(t *testing.T) {
	// The file is rewritten in place, so bind mounts of it keep working.
	a, path := newApplier(t, nil)
	if err := a.Apply(t.Context(), []byte("good 1")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if err := a.Apply(t.Context(), []byte("good 2")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) {
		t.Error("Apply replaced the file instead of rewriting it")
	}
}

func TestApplyRestoresAfterFailedTest(t *testing.T) {
	a, path := newApplier(t, []string{"true"})
	if err := a.Apply(t.Context(), []byte("good")); err != nil {
		t.Fatal(err)
	}
	err := a.Apply(t.Context(), []byte("bad"))
	var aerr *apply.Error
	if !errors.As(err, &aerr) || aerr.Step != "test" {
		t.Fatalf("Apply error = %v, want a test failure", err)
	}
	if !strings.Contains(aerr.Output, "[emerg] bad config") {
		t.Errorf("Output = %q, want nginx's message", aerr.Output)
	}
	if got := content(t, path); got != "good" {
		t.Errorf("file = %q, want the previous configuration", got)
	}
}

// realNginx fails like nginx -t: it prints the error to its log on stderr
// and as a summary, pointing at line 2 of the file, which nginx may see at
// another path, e.g. in a container.
const realNginx = `msg='unknown directive "bad" in /etc/nginx/conf.d/proxy.conf:2'
echo "2026/11/26 18:59:16 [warn] 183#183: the \"listen ... http2\" directive is deprecated" >&2
echo "2026/11/26 18:59:16 [emerg] 183#183: $msg" >&2
echo "nginx: [emerg] $msg" >&2
echo "nginx: configuration file /etc/nginx/nginx.conf test failed" >&2
exit 1`

func TestErrorMessage(t *testing.T) {
	a, _ := newApplier(t, nil)
	a.Test = []string{"sh", "-c", realNginx}
	err := a.Apply(t.Context(), []byte("server {\n    bad on;\n}\n"))
	var aerr *apply.Error
	if !errors.As(err, &aerr) {
		t.Fatalf("Apply error = %v, want an *apply.Error", err)
	}
	want := `test failed: unknown directive "bad" in /etc/nginx/conf.d/proxy.conf:2 (bad on;)`
	if got := aerr.Message(); got != want {
		t.Errorf("Message() = %q\nwant        %q", got, want)
	}
	if !strings.Contains(aerr.Output, "test failed") {
		t.Errorf("Output = %q, want all of nginx's output", aerr.Output)
	}

	// Without messages of nginx, the output tells, or else the error.
	for _, c := range []struct {
		output, want string
	}{
		{"Error: no container with name or ID \"nginx\" found\nmore", `reload failed: Error: no container with name or ID "nginx" found`},
		{"", "reload failed: podman exec nginx nginx -s reload: exit status 125"},
		{"nginx: [error] open() \"/run/nginx.pid\" failed (2: No such file or directory)", `reload failed: open() "/run/nginx.pid" failed (2: No such file or directory)`},
	} {
		e := &apply.Error{Step: "reload", Output: c.output, Err: errors.New("podman exec nginx nginx -s reload: exit status 125")}
		if got := e.Message(); got != c.want {
			t.Errorf("Message() with output %q = %q, want %q", c.output, got, c.want)
		}
	}
}

func TestApplyRemovesNewFileAfterFailedTest(t *testing.T) {
	a, path := newApplier(t, nil)
	if err := a.Apply(t.Context(), []byte("bad")); err == nil {
		t.Fatal("Apply succeeded")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the rejected file is still there: %v", err)
	}
}

func TestApplyRestoresAfterFailedReload(t *testing.T) {
	a, path := newApplier(t, nil)
	if err := a.Apply(t.Context(), []byte("good")); err != nil {
		t.Fatal(err)
	}
	a.Reload = []string{"sh", "-c", "echo 'nginx: [error] open() \"/run/nginx.pid\" failed'; exit 1"}
	err := a.Apply(t.Context(), []byte("good 2"))
	var aerr *apply.Error
	if !errors.As(err, &aerr) || aerr.Step != "reload" || !strings.Contains(aerr.Output, "nginx.pid") {
		t.Fatalf("Apply error = %v, want a reload failure with its output", err)
	}
	if got := content(t, path); got != "good" {
		t.Errorf("file = %q, want the previous configuration", got)
	}
}

// nginxV prints like nginx -V, to stderr.
const nginxV = `echo "nginx version: nginx/1.27.3
built by gcc 12.2.0 (Debian 12.2.0-14)
built with OpenSSL 3.0.15 3 Sep 2024
TLS SNI support enabled
configure arguments: --prefix=/etc/nginx --with-http_ssl_module" >&2`

func TestNginx(t *testing.T) {
	a, _ := newApplier(t, nil)
	if _, err := a.Nginx(t.Context()); !errors.Is(err, apply.ErrNoVersion) {
		t.Errorf("Nginx without a Version command: %v, want ErrNoVersion", err)
	}

	a.Version = []string{"sh", "-c", nginxV}
	got, err := a.Nginx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "nginx/1.27.3" || !strings.HasPrefix(got.Build, "built by gcc") || !strings.HasSuffix(got.Build, "--with-http_ssl_module") {
		t.Errorf("Nginx = %+v", got)
	}

	a.Version = []string{"sh", "-c", "echo 'nginx version: openresty/1.25.3.1' >&2"}
	if got, err := a.Nginx(t.Context()); err != nil || got.Version != "openresty/1.25.3.1" || got.Build != "" {
		t.Errorf("Nginx of OpenResty = %+v, %v", got, err)
	}

	for _, cmd := range []string{"echo hello", "echo 'sh: nginx: not found' >&2; exit 127"} {
		a.Version = []string{"sh", "-c", cmd}
		if got, err := a.Nginx(t.Context()); err == nil {
			t.Errorf("Nginx with %q = %+v, want an error", cmd, got)
		}
	}
}
