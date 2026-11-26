// Package web serves the browser UI and the HTTP API of simple-proxy.
//
// The UI is the React application in the web directory of the repository,
// built into dist by `go generate` (which requires `npm ci` in web first) or
// build.sh. dist is not in the repository, so the package only compiles once
// the UI is built.
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
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/kiritow/simple-proxy/internal/acme"
	"github.com/kiritow/simple-proxy/internal/apply"
	"github.com/kiritow/simple-proxy/internal/auth"
	"github.com/kiritow/simple-proxy/internal/buildinfo"
	"github.com/kiritow/simple-proxy/internal/certs"
	"github.com/kiritow/simple-proxy/internal/config"
	"github.com/kiritow/simple-proxy/internal/manager"
	"github.com/kiritow/simple-proxy/internal/nginx"
	"github.com/kiritow/simple-proxy/internal/store"
)

// MaxConfigSize is the size limit of request bodies, in bytes.
const MaxConfigSize = 1 << 20

// dist is the UI. If compiling fails here with "pattern dist: no matching
// files found", build the UI first, which needs Node.js:
//
//	npm --prefix web ci && go generate ./internal/web
//
// or run build.sh. Without the UI, nothing compiles, so that no binary
// lacks it.
//
//go:embed dist
var dist embed.FS

// Options configure the handler returned by NewHandler.
type Options struct {
	Manager *manager.Manager
	// Auth signs users in, and tells what they may do. It is required.
	Auth    *auth.Service
	Example []byte // the example configuration
	Logger  *slog.Logger
	Build   buildinfo.Info // what the server was built from
}

// NewHandler returns a handler serving the browser UI at / and this API:
//
//	GET    /api/auth/config    how users sign in: {"password": true, "registration": true, "oidc": {"name": "..."}}
//	POST   /api/auth/login     sign in: {"username": "...", "password": "..."} gives {"user": {...}, "permissions": {...}}
//	POST   /api/auth/register  sign up: {"username": "...", "password": "...", "display_name": "..."}, likewise
//	POST   /api/auth/logout    sign out
//	GET    /api/auth/me        the signed-in user and what they may do: {"user": {...}, "permissions": {...}}
//	POST   /api/auth/password  change your password: {"current": "...", "password": "..."}
//	GET    /api/auth/oidc/login     sign in with the OIDC provider
//	GET    /api/auth/oidc/callback  where the provider sends users back; failures go to the console with ?login_error=code
//	GET    /api/users          the users: {"users": [{"id": "...", "username": "...", "role": "site_operator", "sites": ["..."], ...}]}
//	POST   /api/users          add one: {"username": "...", "password": "...", "role": "...", "sites": [...], ...}
//	PATCH  /api/users/{id}     change one: {"role": "...", "sites": [...], "disabled": true, "password": "...", ...}
//	DELETE /api/users/{id}     delete one
//	GET    /api/settings       {"registration": true}: whether new accounts may sign up
//	PUT    /api/settings       change them
//	GET    /api/versions/stats  super administrators: the versions kept of every change of a site, by day; ?offset= minutes east of UTC
//	POST   /api/versions/purge  delete the versions made before {"before": "<RFC 3339>"}, but each site's current one: {"deleted": 3}
//	GET    /api/sites          the sites: {"sites": [{"id": "...", "config": {...}, "generation": 1, "created_at": "...", "updated_at": "..."}]}
//	POST   /api/sites          add a site: {"config": {...}}
//	PUT    /api/sites          replace all sites: {"configs": [{...}]}
//	PUT    /api/sites/{id}     change a site: {"config": {...}, "generation": 1}
//	DELETE /api/sites/{id}     remove a site; ?generation=1
//	POST   /api/sites/preview  what saving a site would change: {"id": "...", "config": {...}}
//	POST   /api/sites/raw      the site as a raw configuration: {"config": {...}} gives {"raw": "..."}
//	GET    /api/status        how configuration is applied to nginx, and the commit the server was built from
//	POST   /api/apply         apply the stored sites again
//	GET    /api/nginx.conf    the configuration generated from the sites
//	GET    /api/certificates  the certificates: {"certificates": [...], "acme_error": "..."}
//	POST   /api/certificates  have acme.sh issue one: {"domains": ["..."], "dns": "dns_cf"}
//	POST   /api/certificates/upload  upload one: {"name": "...", "cert": "PEM", "key": "PEM", "replace": false}
//	POST   /api/certificates/sign    have a CA sign one: {"name": "...", "authority": "...", "domains": ["..."], "days": 365, "key_type": "ec", "replace": false}
//	POST   /api/certificates/upload/preview  what uploading would store, without storing it
//	POST   /api/certificates/sign/preview    what signing would store, without signing
//	DELETE /api/certificates/{name}  remove an uploaded or signed one
//	GET    /api/authorities          the CAs: {"authorities": [...]}
//	POST   /api/authorities          upload one: {"name": "...", "cert": "PEM", "key": "PEM"}
//	POST   /api/authorities/preview  what uploading one would store
//	DELETE /api/authorities/{name}   remove one
//	GET    /api/authorities/{name}/cert.pem  its certificate, for clients to trust
//	POST   /api/validate      204 if the configuration in the body is valid
//	POST   /api/generate      the configuration generated from the body
//	GET    /api/example       an example configuration
//
// Requests other than signing in need a session, whose cookie signing in
// sets, or a username and password with HTTP Basic auth; otherwise they fail
// with 401. What users may do depends on their role, see auth.Permissions;
// other requests fail with 403. Site operators only see and change the sites
// assigned to them, and previews of those only.
//
// A site's config is one entry of servers in the configuration schema, in
// JSON. Changing sites applies them to nginx; if nginx rejects the result,
// nothing changes and the response is 502 with nginx's output.
//
// A site's generation counts its changes. Changes and deletions based on an
// outdated generation fail with 409 Conflict, so that concurrent editors do
// not overwrite each other; without a generation, they always apply. The
// preview answers {"current": "...", "proposed": "...", "site": "...",
// "generation": 1}: the configuration before and after saving, the
// configuration of the site alone, as it would be enabled ("" if it cannot
// be generated, as only disabled sites may), and the site's current
// generation (0 for a new site, whose id is empty).
//
// Errors are reported as
//
//	{"message": "...", "code": "forbidden", "fields": [{"field": "servers[0].default.port", "message": "...", "code": "invalid_port", "value": "70000"}], "output": "..."}
//
// where the code of a field is a config.ProblemCode, a certs.ProblemCode, an
// auth.ProblemCode, or "cert_not_found", and identifies the problem for
// clients that describe it in their own words; the code of the error is that
// of an auth.Error. Problems with a submitted site refer to it as
// servers[0]; those with uploads and signing requests name their fields. Only
// validate and generate answer in plain text, unless the request accepts
// application/json. Private keys are never sent.
//
// Certificate previews answer 200 with the certificate as far as it can be
// read, and the problems that keep it from being stored, which are expected
// while it is being entered:
//
//	{"name": "...", "subject": "...", "issuer": "...", "domains": [...], "key": "EC P-256",
//	 "not_before": "...", "not_after": "...", "chain": [{"subject": "...", "not_after": "...", "root": true}],
//	 "has_key": true, "problems": [{"field": "key", "message": "...", "code": "key_mismatch"}]}
func NewHandler(opts Options) http.Handler {
	ui, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded, so this cannot happen
	}
	files := http.FileServerFS(ui)
	h := &handler{m: opts.Manager, auth: opts.Auth, log: opts.Logger, build: opts.Build}
	g := h.guard

	mux := http.NewServeMux()
	// Only the index page and the build output are routed to the file server,
	// so that requests with the wrong method to the API get a 405.
	mux.Handle("GET /{$}", files)
	mux.Handle("GET /assets/", immutable(files))

	mux.Handle("GET /api/auth/config", g(public, h.authConfig))
	mux.Handle("POST /api/auth/login", g(public, h.login))
	mux.Handle("POST /api/auth/register", g(public, h.register))
	mux.Handle("POST /api/auth/logout", g(public, h.logout))
	mux.Handle("GET /api/auth/oidc/login", g(public, h.oidcLogin))
	mux.Handle("GET /api/auth/oidc/callback", g(public, h.oidcCallback))
	mux.Handle("GET /api/auth/me", g(account, h.me))
	mux.Handle("POST /api/auth/password", g(account, h.changePassword))

	mux.Handle("GET /api/users", g(usersAccess, h.listUsers))
	mux.Handle("POST /api/users", g(usersAccess, h.createUser))
	mux.Handle("PATCH /api/users/{id}", g(usersAccess, h.updateUser))
	mux.Handle("DELETE /api/users/{id}", g(usersAccess, h.deleteUser))
	mux.Handle("GET /api/settings", g(usersAccess, h.settings))
	mux.Handle("PUT /api/settings", g(usersAccess, h.updateSettings))
	mux.Handle("GET /api/versions/stats", g(historyAccess, h.versionStats))
	mux.Handle("POST /api/versions/purge", g(historyAccess, h.purgeVersions))

	mux.Handle("GET /api/example", g(sitesAccess, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(opts.Example)
	}))
	mux.Handle("GET /api/sites", g(sitesAccess, h.listSites))
	mux.Handle("POST /api/sites", g(allSites, h.createSite))
	mux.Handle("PUT /api/sites", g(allSites, h.replaceSites))
	mux.Handle("POST /api/sites/preview", g(sitesAccess, h.previewSite))
	mux.Handle("POST /api/sites/raw", g(sitesAccess, h.rawSite))
	mux.Handle("PUT /api/sites/{id}", g(sitesAccess, h.updateSite))
	mux.Handle("DELETE /api/sites/{id}", g(allSites, h.deleteSite))
	mux.Handle("GET /api/status", g(sitesAccess, h.status))
	mux.Handle("POST /api/apply", g(allSites, h.apply))
	mux.Handle("GET /api/nginx.conf", g(allSites, h.nginxConf))
	mux.Handle("GET /api/certificates", g(sitesAccess, h.certificates))
	mux.Handle("POST /api/certificates", g(certsAccess, h.issueCertificate))
	mux.Handle("POST /api/certificates/upload", g(certsAccess, h.uploadCertificate))
	mux.Handle("POST /api/certificates/sign", g(certsAccess, h.signCertificate))
	mux.Handle("POST /api/certificates/upload/preview", g(certsAccess, h.previewUpload(false)))
	mux.Handle("POST /api/certificates/sign/preview", g(certsAccess, h.previewSign))
	mux.Handle("POST /api/authorities/preview", g(certsAccess, h.previewUpload(true)))
	mux.Handle("GET /api/certificates/{name}", g(sitesAccess, h.certificate))
	mux.Handle("PATCH /api/certificates/{name}", g(certsAccess, h.renameCertificate))
	mux.Handle("DELETE /api/certificates/{name}", g(certsAccess, h.deleteCertificate))
	mux.Handle("GET /api/authorities", g(sitesAccess, h.authorities))
	mux.Handle("POST /api/authorities", g(certsAccess, h.addAuthority))
	mux.Handle("GET /api/authorities/{name}", g(sitesAccess, h.authority))
	mux.Handle("PATCH /api/authorities/{name}", g(certsAccess, h.renameAuthority))
	mux.Handle("DELETE /api/authorities/{name}", g(certsAccess, h.deleteAuthority))
	mux.Handle("GET /api/authorities/{name}/cert.pem", g(sitesAccess, h.authorityCert))
	mux.Handle("POST /api/validate", g(sitesAccess, h.validate))
	mux.Handle("POST /api/generate", g(sitesAccess, h.generate))

	// Browsers must not be tricked into changing anything for other sites,
	// with the session cookie: requests other than GET must come from the
	// console itself.
	cop := http.NewCrossOriginProtection()
	if console := opts.Auth.OIDCConsoleURL(); console != "" {
		if u, err := url.Parse(console); err == nil {
			_ = cop.AddTrustedOrigin(u.Scheme + "://" + u.Host)
		}
	}
	return cop.Handler(mux)
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
	m     *manager.Manager
	auth  *auth.Service
	log   *slog.Logger
	build buildinfo.Info
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
	u := userOf(r).user
	sites := []siteResponse{}
	for _, info := range infos {
		if !auth.CanManageSite(u, info.ID) {
			continue
		}
		site := siteResponse{Site: info.Site}
		if info.Problem != nil {
			site.Problems = describe(info.Problem).Fields
			if len(site.Problems) == 0 {
				site.Problems = []fieldError{{Message: info.Problem.Error()}}
			}
		}
		sites = append(sites, site)
	}
	writeJSON(w, http.StatusOK, map[string][]siteResponse{"sites": sites})
}

type siteRequest struct {
	ID         string          `json:"id"` // for previews
	Config     json.RawMessage `json:"config"`
	Generation int64           `json:"generation"`
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
	if err := h.checkSiteChange(r, r.PathValue("id"), req.Config); err != nil {
		h.fail(w, r, err)
		return
	}
	site, err := h.m.UpdateSite(r.Context(), r.PathValue("id"), req.Generation, req.Config)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, site)
}

func (h *handler) previewSite(w http.ResponseWriter, r *http.Request) {
	var req siteRequest
	if !decodeJSON(w, r, &req) || !checkObject(w, req.Config) {
		return
	}
	// Users who may not see all sites only preview the changes to theirs.
	u := userOf(r).user
	var visible func(id string) bool
	if !auth.PermissionsOf(u.Role).AllSites {
		if err := h.checkSiteChange(r, req.ID, req.Config); err != nil {
			h.fail(w, r, err)
			return
		}
		visible = func(id string) bool { return auth.CanManageSite(u, id) }
	}
	p, err := h.m.Preview(r.Context(), req.ID, req.Config, visible)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"current":    string(p.Current),
		"proposed":   string(p.Proposed),
		"site":       p.Site,
		"generation": p.Generation,
	})
}

func (h *handler) rawSite(w http.ResponseWriter, r *http.Request) {
	var req siteRequest
	if !decodeJSON(w, r, &req) || !checkObject(w, req.Config) {
		return
	}
	raw, err := h.m.RawConfig(r.Context(), req.Config)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"raw": raw})
}

// checkSiteChange checks that the user of r may change the site with the
// given ID to cfg. Users who manage all sites may change anything. Site
// operators may only change their own sites, and not their raw
// configuration or extra directives: nginx runs those with its privileges,
// e.g. to listen on other addresses or write files, which reaches beyond
// their sites.
func (h *handler) checkSiteChange(r *http.Request, id string, cfg json.RawMessage) error {
	u := userOf(r).user
	if auth.PermissionsOf(u.Role).AllSites {
		return nil
	}
	if !auth.CanManageSite(u, id) {
		return auth.ErrForbidden
	}
	sites, err := h.m.Sites(r.Context())
	if err != nil {
		return err
	}
	i := slices.IndexFunc(sites, func(s manager.SiteInfo) bool { return s.ID == id })
	if i < 0 {
		return manager.ErrNotFound
	}
	proposed, err := directivesOf(cfg)
	if err != nil {
		return nil // saving reports the problems
	}
	current, _ := directivesOf(sites[i].Config) // none if invalid
	if current != proposed {
		return auth.ErrDirectivesRestricted
	}
	return nil
}

// directivesOf returns the raw configuration and the extra directives of a
// site's config, by location path, as a string to compare.
func directivesOf(cfg json.RawMessage) (string, error) {
	s, err := config.DecodeServer(cfg)
	if err != nil {
		s = &config.Server{}
	}
	d := struct {
		Raw       *string
		Server    []config.Directive
		Locations map[string][][]config.Directive
	}{Raw: s.Raw, Server: s.Options, Locations: map[string][][]config.Directive{}}
	for _, l := range append([]config.Location{s.Default}, s.Locations...) {
		if len(l.Options) > 0 {
			d.Locations[l.Path] = append(d.Locations[l.Path], l.Options)
		}
	}
	text, _ := json.Marshal(d)
	return string(text), err
}

func (h *handler) deleteSite(w http.ResponseWriter, r *http.Request) {
	var generation int64
	if g := r.URL.Query().Get("generation"); g != "" {
		var err error
		if generation, err = strconv.ParseInt(g, 10, 64); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Message: "generation must be a number"})
			return
		}
	}
	if err := h.m.DeleteSite(r.Context(), r.PathValue("id"), generation); err != nil {
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

// statusResponse is the status of the manager, with the build of the server.
type statusResponse struct {
	manager.Status
	buildinfo.Info
}

func (h *handler) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusResponse{h.m.Status(), h.build})
}

func (h *handler) apply(w http.ResponseWriter, r *http.Request) {
	if err := h.m.Apply(r.Context()); err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{h.m.Status(), h.build})
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
	list, err := h.m.Certificates(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *handler) uploadCertificate(w http.ResponseWriter, r *http.Request) {
	var req manager.UploadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cert, err := h.m.UploadCertificate(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, cert)
}

func (h *handler) signCertificate(w http.ResponseWriter, r *http.Request) {
	var req manager.SignRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cert, err := h.m.SignCertificate(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, cert)
}

type previewResponse struct {
	manager.CertificatePreview
	Problems []fieldError `json:"problems"`
}

func (h *handler) previewUpload(authority bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req manager.UploadRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		p, problems, err := h.m.PreviewUpload(r.Context(), req, authority)
		h.writePreview(w, r, p, problems, err)
	}
}

func (h *handler) previewSign(w http.ResponseWriter, r *http.Request) {
	var req manager.SignRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p, problems, err := h.m.PreviewSign(r.Context(), req)
	h.writePreview(w, r, p, problems, err)
}

func (h *handler) writePreview(w http.ResponseWriter, r *http.Request, p manager.CertificatePreview, problems config.FieldErrors, err error) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resp := previewResponse{CertificatePreview: p, Problems: []fieldError{}}
	if len(problems) > 0 {
		resp.Problems = describe(problems).Fields
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) certificate(w http.ResponseWriter, r *http.Request) {
	d, err := h.m.CertificateDetails(r.Context(), r.PathValue("name"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	u := userOf(r).user
	d.Sites = slices.DeleteFunc(d.Sites, func(s manager.SiteRef) bool { return !auth.CanManageSite(u, s.ID) })
	writeJSON(w, http.StatusOK, d)
}

// renameRequest renames a certificate or a CA.
type renameRequest struct {
	Name string `json:"name"`
}

func (h *handler) renameCertificate(w http.ResponseWriter, r *http.Request) {
	var req renameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cert, err := h.m.RenameCertificate(r.Context(), r.PathValue("name"), req.Name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cert)
}

func (h *handler) deleteCertificate(w http.ResponseWriter, r *http.Request) {
	if err := h.m.DeleteCertificate(r.Context(), r.PathValue("name")); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) authorities(w http.ResponseWriter, r *http.Request) {
	cas, err := h.m.Authorities(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]manager.Authority{"authorities": cas})
}

func (h *handler) addAuthority(w http.ResponseWriter, r *http.Request) {
	var req manager.UploadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Replace {
		writeJSON(w, http.StatusBadRequest, errorResponse{Message: "CAs cannot be replaced; delete the CA first"})
		return
	}
	ca, err := h.m.AddAuthority(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, ca)
}

func (h *handler) authority(w http.ResponseWriter, r *http.Request) {
	d, err := h.m.AuthorityDetails(r.Context(), r.PathValue("name"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *handler) renameAuthority(w http.ResponseWriter, r *http.Request) {
	var req renameRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ca, err := h.m.RenameAuthority(r.Context(), r.PathValue("name"), req.Name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ca)
}

func (h *handler) deleteAuthority(w http.ResponseWriter, r *http.Request) {
	if err := h.m.DeleteAuthority(r.Context(), r.PathValue("name")); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) authorityCert(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pem, err := h.m.AuthorityCert(r.Context(), name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".crt"}))
	w.Write(pem)
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
	case errors.Is(err, manager.ErrNoACME):
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
	conf, err := h.m.Generate(r.Context(), cfg)
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
		authErr   *auth.Error
	)
	status := http.StatusInternalServerError
	switch {
	case errors.As(err, &authErr):
		status = authStatus[authErr.Code]
		if status == 0 {
			status = http.StatusForbidden
		}
	case errors.Is(err, auth.ErrUserNotFound):
		status = http.StatusNotFound
	case errors.Is(err, nginx.ErrCertNotFound):
		status = http.StatusUnprocessableEntity
	case errors.As(err, &fieldErrs), errors.As(err, &fieldErr), errors.As(err, &syntax):
		status = http.StatusBadRequest
	case errors.Is(err, manager.ErrNotFound), errors.Is(err, manager.ErrNoCertificate), errors.Is(err, manager.ErrNoAuthority):
		status = http.StatusNotFound
	case errors.Is(err, manager.ErrConflict):
		status = http.StatusConflict
	case errors.As(err, &applyErr):
		status = http.StatusBadGateway // nginx rejected the configuration
	default:
		h.log.ErrorContext(r.Context(), "handling "+r.Method+" "+r.URL.Path, "err", err)
	}
	writeError(w, r, status, err)
}

// authStatus is the status of the auth.Errors other than 403 Forbidden.
var authStatus = map[string]int{
	auth.ErrUnauthenticated.Code:    http.StatusUnauthorized,
	auth.ErrInvalidCredentials.Code: http.StatusUnauthorized,
	auth.ErrTooManyAttempts.Code:    http.StatusTooManyRequests,
	auth.ErrLastAdmin.Code:          http.StatusBadRequest,
	auth.ErrOIDCUnavailable.Code:    http.StatusNotFound,
	auth.ErrOIDCFailed.Code:         http.StatusBadGateway,
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
	Code    string       `json:"code,omitempty"` // of an auth.Error
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
	var authErr *auth.Error
	if errors.As(err, &authErr) {
		resp.Code = authErr.Code
	}
	var applyErr *apply.Error
	if errors.As(err, &applyErr) {
		resp.Message = applyErr.Message() // with nginx's reason
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
		var certProblem *certs.Problem
		var authProblem *auth.Problem
		switch {
		case errors.As(fe.Err, &problem):
			f.Code, f.Value = string(problem.Code), problem.Value
		case errors.As(fe.Err, &certProblem):
			f.Code, f.Value = string(certProblem.Code), certProblem.Value
		case errors.As(fe.Err, &authProblem):
			f.Code, f.Value = string(authProblem.Code), authProblem.Value
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
