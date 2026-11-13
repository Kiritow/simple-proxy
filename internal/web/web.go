// Package web serves the browser UI and the HTTP API of simple-proxy.
//
// The UI is the React application in the web directory of the repository,
// built into dist by `go generate` (which requires `npm ci` in web first).
package web

//go:generate npm --prefix ../../web run build

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/nginx"
	"github.com/kiritow/simple-proxy/internal/store"
)

// MaxConfigSize is the size limit of submitted configurations, in bytes.
const MaxConfigSize = 1 << 20

//go:embed dist
var dist embed.FS

// CertLister lists the names of the certificates available to HTTPS servers.
type CertLister interface {
	ListCerts(ctx context.Context) ([]string, error)
}

// Options configure the handler returned by NewHandler.
type Options struct {
	Generator *nginx.Generator
	Sites     *store.Store
	Certs     CertLister // nil if no certificates are available
	Example   []byte     // the example configuration
	Logger    *slog.Logger
}

// NewHandler returns a handler serving
//
//	GET  /                  the browser UI
//	GET  /api/sites         the stored sites and their revision (JSON)
//	PUT  /api/sites         replace the stored sites (JSON)
//	GET  /api/example       the example configuration (YAML)
//	GET  /api/certificates  the certificate names listed by Certs (JSON)
//	POST /api/validate      204 if the configuration in the body is valid
//	POST /api/generate      the nginx configuration generated from the body
//
// Configurations are YAML or JSON. Responses of validate and generate are
// plain text, unless the request accepts application/json: then generate
// answers {"config": "..."}, and errors are reported as
//
//	{"message": "...", "fields": [{"field": "servers[0].default.port", "message": "...", "code": "invalid_port", "value": "70000"}]}
//
// where code is a config.ProblemCode, or "cert_not_found", and identifies
// the problem for clients that describe it in their own words.
//
// Sites are exchanged as {"revision": 1, "sites": [{"id": "...", "config": {...}}]},
// where each config is one entry of servers. A PUT must carry the revision
// it is based on, and fails with 409 Conflict if the sites have been changed
// since then.
func NewHandler(opts Options) http.Handler {
	ui, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded, so this cannot happen
	}
	files := http.FileServerFS(ui)
	h := &handler{gen: opts.Generator, sites: opts.Sites, certs: opts.Certs, log: opts.Logger}
	example := opts.Example

	mux := http.NewServeMux()
	// Only the index page and the build output are routed to the file server,
	// so that requests with the wrong method to the API get a 405.
	mux.Handle("GET /{$}", files)
	mux.Handle("GET /assets/", immutable(files))
	mux.HandleFunc("GET /api/example", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(example)
	})
	mux.HandleFunc("GET /api/sites", h.getSites)
	mux.HandleFunc("PUT /api/sites", h.putSites)
	mux.HandleFunc("GET /api/certificates", h.certificates)
	mux.HandleFunc("POST /api/validate", h.validate)
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
	gen   *nginx.Generator
	sites *store.Store
	certs CertLister
	log   *slog.Logger
}

type sitesDoc struct {
	Revision int64        `json:"revision"`
	Sites    []store.Site `json:"sites"`
}

func (h *handler) getSites(w http.ResponseWriter, r *http.Request) {
	sites, revision, err := h.sites.Sites(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "loading sites", "err", err)
		writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sitesDoc{revision, sites})
}

var siteID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (h *handler) putSites(w http.ResponseWriter, r *http.Request) {
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var doc sitesDoc
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return
	}
	// Sites are drafts, so they are only checked to match the schema.
	seen := make(map[string]bool, len(doc.Sites))
	for i, site := range doc.Sites {
		err := checkSite(site)
		if err == nil && seen[site.ID] {
			err = fmt.Errorf("duplicate ID %q", site.ID)
		}
		if err != nil {
			writeError(w, r, http.StatusBadRequest, fmt.Errorf("sites[%d]: %w", i, err))
			return
		}
		seen[site.ID] = true
	}

	revision, err := h.sites.ReplaceSites(r.Context(), doc.Sites, doc.Revision)
	switch {
	case errors.Is(err, store.ErrConflict):
		writeError(w, r, http.StatusConflict, err)
		return
	case err != nil:
		h.log.ErrorContext(r.Context(), "storing sites", "err", err)
		writeError(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revision": revision})
}

func checkSite(site store.Site) error {
	if !siteID.MatchString(site.ID) {
		return fmt.Errorf("invalid ID %q", site.ID)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(site.Config, &object); err != nil {
		return errors.New("config must be a JSON object")
	}
	if _, err := config.DecodeServer(site.Config); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

func (h *handler) certificates(w http.ResponseWriter, r *http.Request) {
	names := []string{} // encoded as [] rather than null
	if h.certs != nil {
		listed, err := h.certs.ListCerts(r.Context())
		if err != nil {
			h.log.WarnContext(r.Context(), "listing certificates", "err", err)
			writeError(w, r, http.StatusServiceUnavailable, err)
			return
		}
		names = append(names, listed...)
	}
	writeJSON(w, http.StatusOK, map[string][]string{"certificates": names})
}

func (h *handler) validate(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.parse(w, r); ok {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *handler) generate(w http.ResponseWriter, r *http.Request) {
	cfg, ok := h.parse(w, r)
	if !ok {
		return
	}

	conf, err := h.gen.Generate(r.Context(), cfg)
	switch {
	case errors.Is(err, nginx.ErrCertNotFound):
		writeError(w, r, http.StatusUnprocessableEntity, err)
		return
	case err != nil:
		h.log.ErrorContext(r.Context(), "generating nginx config", "err", err)
		writeError(w, r, http.StatusInternalServerError, err)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]string{"config": string(conf)})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(conf)
}

// readBody returns the request body. If it cannot be read or is too large,
// readBody writes an error response and returns false.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxConfigSize))
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, r, status, err)
		return nil, false
	}
	return body, true
}

// parse returns the configuration in the request body. If it is missing or
// invalid, parse writes an error response and returns false.
func (h *handler) parse(w http.ResponseWriter, r *http.Request) (*config.Config, bool) {
	body, ok := readBody(w, r)
	if !ok {
		return nil, false
	}

	cfg, err := config.Parse(body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err)
		return nil, false
	}
	return cfg, true
}

// wantsJSON reports whether the client asked for JSON responses, as the UI
// does. Other clients, such as curl, get plain text.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

type errorResponse struct {
	Message string       `json:"message"`
	Fields  []fieldError `json:"fields,omitempty"`
}

type fieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
	Value   string `json:"value,omitempty"`
}

// certNotFound is the problem code of missing certificates.
const certNotFound = "cert_not_found"

func writeError(w http.ResponseWriter, r *http.Request, status int, err error) {
	if !wantsJSON(r) {
		http.Error(w, err.Error(), status)
		return
	}

	resp := errorResponse{Message: err.Error()}
	var list config.FieldErrors
	var single *config.FieldError
	switch {
	case errors.As(err, &list):
	case errors.As(err, &single):
		list = config.FieldErrors{single}
	}
	for _, fe := range list {
		f := fieldError{Field: fe.Field, Message: fe.Err.Error()}
		var problem *config.Problem
		switch {
		case errors.As(fe.Err, &problem):
			f.Code, f.Value = string(problem.Code), problem.Value
		case errors.Is(fe.Err, nginx.ErrCertNotFound):
			f.Code = certNotFound
		}
		resp.Fields = append(resp.Fields, f)
	}
	writeJSON(w, status, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// An error here means that the client went away; there is nobody to tell.
	_ = json.NewEncoder(w).Encode(v)
}
