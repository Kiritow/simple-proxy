package web_test

import (
	"context"
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

type noCerts struct{}

func (noCerts) LocateCert(_ context.Context, name string) (nginx.Certificate, error) {
	return nginx.Certificate{}, fmt.Errorf("%w: %s", nginx.ErrCertNotFound, name)
}

func TestHandler(t *testing.T) {
	const example = "servers: []\n"
	gen := &nginx.Generator{Certs: noCerts{}}
	h := web.NewHandler(gen, []byte(example), slog.New(slog.DiscardHandler))

	for _, tc := range []struct {
		name, method, path, body string
		wantStatus               int
		wantBody                 string // substring
	}{
		{"ui", "GET", "/", "", http.StatusOK, `<div id="root">`},
		{"example", "GET", "/api/example", "", http.StatusOK, example},
		{
			"generate", "POST", "/api/generate",
			"servers:\n- server_name: example.com\n  default: {port: 3000}\n",
			http.StatusOK, "proxy_pass http://127.0.0.1:3000;",
		},
		{"invalid config", "POST", "/api/generate", "servers: [{}]", http.StatusBadRequest, "server_name: is required"},
		{
			"missing certificate", "POST", "/api/generate",
			"servers:\n- server_name: example.com\n  https: true\n  default: {port: 3000}\n",
			http.StatusUnprocessableEntity, "certificate not found",
		},
		{"too large", "POST", "/api/generate", strings.Repeat("#", web.MaxConfigSize+1), http.StatusRequestEntityTooLarge, "too large"},
		{"wrong method", "GET", "/api/generate", "", http.StatusMethodNotAllowed, ""},
		{"unknown page", "GET", "/nope", "", http.StatusNotFound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			res := rec.Result()
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d; body:\n%s", res.StatusCode, tc.wantStatus, body)
			}
			if !strings.Contains(string(body), tc.wantBody) {
				t.Errorf("body does not contain %q:\n%s", tc.wantBody, body)
			}
		})
	}
}
