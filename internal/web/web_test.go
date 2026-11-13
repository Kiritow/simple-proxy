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
	"strings"
	"testing"

	"github.com/kiritow/simple-proxy/internal/nginx"
	"github.com/kiritow/simple-proxy/internal/store"
	"github.com/kiritow/simple-proxy/internal/web"
)

// fakeCerts knows no certificate files, but lists names (or fails to).
type fakeCerts struct {
	names []string
	err   error
}

func (fakeCerts) LocateCert(_ context.Context, name string) (nginx.Certificate, error) {
	return nginx.Certificate{}, fmt.Errorf("%w: %s", nginx.ErrCertNotFound, name)
}

func (f fakeCerts) ListCerts(context.Context) ([]string, error) { return f.names, f.err }

const (
	example     = "servers: []\n"
	validConfig = "servers:\n- server_name: example.com\n  default: {port: 3000}\n"
	httpsConfig = "servers:\n- server_name: example.com\n  https: true\n  default: {port: 3000}\n"
	badConfig   = `{"servers": [{"server_name": "a;b", "default": {"port": 70000}}]}`
)

func newHandler(t *testing.T, certs web.CertLister) http.Handler {
	t.Helper()
	sites, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "sites.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sites.Close() })
	gen := &nginx.Generator{}
	if c, ok := certs.(nginx.CertLocator); ok {
		gen.Certs = c
	}
	return web.NewHandler(web.Options{
		Generator: gen,
		Sites:     sites,
		Certs:     certs,
		Example:   []byte(example),
		Logger:    slog.New(slog.DiscardHandler),
	})
}

type request struct {
	method, path, body string
	json               bool // send Accept: application/json
}

func (req request) do(h http.Handler) (status int, contentType, body string) {
	r := httptest.NewRequest(req.method, req.path, strings.NewReader(req.body))
	if req.json {
		r.Header.Set("Accept", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	res := rec.Result()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), string(b)
}

func TestHandler(t *testing.T) {
	h := newHandler(t, fakeCerts{names: []string{"*.example.com"}})

	for _, tc := range []struct {
		name       string
		req        request
		wantStatus int
		wantType   string // prefix
		wantBody   string // substring
	}{
		{"ui", request{"GET", "/", "", false}, http.StatusOK, "text/html", `<div id="root">`},
		{"example", request{"GET", "/api/example", "", false}, http.StatusOK, "text/plain", example},
		{"certificates", request{"GET", "/api/certificates", "", false}, http.StatusOK, "application/json", `{"certificates":["*.example.com"]}`},

		{"generate text", request{"POST", "/api/generate", validConfig, false}, http.StatusOK, "text/plain", "proxy_pass http://127.0.0.1:3000;"},
		{"generate json", request{"POST", "/api/generate", validConfig, true}, http.StatusOK, "application/json", `"config":"# Auto generated`},
		{"invalid text", request{"POST", "/api/generate", badConfig, false}, http.StatusBadRequest, "text/plain", "servers[0].server_name: invalid name"},
		{
			"invalid json", request{"POST", "/api/generate", badConfig, true}, http.StatusBadRequest, "application/json",
			`"fields":[{"field":"servers[0].server_name","message":"invalid name \"a;b\""},{"field":"servers[0].default.port","message":"70000 is not a valid port"}]`,
		},
		{"syntax error json", request{"POST", "/api/generate", "servers: [", true}, http.StatusBadRequest, "application/json", `{"message":"yaml: `},
		{"missing certificate text", request{"POST", "/api/generate", httpsConfig, false}, http.StatusUnprocessableEntity, "text/plain", "certificate not found"},
		{
			"missing certificate json", request{"POST", "/api/generate", httpsConfig, true}, http.StatusUnprocessableEntity, "application/json",
			`"fields":[{"field":"servers[0].cert_name","message":"certificate not found: example.com"}]`,
		},
		{"too large", request{"POST", "/api/generate", strings.Repeat("#", web.MaxConfigSize+1), false}, http.StatusRequestEntityTooLarge, "text/plain", "too large"},

		{"validate ok", request{"POST", "/api/validate", httpsConfig, true}, http.StatusNoContent, "", ""},
		{"validate invalid", request{"POST", "/api/validate", badConfig, true}, http.StatusBadRequest, "application/json", `"field":"servers[0].default.port"`},

		{"wrong method", request{"GET", "/api/generate", "", false}, http.StatusMethodNotAllowed, "text/plain", ""},
		{"unknown page", request{"GET", "/nope", "", false}, http.StatusNotFound, "text/plain", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, contentType, body := tc.req.do(h)
			if status != tc.wantStatus {
				t.Errorf("status = %d, want %d; body:\n%s", status, tc.wantStatus, body)
			}
			if !strings.HasPrefix(contentType, tc.wantType) {
				t.Errorf("Content-Type = %q, want %q", contentType, tc.wantType)
			}
			if !strings.Contains(body, tc.wantBody) {
				t.Errorf("body does not contain %s:\n%s", tc.wantBody, body)
			}
		})
	}
}

func TestCertificatesUnavailable(t *testing.T) {
	h := newHandler(t, fakeCerts{err: errors.New("acme.sh: not found")})
	status, _, body := request{"GET", "/api/certificates", "", true}.do(h)
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "acme.sh: not found") {
		t.Errorf("got %d %s, want 503 with the error", status, body)
	}
}

func TestCertificatesNone(t *testing.T) {
	h := newHandler(t, nil)
	status, _, body := request{"GET", "/api/certificates", "", true}.do(h)
	if status != http.StatusOK || strings.TrimSpace(body) != `{"certificates":[]}` {
		t.Errorf("got %d %s, want an empty list", status, body)
	}
}

func TestSites(t *testing.T) {
	h := newHandler(t, nil)
	get := request{"GET", "/api/sites", "", true}
	put := func(body string) request { return request{"PUT", "/api/sites", body, true} }

	steps := []struct {
		name       string
		req        request
		wantStatus int
		wantBody   string
	}{
		{"empty", get, http.StatusOK, `{"revision":0,"sites":[]}`},
		{
			"save a draft", put(`{"revision": 0, "sites": [{"id": "a1", "config": {"server_name": "", "default": {"port": 70000}}}]}`),
			http.StatusOK, `{"revision":1}`,
		},
		{"load it", get, http.StatusOK, `{"revision":1,"sites":[{"id":"a1","config":{"server_name":"","default":{"port":70000}}}]}`},
		{"stale revision", put(`{"revision": 0, "sites": []}`), http.StatusConflict, "changed by someone else"},
		{"unknown field", put(`{"revision": 1, "sites": [{"id": "a1", "config": {"websockets": true}}]}`), http.StatusBadRequest, "sites[0]: config: "},
		{"not an object", put(`{"revision": 1, "sites": [{"id": "a1", "config": []}]}`), http.StatusBadRequest, "config must be a JSON object"},
		{"bad ID", put(`{"revision": 1, "sites": [{"id": "a/b", "config": {}}]}`), http.StatusBadRequest, `invalid ID \"a/b\"`},
		{"duplicate ID", put(`{"revision": 1, "sites": [{"id": "a", "config": {}}, {"id": "a", "config": {}}]}`), http.StatusBadRequest, `sites[1]: duplicate ID \"a\"`},
		{"unknown top-level field", put(`{"revision": 1, "site": []}`), http.StatusBadRequest, "unknown field"},
		{"delete all", put(`{"revision": 1, "sites": []}`), http.StatusOK, `{"revision":2}`},
		{"empty again", get, http.StatusOK, `{"revision":2,"sites":[]}`},
	}
	for _, step := range steps {
		status, _, body := step.req.do(h)
		if status != step.wantStatus || !strings.Contains(body, step.wantBody) {
			t.Fatalf("%s: got %d %s; want %d containing %s", step.name, status, body, step.wantStatus, step.wantBody)
		}
	}
}
