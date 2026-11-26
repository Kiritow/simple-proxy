package web_test

import (
	"context"
	"encoding/json"
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
	"github.com/kiritow/simple-proxy/internal/certs"
	"github.com/kiritow/simple-proxy/internal/certs/certstest"
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
	opts := manager.Options{Store: s, Generator: gen, CertDir: certs.Dir(filepath.Join(dir, "certs"))}
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
		Manager: manager.New(opts),
		Example: []byte(example),
		Logger:  slog.New(slog.DiscardHandler),
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
		{"missing certificate", "POST", "/api/sites", `{"config": {"server_name": "b.example.com", "https": true, "default": {"port": 1}}}`, false, 422, `"field":"servers[0].cert_name","message":"certificate not found: there is no certificate named \"b.example.com\"","code":"cert_not_found"`},
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
		{"preview", "POST", "/api/sites/preview", `{"id": "` + id + `", "config": {"server_name": "a.example.com", "default": {"port": 2}}}`, false, 200, `"generation":1,`},
		{"preview shows the change", "POST", "/api/sites/preview", `{"id": "` + id + `", "config": {"server_name": "a.example.com", "default": {"port": 2}}}`, false, 200, `proxy_pass http://127.0.0.1:2;`},
		{"preview of a new site", "POST", "/api/sites/preview", `{"config": {"server_name": "b.example.com", "default": {"port": 3}}}`, false, 200, `"generation":0`},
		{"preview of the site alone", "POST", "/api/sites/preview", `{"config": {"server_name": "b.example.com", "default": {"port": 3}}}`, false, 200, `"site":"server {`},
		{"preview problems", "POST", "/api/sites/preview", `{"config": {"server_name": "A.example.com", "default": {"port": 3}}}`, false, 400, `"code":"domain_in_use"`},
		{"preview of a missing site", "POST", "/api/sites/preview", `{"id": "nope", "config": {"server_name": "x.example.com"}}`, false, 404, "site not found"},
		{"update", "PUT", "/api/sites/" + id, `{"config": {"server_name": "a.example.com", "default": {"port": 2}}, "generation": 1}`, false, 200, `"port":2}},"generation":2}`},
		{"stale update", "PUT", "/api/sites/" + id, `{"config": {"server_name": "a.example.com", "default": {"port": 3}}, "generation": 1}`, false, 409, "changed by someone else"},
		{"listed", "GET", "/api/sites", "", false, 200, `"id":"` + id + `","config":{"server_name":"a.example.com","default":{"port":2}},"generation":2`},
		{"stale delete", "DELETE", "/api/sites/" + id + "?generation=1", "", false, 409, "changed by someone else"},
		{"bad generation", "DELETE", "/api/sites/" + id + "?generation=x", "", false, 400, "generation must be a number"},
		{"delete", "DELETE", "/api/sites/" + id + "?generation=2", "", false, 204, ""},
		{"gone", "GET", "/api/sites", "", false, 200, `{"sites":[]}`},
	})
}

func TestCertificates(t *testing.T) {
	run(t, newHandler(t, options{ca: true, webroot: true}), []step{
		{"list", "GET", "/api/certificates", "", false, 200, `{"certificates":[{"name":"*.example.com","source":"acme","domains":["*.example.com"],"key":"","issuer":"ZeroSSL.com"}]}`},
		{"issue over HTTP", "POST", "/api/certificates", `{"domains": ["a.example.com"]}`, false, 201, `{"output":"Cert success."}`},
		{"issue over DNS", "POST", "/api/certificates", `{"domains": ["*.example.org"], "dns": "dns_cf"}`, false, 201, "Cert success."},
		{"failure", "POST", "/api/certificates", `{"domains": ["fail.example.com"]}`, false, 502, `"output":"Verify error: 404"`},
	})
	run(t, newHandler(t, options{ca: true}), []step{
		{"no webroot", "POST", "/api/certificates", `{"domains": ["a.example.com"]}`, false, 400, "ACME webroot"},
	})
	run(t, newHandler(t, options{}), []step{
		{"no acme.sh: list", "GET", "/api/certificates", "", false, 200, `{"certificates":[]}`},
		{"no acme.sh: issue", "POST", "/api/certificates", `{"domains": ["a.example.com"], "dns": "dns_cf"}`, false, 503, "acme.sh is not available"},
		{"status", "GET", "/api/status", "", false, 200, `"applies":false,"acme":false`},
	})
}

func TestLocalCertificates(t *testing.T) {
	caCert, caKey := certstest.CA(t, "Home CA")
	leafCert, leafKey := certstest.Leaf(t, caCert, caKey, "app.example.com")
	body := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	run(t, newHandler(t, options{applies: true}), []step{
		{"upload", "POST", "/api/certificates/upload", body(map[string]string{"cert": leafCert, "key": leafKey}), false, 201, `"name":"app.example.com","source":"uploaded","domains":["app.example.com"],"key":"EC P-256","issuer":"Home CA"`},
		{"upload problems", "POST", "/api/certificates/upload", body(map[string]string{"cert": leafCert}), false, 400,
			`"fields":[{"field":"key","message":"contains no PEM private key","code":"no_key"},{"field":"name","message":"the name \"app.example.com\" is taken","code":"name_in_use","value":"app.example.com"}]`},
		{"preview", "POST", "/api/certificates/upload/preview", body(map[string]string{"name": "Shop / 2026", "cert": leafCert}), false, 200,
			`"name":"Shop / 2026","subject":"app.example.com","issuer":"Home CA","domains":["app.example.com"],"key":"EC P-256"`},
		{"preview problems", "POST", "/api/certificates/upload/preview", body(map[string]string{"cert": leafCert}), false, 200,
			`"has_key":false,"problems":[{"field":"key","message":"contains no PEM private key","code":"no_key"},{"field":"name"`},
		{"empty preview", "POST", "/api/certificates/upload/preview", `{}`, false, 200, `"domains":[],"key":"","chain":[],"has_key":false,"problems":[{"field":"cert"`},
		{"descriptive name", "POST", "/api/certificates/upload", body(map[string]string{"name": "Shop / 2026", "cert": leafCert, "key": leafKey}), false, 201, `"name":"Shop / 2026"`},
		{"delete it", "DELETE", "/api/certificates/Shop%20%2F%202026", "", false, 204, ""},
		{"as raw", "POST", "/api/sites/raw", `{"config": {"server_name": "app.example.com", "https": true, "default": {"port": 1}}}`, false, 200,
			`ssl_certificate {{cert \"app.example.com\"}};`},
		{"raw problems", "POST", "/api/sites", `{"config": {"raw": "server {"}}`, false, 400, `"field":"servers[0].raw","message":"line 1: the block opened here is not closed","code":"raw_unclosed_block","value":"1"`},
		{"listed without keys", "GET", "/api/certificates", "", false, 200, `"source":"uploaded"`},
		{"site", "POST", "/api/sites", `{"config": {"server_name": "app.example.com", "https": true, "default": {"port": 1}}}`, false, 201, ""},
		{"in use", "DELETE", "/api/certificates/app.example.com", "", false, 400, `"code":"cert_in_use","value":"app.example.com"`},
		{"missing", "DELETE", "/api/certificates/nope", "", false, 404, "certificate not found"},
		{"details", "GET", "/api/certificates/app.example.com", "", false, 200, `"subject":"app.example.com"`},
		{"details of sites", "GET", "/api/certificates/app.example.com", "", false, 200, `"name":"app.example.com","enabled":true}]`},
		{"missing details", "GET", "/api/certificates/nope", "", false, 404, "certificate not found"},
		{"rename", "PATCH", "/api/certificates/app.example.com", `{"name": "App / 2026"}`, false, 200, `"name":"App / 2026","source":"uploaded"`},
		{"renamed", "GET", "/api/certificates/App%20%2F%202026", "", false, 200, `"name":"App / 2026"`},
		{"site follows", "GET", "/api/sites", "", false, 200, `"cert_name":"App / 2026"`},
		{"rename problems", "PATCH", "/api/certificates/App%20%2F%202026", `{"name": ""}`, false, 400, `"field":"name","message":"is required","code":"required"`},
		{"rename missing", "PATCH", "/api/certificates/nope", `{"name": "x"}`, false, 404, "certificate not found"},
		{"rename unknown field", "PATCH", "/api/certificates/App%20%2F%202026", `{"title": "x"}`, false, 400, "unknown field"},
		{"CA preview", "POST", "/api/authorities/preview", body(map[string]string{"cert": caCert, "key": caKey}), false, 200, `"name":"Home CA","subject":"Home CA","issuer":"Home CA"`},
		{"add CA", "POST", "/api/authorities", body(map[string]string{"cert": caCert, "key": caKey}), false, 201, `"name":"Home CA","subject":"Home CA","issuer":"Home CA","key":"EC P-256"`},
		{"sign preview", "POST", "/api/certificates/sign/preview", `{"authority": "Home CA", "domains": ["nas.lan"], "days": 30, "key_type": "rsa"}`, false, 200,
			`"name":"nas.lan","subject":"nas.lan","issuer":"Home CA","domains":["nas.lan"],"key":"RSA 2048"`},
		{"replace CA", "POST", "/api/authorities", body(map[string]any{"cert": caCert, "key": caKey, "replace": true}), false, 400, "cannot be replaced"},
		{"sign", "POST", "/api/certificates/sign", `{"authority": "Home CA", "domains": ["nas.lan"], "days": 30, "key_type": "ec"}`, false, 201, `"name":"nas.lan","source":"signed"`},
		{"sign problems", "POST", "/api/certificates/sign", `{"authority": "Home CA", "domains": ["a b"], "days": 0, "key_type": "ec"}`, false, 400, `"code":"invalid_domain","value":"a b"},{"field":"days"`},
		{"CAs", "GET", "/api/authorities", "", false, 200, `"signed":1`},
		{"CA certificate", "GET", "/api/authorities/Home%20CA/cert.pem", "", false, 200, "-----BEGIN CERTIFICATE-----"},
		{"missing CA certificate", "GET", "/api/authorities/nope/cert.pem", "", false, 404, "CA not found"},
		{"CA details", "GET", "/api/authorities/Home%20CA", "", false, 200, `"certificates":["nas.lan"]`},
		{"missing CA details", "GET", "/api/authorities/nope", "", false, 404, "CA not found"},
		{"rename CA", "PATCH", "/api/authorities/Home%20CA", `{"name": "Home CA 2026"}`, false, 200, `"name":"Home CA 2026","subject":"Home CA"`},
		{"signed follows", "GET", "/api/certificates", "", false, 200, `"ca":"Home CA 2026"`},
		{"rename missing CA", "PATCH", "/api/authorities/Home%20CA", `{"name": "x"}`, false, 404, "CA not found"},
		{"delete CA", "DELETE", "/api/authorities/Home%20CA%202026", "", false, 204, ""},
		{"delete signed", "DELETE", "/api/certificates/nas.lan", "", false, 204, ""},
	})
	// No response contains a private key.
	h := newHandler(t, options{})
	for _, path := range []string{"/api/certificates/upload", "/api/authorities"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("POST", path, strings.NewReader(body(map[string]string{"cert": caCert, "key": caKey}))))
		if strings.Contains(r.Body.String(), "PRIVATE") {
			t.Errorf("%s answered with a private key: %s", path, r.Body)
		}
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/certificates/upload", strings.NewReader(body(map[string]string{"cert": leafCert, "key": leafKey}))))
	for _, path := range []string{"/api/certificates/app.example.com", "/api/authorities/Home%20CA"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 || strings.Contains(r.Body.String(), "PRIVATE") {
			t.Errorf("GET %s = %d %s, want details without a private key", path, r.Code, r.Body)
		}
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/api/authorities/Home%20CA/cert.pem", nil))
	if cd := r.Header().Get("Content-Disposition"); cd != `attachment; filename="Home CA.crt"` || strings.Contains(r.Body.String(), "PRIVATE") {
		t.Errorf("CA certificate: %s, %s", cd, r.Body)
	}
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
