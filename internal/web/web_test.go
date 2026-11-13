package web_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kiritow/simple-proxy/internal/nginx"
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

func newHandler(certs fakeCerts) http.Handler {
	gen := &nginx.Generator{Certs: certs}
	return web.NewHandler(gen, certs, []byte(example), slog.New(slog.DiscardHandler))
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
	h := newHandler(fakeCerts{names: []string{"*.example.com"}})

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
	h := newHandler(fakeCerts{err: errors.New("acme.sh: not found")})
	status, _, body := request{"GET", "/api/certificates", "", true}.do(h)
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "acme.sh: not found") {
		t.Errorf("got %d %s, want 503 with the error", status, body)
	}
}

func TestCertificatesNone(t *testing.T) {
	gen := &nginx.Generator{}
	h := web.NewHandler(gen, nil, nil, slog.New(slog.DiscardHandler))
	status, _, body := request{"GET", "/api/certificates", "", true}.do(h)
	if status != http.StatusOK || strings.TrimSpace(body) != `{"certificates":[]}` {
		t.Errorf("got %d %s, want an empty list", status, body)
	}
}
