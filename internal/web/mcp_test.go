package web_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// tokenOf creates an API token for the user of c, and returns its secret.
func tokenOf(t *testing.T, c *client) string {
	t.Helper()
	var resp struct{ Secret string }
	body := c.want("POST", "/api/auth/tokens", `{"name": "test", "expires_in_days": 1}`, 201, `"prefix":"sp_`)
	if err := json.Unmarshal([]byte(body), &resp); err != nil || !strings.HasPrefix(resp.Secret, "sp_") {
		t.Fatalf("no secret in %s: %v", body, err)
	}
	return resp.Secret
}

// bearer adds an API token to requests.
type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// mcpClient connects to the MCP endpoint of srv with an API token.
func mcpClient(t *testing.T, srv *httptest.Server, secret string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer(secret)},
	}, nil)
	if err != nil {
		t.Fatalf("connecting to MCP: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func toolNames(t *testing.T, s *mcp.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// call calls a tool, and returns its text, and whether it failed.
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String(), res.IsError
}

func TestMCP(t *testing.T) {
	h := newServer(t, options{})
	srv := httptest.NewServer(h)
	defer srv.Close()
	admin := as(t, h, "admin", adminPassword)
	a := idOf(t, admin.want("POST", "/api/sites", `{"config": {"server_name": "a.example.com", "options": ["client_max_body_size 1m"], "default": {"port": 1}}}`, 201))
	b := idOf(t, admin.want("POST", "/api/sites", `{"config": {"server_name": "b.example.com", "default": {"port": 2}}}`, 201))
	for _, u := range []string{
		fmt.Sprintf(`{"username": "site", "password": "site-password", "role": "site_operator", "sites": [%q]}`, a),
		`{"username": "platform", "password": "platform-password", "role": "platform_operator"}`,
		`{"username": "nobody", "password": "nobody-password"}`,
	} {
		admin.want("POST", "/api/users", u, 201)
	}

	// The tools follow the role.
	nobody := mcpClient(t, srv, tokenOf(t, as(t, h, "nobody", "nobody-password")))
	if got := toolNames(t, nobody); !slices.Equal(got, []string{"whoami"}) {
		t.Errorf("tools without a role = %q", got)
	}
	site := mcpClient(t, srv, tokenOf(t, as(t, h, "site", "site-password")))
	names := toolNames(t, site)
	for _, want := range []string{"whoami", "list_sites", "get_site", "preview_site", "update_site", "set_site_enabled", "get_status", "list_certificates"} {
		if !slices.Contains(names, want) {
			t.Errorf("site operator lacks %s: %q", want, names)
		}
	}
	for _, not := range []string{"create_site", "delete_site", "get_nginx_config", "apply_sites", "issue_certificate", "delete_certificate"} {
		if slices.Contains(names, not) {
			t.Errorf("site operator has %s", not)
		}
	}

	// A site operator sees and changes their site only, as in the console.
	if text, _ := call(t, site, "whoami", nil); !strings.Contains(text, `"username":"site"`) || !strings.Contains(text, `"all_sites":false`) {
		t.Errorf("whoami = %s", text)
	}
	text, failed := call(t, site, "list_sites", nil)
	if failed || !strings.Contains(text, "a.example.com") || strings.Contains(text, "b.example.com") {
		t.Errorf("list_sites = %s", text)
	}
	text, failed = call(t, site, "get_site", map[string]any{"id": a})
	if failed || !strings.Contains(text, `"generation":1`) || !strings.Contains(text, "proxy_pass http://127.0.0.1:1;") {
		t.Errorf("get_site = %s", text)
	}
	if text, failed := call(t, site, "get_site", map[string]any{"id": b}); !failed || !strings.Contains(text, "no site") {
		t.Errorf("get_site of another site = %s", text)
	}
	newConfig := map[string]any{"server_name": "a.example.com", "options": []string{"client_max_body_size 1m"}, "default": map[string]any{"port": 5}}
	text, failed = call(t, site, "preview_site", map[string]any{"id": a, "config": newConfig})
	if failed || !strings.Contains(text, `-    proxy_pass http://127.0.0.1:1;`) || !strings.Contains(text, `+    proxy_pass http://127.0.0.1:5;`) {
		t.Errorf("preview_site = %s", text)
	}
	text, failed = call(t, site, "update_site", map[string]any{"id": a, "config": newConfig, "generation": 1})
	if failed || !strings.Contains(text, `"generation":2`) {
		t.Errorf("update_site = %s", text)
	}
	if text, failed := call(t, site, "update_site", map[string]any{"id": a, "config": newConfig, "generation": 1}); !failed || !strings.Contains(text, "changed by someone else") {
		t.Errorf("update_site at an old generation = %s", text)
	}
	other := map[string]any{"server_name": "b.example.com", "default": map[string]any{"port": 9}}
	if text, failed := call(t, site, "update_site", map[string]any{"id": b, "config": other, "generation": 0}); !failed || !strings.Contains(text, "(forbidden)") {
		t.Errorf("update_site of another site = %s", text)
	}
	noOptions := map[string]any{"server_name": "a.example.com", "default": map[string]any{"port": 5}}
	if text, failed := call(t, site, "update_site", map[string]any{"id": a, "config": noOptions, "generation": 0}); !failed || !strings.Contains(text, "directives_restricted") {
		t.Errorf("update_site changing directives = %s", text)
	}
	invalid := map[string]any{"server_name": "a.example.com", "options": []string{"client_max_body_size 1m"}, "default": map[string]any{"port": 70000}}
	if text, failed := call(t, site, "preview_site", map[string]any{"id": a, "config": invalid}); !failed || !strings.Contains(text, "servers[0].default.port: 70000 is not a valid port") {
		t.Errorf("preview_site of an invalid configuration = %s", text)
	}
	text, failed = call(t, site, "set_site_enabled", map[string]any{"id": a, "enabled": false})
	if failed || !strings.Contains(text, `"enable":false`) {
		t.Errorf("set_site_enabled = %s", text)
	}
	// Tools the role does not allow do not exist for the user.
	if _, err := site.CallTool(t.Context(), &mcp.CallToolParams{Name: "create_site", Arguments: map[string]any{"config": other}}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("create_site as a site operator: %v, want an unknown tool", err)
	}

	// A platform operator manages all sites.
	platform := mcpClient(t, srv, tokenOf(t, as(t, h, "platform", "platform-password")))
	text, failed = call(t, platform, "create_site", map[string]any{"config": map[string]any{"server_name": "c.example.com", "default": map[string]any{"port": 3}}})
	if failed || !strings.Contains(text, "c.example.com") {
		t.Fatalf("create_site = %s", text)
	}
	c := idOf(t, text)
	if text, failed := call(t, platform, "get_nginx_config", nil); failed || !strings.Contains(text, "server_name c.example.com;") || strings.Contains(text, "server_name a.example.com;") {
		t.Errorf("get_nginx_config = %s", text)
	}
	if text, failed := call(t, platform, "delete_site", map[string]any{"id": c}); failed || text != `{"ok":true}` {
		t.Errorf("delete_site = %s", text)
	}
	if names := toolNames(t, platform); slices.Contains(names, "issue_certificate") {
		t.Errorf("platform operator has issue_certificate")
	}

	// Administrators manage certificates.
	if names := toolNames(t, mcpClient(t, srv, tokenOf(t, admin))); !slices.Contains(names, "sign_certificate") || !slices.Contains(names, "delete_certificate") {
		t.Errorf("administrator tools = %q", names)
	}
}

func TestMCPNeedsAToken(t *testing.T) {
	h := newServer(t, options{})
	srv := httptest.NewServer(h)
	defer srv.Close()
	admin := as(t, h, "admin", adminPassword)
	secret := tokenOf(t, admin)

	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	for _, bad := range []string{"", "sp_wrong"} {
		_, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{
			Endpoint:   srv.URL + "/mcp",
			HTTPClient: &http.Client{Transport: bearer(bad)},
		}, nil)
		if err == nil {
			t.Errorf("connected with token %q", bad)
		}
	}
	// Tokens also work for the HTTP API, and stop working once revoked.
	api := &client{t: t, h: h, header: http.Header{"Authorization": {"Bearer " + secret}}}
	api.want("GET", "/api/auth/me", "", 200, `"username":"admin"`)
	var list struct {
		Tokens []struct{ ID string }
	}
	if err := json.Unmarshal([]byte(admin.want("GET", "/api/auth/tokens", "", 200, `"name":"test"`)), &list); err != nil || len(list.Tokens) != 1 {
		t.Fatalf("tokens = %+v, %v", list, err)
	}
	admin.want("POST", "/api/auth/tokens", `{"name": "", "expires_in_days": -1}`, 400, `"field":"name"`, `"code":"required"`, `"code":"invalid_days"`)
	admin.want("DELETE", "/api/auth/tokens/nope", "", 404)
	admin.want("DELETE", "/api/auth/tokens/"+list.Tokens[0].ID, "", 204)
	api.want("GET", "/api/auth/me", "", 401)

	// Browsers cannot be made to call the tools from other sites.
	secret = tokenOf(t, admin)
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site MCP request = %d, want 403", rec.Code)
	}
}
