package config_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kiritow/simple-proxy/internal/config"
)

func TestParseDefaults(t *testing.T) {
	cfg, err := config.Parse([]byte(`
servers:
- server_name: plain.example.com www.example.com
  default:
    port: 3000
  locations:
  - path: /api
  - path: /other
    ip: 10.0.0.2
    port: 4000
- server_name: secure.example.com
  cert_name: "*.example.com"
  default:
    ip: 10.0.0.1
    port: 3000
  locations:
  - path: /api
- server_name: off.example.com
  enable: false
  https: false
  cert_name: off.example.com
  default:
    enable: false
`))
	if err != nil {
		t.Fatal(err)
	}

	plain, secure, off := cfg.Servers[0], cfg.Servers[1], cfg.Servers[2]
	for _, tc := range []struct {
		name      string
		got, want any
	}{
		{"plain.UseHTTPS", plain.UseHTTPS(), false},
		{"plain.Port", plain.Port(), 80},
		{"plain.Certificate", plain.Certificate(), "plain.example.com"},
		{"plain.Default.Path", plain.Default.Path, "/"},
		{"plain.Default.IP", plain.Default.IP, config.DefaultUpstreamIP},
		{"plain.Locations[0].IP", plain.Locations[0].IP, config.DefaultUpstreamIP},
		{"plain.Locations[0].Port", plain.Locations[0].Port, 3000},
		{"plain.Locations[1].IP", plain.Locations[1].IP, "10.0.0.2"},
		{"plain.Locations[1].Port", plain.Locations[1].Port, 4000},
		{"secure.UseHTTPS", secure.UseHTTPS(), true},
		{"secure.Port", secure.Port(), 443},
		{"secure.Certificate", secure.Certificate(), "*.example.com"},
		{"secure.Locations[0].IP", secure.Locations[0].IP, "10.0.0.1"},
		{"off.Enabled", off.Enabled(), false},
		{"off.UseHTTPS", off.UseHTTPS(), false},
		{"off.Default.Enabled", off.Default.Enabled(), false},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

func TestParseDirectives(t *testing.T) {
	cfg, err := config.Parse([]byte(`
servers:
- server_name: example.com
  options:
  - add_header X-Example "a  b"
  - "client_max_body_size 64m;"
  - key: proxy_read_timeout
    value: 300s
  - internal
  default:
    port: 3000
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []config.Directive{
		{Name: "add_header", Args: `X-Example "a  b"`},
		{Name: "client_max_body_size", Args: "64m"},
		{Name: "proxy_read_timeout", Args: "300s"},
		{Name: "internal"},
	}
	if got := cfg.Servers[0].Options; !reflect.DeepEqual(got, want) {
		t.Errorf("Options = %q, want %q", got, want)
	}
}

func TestParseJSON(t *testing.T) {
	_, err := config.Parse([]byte(`{"servers": [{"server_name": "example.com", "default": {"port": 3000}}]}`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       []string // substrings of the error
	}{
		{"empty", "", []string{"config is empty"}},
		{"not a mapping", "hello", []string{"cannot unmarshal"}},
		{
			"unknown field",
			"servers:\n- server_name: a\n  default: {port: 1}\n  websockets: true\n",
			[]string{"field websockets not found"},
		},
		{
			"missing fields",
			"servers:\n- default: {}\n  locations:\n  - port: 1\n",
			[]string{
				"servers[0].server_name: is required",
				"servers[0].default.port: is required",
				"servers[0].locations[0].path: is required",
			},
		},
		{
			"location port not inherited from disabled default",
			"servers:\n- server_name: a\n  default: {enable: false}\n  locations:\n  - path: /api\n",
			[]string{"servers[0].locations[0].port: is required"},
		},
		{
			"out of range",
			"servers:\n- server_name: a\n  listen_port: 70000\n  default: {port: -1}\n",
			[]string{"listen_port: 70000 is not a valid port", "default.port: -1 is not a valid port"},
		},
		{
			"injection through names",
			"servers:\n- server_name: 'a;b'\n  cert_name: \"c\\nd\"\n  default: {port: 1, path: '/x{', ip: 'h;'}\n",
			[]string{"server_name: invalid name", "cert_name: invalid name", "default.path:", "default.ip:"},
		},
		{
			"relative path",
			"servers:\n- server_name: a\n  default: {port: 1, path: api}\n",
			[]string{`default.path: "api" is not a URL path`},
		},
		{
			"bad options",
			`servers:
- server_name: a
  default: {port: 1}
  options:
  - "Add-Header x"
  - "add_header X a; return 200"
  - "add_header X \"open"
  - "add_header X a\nb"
  - "add_header X a }"
  - [not, a, directive]
`,
			[]string{
				"an option must be a string or a mapping",
			},
		},
		{
			"bad option contents",
			`servers:
- server_name: a
  default: {port: 1}
  options:
  - "Add-Header x"
  - "add_header X a; return 200"
  - "add_header X \"open"
  - "add_header X a\nb"
  - "add_header X a }"
  - "add_header X a\\"
`,
			[]string{
				`options[0]: invalid directive name "Add-Header"`,
				`options[1]: ";" must be quoted`,
				"options[2]: has an unterminated quote",
				"options[3]: must not contain line breaks",
				`options[4]: "}" must be quoted`,
				"options[5]: ends with a backslash",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatal("Parse succeeded, want error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not contain %q:\n%v", want, err)
				}
			}
		})
	}
}

func TestParseAcceptsQuotedSpecialCharacters(t *testing.T) {
	_, err := config.Parse([]byte(`
servers:
- server_name: example.com
  options:
  - add_header Content-Security-Policy "default-src 'self'; img-src *"
  - add_header X-Escaped a\;b
  default:
    port: 3000
`))
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseFieldErrors(t *testing.T) {
	_, err := config.Parse([]byte(`
servers:
- server_name: example.com
  default: {port: 0}
  locations:
  - path: /api
    port: 70000
    options: ["add_header X a;b"]
`))
	var errs config.FieldErrors
	if !errors.As(err, &errs) {
		t.Fatalf("Parse error = %v, want FieldErrors", err)
	}
	var got []string
	for _, fe := range errs {
		got = append(got, fe.Field)
	}
	want := []string{
		"servers[0].default.port",
		"servers[0].locations[0].port",
		"servers[0].locations[0].options[0]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %q, want %q", got, want)
	}
}

func TestParseSyntaxError(t *testing.T) {
	for _, doc := range []string{"servers: [unclosed", "servers: [{websockets: true}]", ""} {
		_, err := config.Parse([]byte(doc))
		var syntax *config.SyntaxError
		var errs config.FieldErrors
		if !errors.As(err, &syntax) || errors.As(err, &errs) {
			t.Errorf("Parse(%q) error = %v, want a *SyntaxError", doc, err)
		}
	}
}

func TestDecodeServer(t *testing.T) {
	// An incomplete draft decodes, since nothing is validated...
	s, err := config.DecodeServer([]byte(`{"server_name": "", "default": {"port": 70000}}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.Default.Port != 70000 {
		t.Errorf("Default.Port = %d, want 70000", s.Default.Port)
	}
	// ...but unknown fields and wrong types do not.
	for _, bad := range []string{`{"websockets": true}`, `{"listen_port": "x"}`, ``} {
		if _, err := config.DecodeServer([]byte(bad)); err == nil {
			t.Errorf("DecodeServer(%q) succeeded, want an error", bad)
		}
	}
}

func TestProblemCodes(t *testing.T) {
	_, err := config.Parse([]byte(`
servers:
- server_name: "a;b"
  listen_port: 70000
  default: {port: 0}
  options: ["add_header X a;b"]
`))
	var errs config.FieldErrors
	if !errors.As(err, &errs) {
		t.Fatalf("Parse error = %v, want FieldErrors", err)
	}
	type problem struct {
		Field string
		Code  config.ProblemCode
		Value string
	}
	var got []problem
	for _, fe := range errs {
		var p *config.Problem
		if !errors.As(fe, &p) {
			t.Fatalf("%v is not a *Problem", fe)
		}
		got = append(got, problem{fe.Field, p.Code, p.Value})
	}
	want := []problem{
		{"servers[0].server_name", config.InvalidName, "a;b"},
		{"servers[0].listen_port", config.InvalidPort, "70000"},
		{"servers[0].options[0]", config.Unquoted, ";"},
		{"servers[0].default.port", config.PortRequired, ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems = %+v\nwant %+v", got, want)
	}
}

func TestCertNames(t *testing.T) {
	for name, want := range map[string]bool{
		"example.com": true, "*.example.com": true, "Shop – wildcard 2026": true, "a/b {c}; d": true, "内网证书": true,
		strings.Repeat("证", config.MaxCertNameLength): true,
		"": false, " padded": false, "line\nbreak": false, "tab\there": false, "\xff": false,
		strings.Repeat("x", config.MaxCertNameLength+1): false,
	} {
		if got := config.ValidCertName(name); got != want {
			t.Errorf("ValidCertName(%q) = %t, want %t", name, got, want)
		}
	}
	// Descriptive names are only used to find certificates.
	if _, err := config.Parse([]byte("servers:\n- server_name: a.example.com\n  cert_name: 'Shop {wildcard}; 2026'\n  default: {port: 1}\n")); err != nil {
		t.Errorf("a descriptive cert_name was rejected: %v", err)
	}
}
