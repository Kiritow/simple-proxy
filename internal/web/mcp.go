package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kiritow/simple-proxy/internal/auth"
	"github.com/kiritow/simple-proxy/internal/store"
)

// mcpHandler serves the MCP endpoint (Streamable HTTP, stateless): tools for
// AI clients to manage the sites as the user of the request. The tools make
// their requests to api, the HTTP API, in-process as that user, so that
// they may do exactly what the user may do in the console, checked the same
// way. Users only get the tools that their role allows.
func (h *handler) mcpHandler(api http.Handler) http.Handler {
	cache := mcp.NewSchemaCache()
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		s := userOf(r)
		return h.mcpServer(&mcpUser{api: api, s: s}, h.auth.Permissions(s.user), cache)
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// The protection against DNS rebinding refuses requests that reach
		// a local address for another host, as those through a reverse
		// proxy on the same host do. Every request needs a user's
		// credentials, which a page of another site cannot have the
		// browser send by rebinding DNS, so it is not needed.
		DisableLocalhostProtection: true,
	})
}

// mcpUser makes API requests as a user.
type mcpUser struct {
	api http.Handler
	s   signedIn
}

// call makes an API request, with body in JSON unless nil, and returns the
// body of the response. Failures are returned as errors describing them as
// the API does.
func (u *mcpUser) call(ctx context.Context, method, path string, body any) ([]byte, error) {
	var data io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		data = bytes.NewReader(b)
	}
	// The user in the context makes guard skip authenticating again, but
	// it still checks what the user may do.
	ctx = store.WithAuthor(context.WithValue(ctx, signedInKey{}, u.s), u.s.user.Username)
	req, err := http.NewRequestWithContext(ctx, method, path, data)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	u.api.ServeHTTP(rec, req)
	if rec.Code >= 300 {
		return nil, apiFailure(rec.Code, rec.Body.Bytes())
	}
	if rec.Code == http.StatusNoContent {
		return []byte(`{"ok":true}`), nil
	}
	return rec.Body.Bytes(), nil
}

// apiFailure describes a failed API request: the message, the problems
// with fields, and the output of nginx or acme.sh.
func apiFailure(status int, body []byte) error {
	var resp errorResponse
	if err := json.Unmarshal(body, &resp); err != nil || resp.Message == "" {
		return fmt.Errorf("%d %s: %s", status, http.StatusText(status), strings.TrimSpace(string(body)))
	}
	var b strings.Builder
	b.WriteString(resp.Message)
	if resp.Code != "" {
		fmt.Fprintf(&b, " (%s)", resp.Code)
	}
	for _, f := range resp.Fields {
		fmt.Fprintf(&b, "\n- %s: %s", f.Field, f.Message)
	}
	if resp.Output != "" {
		fmt.Fprintf(&b, "\noutput:\n%s", resp.Output)
	}
	return errors.New(b.String())
}

// tool adds a tool, whose handler returns the text of its result.
func tool[In any](srv *mcp.Server, t *mcp.Tool, handle func(context.Context, In) ([]byte, error)) {
	mcp.AddTool(srv, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		out, err := handle(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}}, nil, nil
	})
}

var (
	yes = func() *bool { b := true; return &b }()
	no  = func() *bool { b := false; return &b }()
)

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: no}
}

func changes(title string, destructive bool) *mcp.ToolAnnotations {
	a := &mcp.ToolAnnotations{Title: title, DestructiveHint: no, OpenWorldHint: no}
	if destructive {
		a.DestructiveHint = yes
	}
	return a
}

// mcpInstructions tell MCP clients how to use the tools.
const mcpInstructions = `simple-proxy manages nginx as a reverse proxy. Each site is an nginx server: its domains, and the upstream services that requests for them are forwarded to. You act as the user whose API token you use, and may do exactly what their account may do in the simple-proxy console; whoami tells what that is.

A site's configuration is a JSON object (unknown fields are rejected):
- server_name: its domains, separated by spaces, e.g. "example.com www.example.com"
- enable: false to keep the site without serving it (default true)
- https: true to serve HTTPS, with the certificate named cert_name (default: the first domain; see list_certificates); listen_port defaults to 443 then, or 80
- gzip: true to compress text responses
- options: extra nginx directives for the server block, e.g. ["client_max_body_size 64m"]
- default: the main route, e.g. {"ip": "127.0.0.1", "port": 3000, "websocket": true}; also path (default "/"), disable_cache, realtime (no buffering) and options
- locations: more routes by path prefix, e.g. [{"path": "/api", "port": 3001}], with the fields of default, whose ip and port they inherit
- raw: instead of all of the above (only enable may be set besides), the site's nginx configuration as text, referring to certificates as {{cert "name"}} and {{key "name"}}

To change a site: read it with get_site (it has a generation), check the change with preview_site, which shows the nginx configuration it would generate, then save it with update_site and that generation. Saving applies the configuration to nginx at once; if nginx rejects it, nothing changes, and the error has nginx's reason. Site operators cannot change the raw configuration or the options (extra directives) of their sites.`

// mcpServer returns an MCP server with the tools that a user with the
// given permissions may use.
func (h *handler) mcpServer(u *mcpUser, p auth.Permissions, cache *mcp.SchemaCache) *mcp.Server {
	version := h.build.Commit
	if len(version) > 12 {
		version = version[:12]
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "simple-proxy", Title: "Simple Proxy", Version: version},
		&mcp.ServerOptions{Instructions: mcpInstructions, SchemaCache: cache, Capabilities: &mcp.ServerCapabilities{}})

	tool(srv, &mcp.Tool{
		Name:        "whoami",
		Description: "The user you act as, their role, and what they may do: permissions.sites (see and change sites: all with all_sites, otherwise those assigned to them), all_sites (add, delete sites, apply, see the whole nginx configuration), certificates (request, upload, sign, rename and delete certificates).",
		Annotations: readOnly("Who am I"),
	}, func(ctx context.Context, _ struct{}) ([]byte, error) {
		return u.call(ctx, "GET", "/api/auth/me", nil)
	})
	if p.Sites {
		h.mcpSiteTools(srv, u)
	}
	if p.AllSites {
		h.mcpPlatformTools(srv, u)
	}
	if p.Certificates {
		h.mcpCertificateTools(srv, u)
	}
	return srv
}

type siteRef struct {
	ID string `json:"id" jsonschema:"the ID of the site, from list_sites"`
}

type siteChange struct {
	ID         string         `json:"id" jsonschema:"the ID of the site, from list_sites"`
	Config     map[string]any `json:"config" jsonschema:"the whole new configuration of the site (see the instructions)"`
	Generation int64          `json:"generation" jsonschema:"the generation of the site that the change is based on, from get_site; saving fails if the site changed since. 0 saves whatever changed"`
}

type sitePreview struct {
	ID     string         `json:"id,omitempty" jsonschema:"the ID of the site to change; omit for a new site"`
	Config map[string]any `json:"config" jsonschema:"the whole configuration of the site (see the instructions)"`
}

func (h *handler) mcpSiteTools(srv *mcp.Server, u *mcpUser) {
	tool(srv, &mcp.Tool{
		Name:        "list_sites",
		Description: "The sites you may see, with their IDs, configurations, generations (which count their changes) and when they were added and changed.",
		Annotations: readOnly("List sites"),
	}, func(ctx context.Context, _ struct{}) ([]byte, error) {
		return u.call(ctx, "GET", "/api/sites", nil)
	})
	tool(srv, &mcp.Tool{
		Name:        "get_site",
		Description: "A site, with the nginx configuration generated for it alone (as it would be when enabled).",
		Annotations: readOnly("Get a site"),
	}, func(ctx context.Context, in siteRef) ([]byte, error) {
		site, err := u.site(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		nginx, err := u.siteNginx(ctx, in.ID, site.Config)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"site": site, "nginx": nginx})
	})
	tool(srv, &mcp.Tool{
		Name:        "preview_site",
		Description: "Checks a configuration of a site without saving it, and shows the nginx configuration it generates for the site, with a diff against the site's current one. Problems with the configuration are reported as errors, like saving would.",
		Annotations: readOnly("Preview a site"),
	}, func(ctx context.Context, in sitePreview) ([]byte, error) {
		var current string
		if in.ID != "" {
			site, err := u.site(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if current, err = u.siteNginx(ctx, in.ID, site.Config); err != nil {
				return nil, err
			}
		}
		body, err := u.call(ctx, "POST", "/api/sites/preview", siteRequestOf(in.ID, in.Config))
		if err != nil {
			return nil, err
		}
		var p struct {
			Site       string `json:"site"`
			Generation int64  `json:"generation"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{
			"valid":      true,
			"generation": p.Generation, // of the site as stored; 0 for a new one
			"nginx":      p.Site,
			"diff":       lineDiff(current, p.Site),
		})
	})
	tool(srv, &mcp.Tool{
		Name:        "update_site",
		Description: "Saves a new configuration of a site, and applies it to nginx right away. Returns the site as saved, with its new generation.",
		Annotations: changes("Change a site", true),
	}, func(ctx context.Context, in siteChange) ([]byte, error) {
		return u.call(ctx, "PUT", "/api/sites/"+url.PathEscape(in.ID),
			map[string]any{"config": in.Config, "generation": in.Generation})
	})
	tool(srv, &mcp.Tool{
		Name:        "set_site_enabled",
		Description: "Turns a site on or off, keeping the rest of its configuration, and applies it to nginx right away.",
		Annotations: changes("Turn a site on or off", false),
	}, func(ctx context.Context, in struct {
		ID      string `json:"id" jsonschema:"the ID of the site, from list_sites"`
		Enabled bool   `json:"enabled" jsonschema:"whether nginx serves the site"`
	}) ([]byte, error) {
		site, err := u.site(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		var cfg map[string]any
		if err := json.Unmarshal(site.Config, &cfg); err != nil {
			return nil, err
		}
		cfg["enable"] = in.Enabled
		return u.call(ctx, "PUT", "/api/sites/"+url.PathEscape(in.ID),
			map[string]any{"config": cfg, "generation": site.Generation})
	})
	tool(srv, &mcp.Tool{
		Name:        "get_status",
		Description: "How the sites are applied to nginx: nginx's version, the configuration file and commands, when the sites were last applied, and the last error, if nginx may not run the stored sites.",
		Annotations: readOnly("nginx status"),
	}, func(ctx context.Context, _ struct{}) ([]byte, error) {
		return u.call(ctx, "GET", "/api/status", nil)
	})
	tool(srv, &mcp.Tool{
		Name:        "list_certificates",
		Description: "The certificates that sites can serve, by name: issued by acme.sh, uploaded, or signed by a CA kept here, with their domains and expiry.",
		Annotations: readOnly("List certificates"),
	}, func(ctx context.Context, _ struct{}) ([]byte, error) {
		return u.call(ctx, "GET", "/api/certificates", nil)
	})
	tool(srv, &mcp.Tool{
		Name:        "get_certificate",
		Description: "A certificate in full: its chain, fingerprint, files, and the sites you may see that serve it.",
		Annotations: readOnly("Get a certificate"),
	}, func(ctx context.Context, in struct {
		Name string `json:"name" jsonschema:"the name of the certificate"`
	}) ([]byte, error) {
		return u.call(ctx, "GET", "/api/certificates/"+url.PathEscape(in.Name), nil)
	})
	tool(srv, &mcp.Tool{
		Name:        "list_authorities",
		Description: "The certificate authorities (CAs) kept here, which sign certificates for internal sites.",
		Annotations: readOnly("List CAs"),
	}, func(ctx context.Context, _ struct{}) ([]byte, error) {
		return u.call(ctx, "GET", "/api/authorities", nil)
	})
}

func (h *handler) mcpPlatformTools(srv *mcp.Server, u *mcpUser) {
	tool(srv, &mcp.Tool{
		Name:        "create_site",
		Description: "Adds a site, and applies it to nginx right away. Returns the site with its ID.",
		Annotations: changes("Add a site", false),
	}, func(ctx context.Context, in struct {
		Config map[string]any `json:"config" jsonschema:"the configuration of the site (see the instructions)"`
	}) ([]byte, error) {
		return u.call(ctx, "POST", "/api/sites", map[string]any{"config": in.Config})
	})
	tool(srv, &mcp.Tool{
		Name:        "delete_site",
		Description: "Deletes a site; nginx stops serving it right away. Its versions stay in the history.",
		Annotations: changes("Delete a site", true),
	}, func(ctx context.Context, in struct {
		ID         string `json:"id" jsonschema:"the ID of the site, from list_sites"`
		Generation int64  `json:"generation,omitempty" jsonschema:"the generation of the site; deleting fails if it changed since. Omit to delete it whatever changed"`
	}) ([]byte, error) {
		path := "/api/sites/" + url.PathEscape(in.ID)
		if in.Generation != 0 {
			path += fmt.Sprintf("?generation=%d", in.Generation)
		}
		return u.call(ctx, "DELETE", path, nil)
	})
	tool(srv, &mcp.Tool{
		Name:        "get_nginx_config",
		Description: "The whole nginx configuration generated from all enabled sites, as nginx gets it.",
		Annotations: readOnly("nginx configuration"),
	}, func(ctx context.Context, _ struct{}) ([]byte, error) {
		return u.call(ctx, "GET", "/api/nginx.conf", nil)
	})
	tool(srv, &mcp.Tool{
		Name:        "apply_sites",
		Description: "Applies the stored sites to nginx again, e.g. after nginx restarted, and returns the status.",
		Annotations: changes("Apply again", false),
	}, func(ctx context.Context, _ struct{}) ([]byte, error) {
		return u.call(ctx, "POST", "/api/apply", nil)
	})
}

func (h *handler) mcpCertificateTools(srv *mcp.Server, u *mcpUser) {
	issue := changes("Request a certificate", false)
	issue.OpenWorldHint = yes // from Let's Encrypt or another ACME CA
	tool(srv, &mcp.Tool{
		Name:        "issue_certificate",
		Description: "Has acme.sh request a certificate for domains from the ACME CA (e.g. Let's Encrypt), validated over HTTP through nginx, or with an acme.sh DNS API such as dns_cf. This can take minutes. The certificate is named after its first domain.",
		Annotations: issue,
	}, func(ctx context.Context, in struct {
		Domains []string `json:"domains" jsonschema:"the domains, e.g. [\"example.com\", \"www.example.com\"]; wildcards need dns"`
		DNS     string   `json:"dns,omitempty" jsonschema:"an acme.sh DNS API, e.g. dns_cf; omit to validate over HTTP"`
	}) ([]byte, error) {
		return u.call(ctx, "POST", "/api/certificates", map[string]any{"domains": in.Domains, "dns": in.DNS})
	})
	tool(srv, &mcp.Tool{
		Name:        "sign_certificate",
		Description: "Has a CA kept here sign a certificate for internal domains or IP addresses, with a new key.",
		Annotations: changes("Sign a certificate", false),
	}, func(ctx context.Context, in struct {
		Authority string   `json:"authority" jsonschema:"the name of the CA, from list_authorities"`
		Domains   []string `json:"domains" jsonschema:"the domains or IP addresses"`
		Days      int      `json:"days,omitempty" jsonschema:"how long it is valid (default 365)"`
		KeyType   string   `json:"key_type,omitempty" jsonschema:"ec (default) or rsa"`
		Name      string   `json:"name,omitempty" jsonschema:"its name (default: the first domain)"`
		Replace   bool     `json:"replace,omitempty" jsonschema:"replace a certificate of the same name"`
	}) ([]byte, error) {
		if in.Days == 0 {
			in.Days = 365
		}
		if in.KeyType == "" {
			in.KeyType = "ec"
		}
		return u.call(ctx, "POST", "/api/certificates/sign", map[string]any{
			"authority": in.Authority, "domains": in.Domains, "days": in.Days,
			"key_type": in.KeyType, "name": in.Name, "replace": in.Replace,
		})
	})
	tool(srv, &mcp.Tool{
		Name:        "upload_certificate",
		Description: "Uploads a certificate from another provider with its private key, in PEM. Sites serving a certificate of the same name switch to it if replace is set.",
		Annotations: changes("Upload a certificate", true),
	}, func(ctx context.Context, in struct {
		Cert    string `json:"cert" jsonschema:"the certificate chain in PEM"`
		Key     string `json:"key" jsonschema:"its unencrypted private key in PEM"`
		Name    string `json:"name,omitempty" jsonschema:"its name (default: its first domain)"`
		Replace bool   `json:"replace,omitempty" jsonschema:"replace the certificate of the same name"`
	}) ([]byte, error) {
		return u.call(ctx, "POST", "/api/certificates/upload",
			map[string]any{"cert": in.Cert, "key": in.Key, "name": in.Name, "replace": in.Replace})
	})
	tool(srv, &mcp.Tool{
		Name:        "rename_certificate",
		Description: "Renames an uploaded or signed certificate; the sites serving it follow.",
		Annotations: changes("Rename a certificate", false),
	}, func(ctx context.Context, in struct {
		Name    string `json:"name" jsonschema:"its name"`
		NewName string `json:"new_name" jsonschema:"its new name"`
	}) ([]byte, error) {
		return u.call(ctx, "PATCH", "/api/certificates/"+url.PathEscape(in.Name), map[string]any{"name": in.NewName})
	})
	tool(srv, &mcp.Tool{
		Name:        "delete_certificate",
		Description: "Deletes an uploaded or signed certificate that no enabled site serves.",
		Annotations: changes("Delete a certificate", true),
	}, func(ctx context.Context, in struct {
		Name string `json:"name" jsonschema:"its name"`
	}) ([]byte, error) {
		return u.call(ctx, "DELETE", "/api/certificates/"+url.PathEscape(in.Name), nil)
	})
}

// siteRequestOf is the body of a preview of a site.
func siteRequestOf(id string, cfg map[string]any) map[string]any {
	return map[string]any{"id": id, "config": cfg}
}

// site returns the site with the given ID, if the user may see it.
func (u *mcpUser) site(ctx context.Context, id string) (store.Site, error) {
	body, err := u.call(ctx, "GET", "/api/sites", nil)
	if err != nil {
		return store.Site{}, err
	}
	var list struct {
		Sites []store.Site `json:"sites"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return store.Site{}, err
	}
	for _, s := range list.Sites {
		if s.ID == id {
			return s, nil
		}
	}
	return store.Site{}, fmt.Errorf("there is no site %q that you may see; list_sites lists them", id)
}

// siteNginx returns the nginx configuration generated for the site with
// the given ID and configuration alone.
func (u *mcpUser) siteNginx(ctx context.Context, id string, cfg json.RawMessage) (string, error) {
	body, err := u.call(ctx, "POST", "/api/sites/preview", map[string]any{"id": id, "config": cfg})
	if err != nil {
		return "", err
	}
	var p struct {
		Site string `json:"site"`
	}
	err = json.Unmarshal(body, &p)
	return p.Site, err
}

// lineDiff returns the lines of b that differ from a, prefixed with "+",
// those of a that b lacks with "-", and unchanged ones with " ", collapsing
// long unchanged runs. It is meant for the configuration of one site.
func lineDiff(a, b string) string {
	x, y := lines(a), lines(b)
	if len(x)*len(y) > 4_000_000 {
		return "(too long to compare)"
	}
	// lcs[i][j] is the length of the longest common subsequence of x[i:]
	// and y[j:].
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			out = append(out, " "+x[i])
			i, j = i+1, j+1
		case j < len(y) && (i == len(x) || lcs[i][j+1] >= lcs[i+1][j]):
			out = append(out, "+"+y[j])
			j++
		default:
			out = append(out, "-"+x[i])
			i++
		}
	}
	if !slicesContainChange(out) {
		return "(no change)"
	}
	return strings.Join(collapse(out, 3), "\n")
}

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func slicesContainChange(diff []string) bool {
	for _, l := range diff {
		if l[0] != ' ' {
			return true
		}
	}
	return false
}

// collapse keeps context unchanged lines around the changes of a diff, and
// replaces longer unchanged runs with "...".
func collapse(diff []string, context int) []string {
	near := make([]bool, len(diff))
	for i, l := range diff {
		if l[0] == ' ' {
			continue
		}
		for k := max(0, i-context); k <= min(len(diff)-1, i+context); k++ {
			near[k] = true
		}
	}
	var out []string
	skipped := false
	for i, l := range diff {
		if near[i] {
			out = append(out, l)
			skipped = false
		} else if !skipped {
			out = append(out, " ...")
			skipped = true
		}
	}
	return out
}
