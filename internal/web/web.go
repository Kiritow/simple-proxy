// Package web serves the browser UI and the HTTP API of simple-proxy.
//
// The UI is the React application in the web directory of the repository,
// built into dist by `go generate` (which requires `npm ci` in web first).
package web

//go:generate npm --prefix ../../web run build

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/nginx"
)

// MaxConfigSize is the size limit of submitted configurations, in bytes.
const MaxConfigSize = 1 << 20

//go:embed dist
var dist embed.FS

// NewHandler returns a handler serving
//
//	GET  /              the browser UI
//	GET  /api/example   the example configuration
//	POST /api/generate  the nginx configuration generated from the YAML or
//	                    JSON configuration in the request body
//
// API responses are plain text. Errors are reported with a 4xx or 5xx status
// code and a human-readable message.
func NewHandler(gen *nginx.Generator, example []byte, logger *slog.Logger) http.Handler {
	ui, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded, so this cannot happen
	}
	files := http.FileServerFS(ui)
	h := &handler{gen: gen, log: logger}

	mux := http.NewServeMux()
	// Only the index page and the build output are routed to the file server,
	// so that requests with the wrong method to the API get a 405.
	mux.Handle("GET /{$}", files)
	mux.Handle("GET /assets/", immutable(files))
	mux.HandleFunc("GET /api/example", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(example)
	})
	mux.HandleFunc("POST /api/generate", h.generate)
	return mux
}

// immutable marks responses as cacheable forever, which is safe for Vite's
// build output because the file names contain content hashes.
func immutable(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

type handler struct {
	gen *nginx.Generator
	log *slog.Logger
}

func (h *handler) generate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxConfigSize))
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}

	cfg, err := config.Parse(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	conf, err := h.gen.Generate(r.Context(), cfg)
	switch {
	case errors.Is(err, nginx.ErrCertNotFound):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case err != nil:
		h.log.ErrorContext(r.Context(), "generating nginx config", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(conf)
}
