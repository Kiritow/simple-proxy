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
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/kiritow/simple-proxy/internal/acme"
	"github.com/kiritow/simple-proxy/internal/apply"
	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/manager"
	"github.com/kiritow/simple-proxy/internal/nginx"
	"github.com/kiritow/simple-proxy/internal/store"
)

// MaxConfigSize is the size limit of request bodies, in bytes.
const MaxConfigSize = 1 << 20

//go:embed dist
var dist embed.FS

// Options configure the handler returned by NewHandler.
type Options struct {
	Manager *manager.Manager
	// Generator generates configuration for /api/generate, which does not
	// involve the stored sites.
	Generator *nginx.Generator
	Example   []byte // the example configuration
	Logger    *slog.Logger
}

// NewHandler returns a handler serving the browser UI at / and this API:
//
//	GET    /api/sites         the sites: {"sites": [{"id": "...", "config": {...}}]}
//	POST   /api/sites         add a site: {"config": {...}}
//	PUT    /api/sites         replace all sites: {"configs": [{...}]}
//	PUT    /api/sites/{id}    change a site: {"config": {...}}
//	DELETE /api/sites/{id}    remove a site
//	GET    /api/status        how configuration is applied to nginx
//	POST   /api/apply         apply the stored sites again
//	GET    /api/nginx.conf    the configuration generated from the sites
//	GET    /api/certificates  the certificates managed by acme.sh
//	POST   /api/certificates  issue one: {"domains": ["..."], "dns": "dns_cf"}
//	POST   /api/validate      204 if the configuration in the body is valid
//	POST   /api/generate      the configuration generated from the body
//	GET    /api/example       an example configuration
//
// A site's config is one entry of servers in the configuration schema, in
// JSON. Changing sites applies them to nginx; if nginx rejects the result,
// nothing changes and the response is 502 with nginx's output.
//
// Errors are reported as
//
//	{"message": "...", "fields": [{"field": "servers[0].default.port", "message": "...", "code": "invalid_port", "value": "70000"}], "output": "..."}
//
// where code is a config.ProblemCode, or "cert_not_found", and identifies
// the problem for clients that describe it in their own words. Problems with
// a submitted site refer to it as servers[0]. Only validate and generate
// answer in plain text, unless the request accepts application/json.
func NewHandler(opts Options) http.Handler {
	ui, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded, so this cannot happen
	}
	files := http.FileServerFS(ui)
	h := &handler{m: opts.Manager, gen: opts.Generator, log: opts.Logger}

	mux := http.NewServeMux()
	// Only the index page and the build output are routed to the file server,
	// so that requests with the wrong method to the API get a 405.
	mux.Handle("GET /{$}", files)
	mux.Handle("GET /assets/", immutable(files))
	mux.HandleFunc("GET /api/example", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(opts.Example)
	})
	mux.HandleFunc("GET /api/sites", h.listSites)
	mux.HandleFunc("POST /api/sites", h.createSite)
	mux.HandleFunc("PUT /api/sites", h.replaceSites)
	mux.HandleFunc("PUT /api/sites/{id}", h.updateSite)
	mux.HandleFunc("DELETE /api/sites/{id}", h.deleteSite)
	mux.HandleFunc("GET /api/status", h.status)
	mux.HandleFunc("POST /api/apply", h.apply)
	mux.HandleFunc("GET /api/nginx.conf", h.nginxConf)
	mux.HandleFunc("GET /api/certificates", h.certificates)
	mux.HandleFunc("POST /api/certificates", h.issueCertificate)
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
	m   *manager.Manager
	gen *nginx.Generator
	log *slog.Logger
}

type siteResponse struct {
	store.Site
	// Problems are set for invalid sites stored by earlier versions, which
	// are not applied.
	Problems []fieldError `json:"problems,omitempty"`
}

func (h *handler) listSites(w http.ResponseWriter, r *http.Request) {
	infos, err := h.m.Sites(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sites := make([]siteResponse, len(infos))
	for i, info := range infos {
		sites[i] = siteResponse{Site: info.Site}
		if info.Problem != nil {
			sites[i].Problems = describe(info.Problem).Fields
			if len(sites[i].Problems) == 0 {
				sites[i].Problems = []fieldError{{Message: info.Problem.Error()}}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string][]siteResponse{"sites": sites})
}

type siteRequest struct {
	Config json.RawMessage `json:"config"`
}

func (h *handler) createSite(w http.ResponseWriter, r *http.Request) {
	var req siteRequest
	if !decodeJSON(w, r, &req) || !checkObject(w, req.Config) {
		return
	}
	site, err := h.m.CreateSite(r.Context(), req.Config)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, site)
}

func (h *handler) updateSite(w http.ResponseWriter, r *http.Request) {
	var req siteRequest
	if !decodeJSON(w, r, &req) || !checkObject(w, req.Config) {
		return
	}
	site, err := h.m.UpdateSite(r.Context(), r.PathValue("id"), req.Config)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, site)
}

func (h *handler) deleteSite(w http.ResponseWriter, r *http.Request) {
	if err := h.m.DeleteSite(r.Context(), r.PathValue("id")); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) replaceSites(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Configs []json.RawMessage `json:"configs"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	for _, cfg := range req.Configs {
		if !checkObject(w, cfg) {
			return
		}
	}
	sites, err := h.m.ReplaceSites(r.Context(), req.Configs)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]store.Site{"sites": sites})
}

func (h *handler) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.m.Status())
}

func (h *handler) apply(w http.ResponseWriter, r *http.Request) {
	if err := h.m.Apply(r.Context()); err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, h.m.Status())
}

func (h *handler) nginxConf(w http.ResponseWriter, r *http.Request) {
	conf, err := h.m.NginxConfig(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(conf)
}

func (h *handler) certificates(w http.ResponseWriter, r *http.Request) {
	certs, err := h.m.Certificates(r.Context())
	if err != nil {
		if !errors.Is(err, manager.ErrNoCA) {
			h.log.WarnContext(r.Context(), "listing certificates", "err", err)
		}
		writeError(w, r, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]acme.Cert{"certificates": certs})
}

func (h *handler) issueCertificate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domains []string `json:"domains"`
		DNS     string   `json:"dns"` // an acme.sh DNS API, or empty for HTTP
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// Issuing takes a while; let acme.sh finish even if the client leaves.
	ctx := context.WithoutCancel(r.Context())
	output, err := h.m.IssueCertificate(ctx, req.Domains, req.DNS)
	switch {
	case errors.Is(err, acme.ErrInvalidRequest), errors.Is(err, manager.ErrNoWebroot):
		writeError(w, r, http.StatusBadRequest, err)
	case errors.Is(err, manager.ErrNoCA):
		writeError(w, r, http.StatusServiceUnavailable, err)
	case err != nil:
		h.log.WarnContext(r.Context(), "issuing a certificate", "domains", req.Domains, "err", err)
		writeJSON(w, http.StatusBadGateway, errorResponse{Message: err.Error(), Output: output})
	default:
		writeJSON(w, http.StatusCreated, map[string]string{"output": output})
	}
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
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]string{"config": string(conf)})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(conf)
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
		h.fail(w, r, err)
		return nil, false
	}
	return cfg, true
}

// fail writes the response for err, choosing the status from its type.
func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var (
		fieldErrs config.FieldErrors
		fieldErr  *config.FieldError
		syntax    *config.SyntaxError
		applyErr  *apply.Error
	)
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, nginx.ErrCertNotFound):
		status = http.StatusUnprocessableEntity
	case errors.As(err, &fieldErrs), errors.As(err, &fieldErr), errors.As(err, &syntax):
		status = http.StatusBadRequest
	case errors.Is(err, manager.ErrNotFound):
		status = http.StatusNotFound
	case errors.As(err, &applyErr):
		status = http.StatusBadGateway // nginx rejected the configuration
	default:
		h.log.ErrorContext(r.Context(), "handling "+r.Method+" "+r.URL.Path, "err", err)
	}
	writeError(w, r, status, err)
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

// decodeJSON decodes the JSON request body into v. If that fails, it writes
// an error response and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, ok := readBody(w, r)
	if !ok {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Message: err.Error()})
		return false
	}
	return true
}

// checkObject makes sure that a site's config is a JSON object. If not, it
// writes an error response and returns false.
func checkObject(w http.ResponseWriter, cfg json.RawMessage) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(cfg, &object); err != nil || object == nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Message: "config must be a JSON object"})
		return false
	}
	return true
}

// wantsJSON reports whether to answer in JSON. Validate and generate answer
// in plain text unless asked for JSON, as the UI does, so that they are easy
// to use with curl.
func wantsJSON(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/validate", "/api/generate":
		return strings.Contains(r.Header.Get("Accept"), "application/json")
	}
	return true
}

type errorResponse struct {
	Message string       `json:"message"`
	Fields  []fieldError `json:"fields,omitempty"`
	Output  string       `json:"output,omitempty"` // of nginx or acme.sh
}

type fieldError struct {
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
	Value   string `json:"value,omitempty"`
}

// certNotFound is the problem code of missing certificates.
const certNotFound = "cert_not_found"

// describe returns the response describing err.
func describe(err error) errorResponse {
	resp := errorResponse{Message: err.Error()}
	var applyErr *apply.Error
	if errors.As(err, &applyErr) {
		resp.Message = applyErr.Step + " failed: " + applyErr.Err.Error()
		resp.Output = applyErr.Output
	}

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
	return resp
}

func writeError(w http.ResponseWriter, r *http.Request, status int, err error) {
	if !wantsJSON(r) {
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, status, describe(err))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// An error here means that the client went away; there is nobody to tell.
	_ = json.NewEncoder(w).Encode(v)
}
