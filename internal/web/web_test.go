package web_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kiritow/simple-proxy/internal/acme"
	"github.com/kiritow/simple-proxy/internal/apply"
	"github.com/kiritow/simple-proxy/internal/manager"
	"github.com/kiritow/simple-proxy/internal/nginx"
	"github.com/kiritow/simple-proxy/internal/store"
	"github.com/kiritow/simple-proxy/internal/web"
)

// fakeCA knows the certificate "*.example.com" and fails to issue for
// fail.example.com.
type fakeCA struct{}

func (fakeCA) LocateCert(_ context.Context, name string) (nginx.Certificate, error) {
	if name != "*.example.com" {
		return nginx.Certificate{}, fmt.Errorf("%w: %s", nginx.ErrCertNotFound, name)
	}
	return nginx.Certificate{Fullchain: "/c/full.cer", Key: "/c/key", CA: "/c/ca.cer"}, nil
}

func (fakeCA) Certs(context.Context) ([]acme.Cert, error) {
	return []acme.Cert{{Name: "*.example.com", Domains: []string{"*.example.com"}, CA: "ZeroSSL.com"}}, nil
}

func (fakeCA) Issue(_ context.Context, req acme.IssueRequest) (string, error) {
	if req.Domains[0] == "fail.example.com" {
		return "Verify error: 404", errors.New("acme.sh --issue: exit status 1")
	}
	return "Cert success.", nil
}

const example = "servers: []\n"

type options struct {
	applies bool // with a fake nginx rejecting "reject_me"
	ca      bool
	webroot bool
}

func newHandler(t *testing.T, o options) http.Handler {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(t.Context(), filepath.Join(dir, "sites.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	gen := &nginx.Generator{}
	opts := manager.Options{Store: s, Generator: gen}
	if o.ca {
		gen.Certs, opts.CA = fakeCA{}, fakeCA{}
	}
	if o.webroot {
		gen.ACMEWebroot, opts.ACMEWebroot = "/var/www/acme", "/var/www/acme"
	}
	if o.applies {
		if runtime.GOOS == "windows" {
			t.Skip("needs a POSIX shell")
		}
		conf := filepath.Join(dir, "proxy.conf")
		opts.Applier = &apply.Applier{
			Path: conf,
			Test: []string{"sh", "-c", `if grep -q reject_me "$0"; then echo "nginx: [emerg] unknown directive" >&2; exit 1; fi`, conf},
		}
	}
	return web.NewHandler(web.Options{
		Manager:   manager.New(opts),
		Generator: gen,
		Example:   []byte(example),
		Logger:    slog.New(slog.DiscardHandler),
	})
}

type step struct {
	name, method, path, body string
	json                     bool // send Accept: application/json
	wantStatus               int
	wantBody                 string // substring
}

func run(t *testing.T, h http.Handler, steps []step) {
	t.Helper()
	for _, s := range steps {
		r := httptest.NewRequest(s.method, s.path, strings.NewReader(s.body))
		if s.json {
			r.Header.Set("Accept", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		body, _ := io.ReadAll(rec.Result().Body)
		if rec.Code != s.wantStatus || !strings.Contains(string(body), s.wantBody) {
			t.Errorf("%s: %s %s = %d %s\nwant %d containing %s", s.name, s.method, s.path, rec.Code, body, s.wantStatus, s.wantBody)
		}
	}
}

func TestSites(t *testing.T) {
	h := newHandler(t, options{applies: true, ca: true})
	run(t, h, []step{
		{"empty", "GET", "/api/sites", "", false, 200, `{"sites":[]}`},
		{"create", "POST", "/api/sites", `{"config": {"server_name": "a.example.com", "default": {"port": 3000}}}`, false, 201, `"config":{"server_name":"a.example.com","default":{"port":3000}}`},
		{"invalid", "POST", "/api/sites", `{"config": {"server_name": "b;", "default": {"port": 70000}}}`, false, 400,
			`"fields":[{"field":"servers[0].server_name","message":"invalid name \"b;\"","code":"invalid_name","value":"b;"},{"field":"servers[0].default.port","message":"70000 is not a valid port","code":"invalid_port","value":"70000"}]`},
		{"domain in use", "POST", "/api/sites", `{"config": {"server_name": "a.example.com", "default": {"port": 1}}}`, false, 400, `"code":"domain_in_use","value":"a.example.com"`},
		{"missing certificate", "POST", "/api/sites", `{"config": {"server_name": "b.example.com", "https": true, "default": {"port": 1}}}`, false, 422, `"field":"servers[0].cert_name","message":"certificate not found: b.example.com","code":"cert_not_found"`},
		{"https", "POST", "/api/sites", `{"config": {"server_name": "c.example.com", "cert_name": "*.example.com", "default": {"port": 1}}}`, false, 201, `"cert_name":"*.example.com"`},
		{"rejected by nginx", "POST", "/api/sites", `{"config": {"server_name": "d.example.com", "options": ["reject_me on"], "default": {"port": 1}}}`, false, 502, `"output":"nginx: [emerg] unknown directive"`},
		{"unknown schema field", "POST", "/api/sites", `{"config": {"server_name": "d.example.com", "websockets": true}}`, false, 400, "field websockets not found"},
		{"not an object", "POST", "/api/sites", `{"config": []}`, false, 400, "config must be a JSON object"},
		{"unknown request field", "POST", "/api/sites", `{"conf": {}}`, false, 400, "unknown field"},
		{"nginx.conf", "GET", "/api/nginx.conf", "", false, 200, "server_name c.example.com;"},
		{"update missing", "PUT", "/api/sites/nope", `{"config": {"server_name": "x.example.com", "default": {"port": 1}}}`, false, 404, "site not found"},
		{"delete missing", "DELETE", "/api/sites/nope", "", false, 404, "site not found"},
		{"replace all", "PUT", "/api/sites", `{"configs": [{"server_name": "x.example.com", "default": {"port": 1}}]}`, false, 200, `"server_name":"x.example.com"`},
		{"replaced", "GET", "/api/nginx.conf", "", false, 200, "server_name x.example.com;"},
		{"status", "GET", "/api/status", "", false, 200, `"applies":true`},
		{"apply", "POST", "/api/apply", "", false, 200, `"applied_at":`},
	})
}

func TestUpdateAndDelete(t *testing.T) {
	h := newHandler(t, options{})
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/api/sites", strings.NewReader(`{"config": {"server_name": "a.example.com", "default": {"port": 1}}}`)))
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Body)
	}
	var id string
	if _, err := fmt.Sscanf(r.Body.String(), `{"id":%q`, &id); err != nil {
		t.Fatalf("no id in %s: %v", r.Body, err)
	}
	run(t, h, []step{
		{"update", "PUT", "/api/sites/" + id, `{"config": {"server_name": "a.example.com", "default": {"port": 2}}}`, false, 200, `"port":2`},
		{"listed", "GET", "/api/sites", "", false, 200, `"id":"` + id + `","config":{"server_name":"a.example.com","default":{"port":2}}`},
		{"delete", "DELETE", "/api/sites/" + id, "", false, 204, ""},
		{"gone", "GET", "/api/sites", "", false, 200, `{"sites":[]}`},
	})
}

func TestCertificates(t *testing.T) {
	run(t, newHandler(t, options{ca: true, webroot: true}), []step{
		{"list", "GET", "/api/certificates", "", false, 200, `{"certificates":[{"name":"*.example.com","domains":["*.example.com"],"key_length":"","ca":"ZeroSSL.com","created":"","renew":""}]}`},
		{"issue over HTTP", "POST", "/api/certificates", `{"domains": ["a.example.com"]}`, false, 201, `{"output":"Cert success."}`},
		{"issue over DNS", "POST", "/api/certificates", `{"domains": ["*.example.org"], "dns": "dns_cf"}`, false, 201, "Cert success."},
		{"failure", "POST", "/api/certificates", `{"domains": ["fail.example.com"]}`, false, 502, `"output":"Verify error: 404"`},
	})
	run(t, newHandler(t, options{ca: true}), []step{
		{"no webroot", "POST", "/api/certificates", `{"domains": ["a.example.com"]}`, false, 400, "ACME webroot"},
	})
	run(t, newHandler(t, options{}), []step{
		{"no acme.sh: list", "GET", "/api/certificates", "", false, 503, "acme.sh is not configured"},
		{"no acme.sh: issue", "POST", "/api/certificates", `{"domains": ["a.example.com"], "dns": "dns_cf"}`, false, 503, "acme.sh is not configured"},
		{"status", "GET", "/api/status", "", false, 200, `"applies":false,"certificates":false`},
	})
}

func TestStatelessAPI(t *testing.T) {
	const (
		valid = "servers:\n- server_name: example.com\n  default: {port: 3000}\n"
		bad   = `{"servers": [{"server_name": "a;b", "default": {"port": 70000}}]}`
	)
	run(t, newHandler(t, options{}), []step{
		{"ui", "GET", "/", "", false, 200, `<div id="root">`},
		{"example", "GET", "/api/example", "", false, 200, example},
		{"generate text", "POST", "/api/generate", valid, false, 200, "proxy_pass http://127.0.0.1:3000;"},
		{"generate json", "POST", "/api/generate", valid, true, 200, `"config":"# Auto generated`},
		{"invalid text", "POST", "/api/generate", bad, false, 400, "servers[0].server_name: invalid name"},
		{"invalid json", "POST", "/api/generate", bad, true, 400, `"code":"invalid_name"`},
		{"syntax error", "POST", "/api/generate", "servers: [", true, 400, `{"message":"yaml: `},
		{"validate ok", "POST", "/api/validate", valid, true, 204, ""},
		{"validate invalid", "POST", "/api/validate", bad, true, 400, `"field":"servers[0].default.port"`},
		{"too large", "POST", "/api/generate", strings.Repeat("#", web.MaxConfigSize+1), false, 413, "too large"},
		{"wrong method", "GET", "/api/generate", "", false, 405, ""},
		{"unknown page", "GET", "/nope", "", false, 404, ""},
	})
}
