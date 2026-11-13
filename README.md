# Simple Proxy

Build nginx reverse proxy configuration (the `http` context part) in a web
UI, or generate it from a simple YAML file over HTTP.

The UI guides you through three steps: **Sites** (domains, HTTPS with a
certificate picked from acme.sh, gzip), **Routes** (path prefixes and their
upstreams, WebSocket, buffering and caching) and **Preview**, which shows the
generated `proxy.conf`. Problems are pointed out next to the inputs as you
type. Existing `config.yaml` files can be imported, and the configuration can
be exported as YAML again.

The UI is available in English and Chinese. It follows the browser's
preferred languages and falls back to English; the language can also be
chosen in the header.

Sites are saved as you edit them in an SQLite database, `sites.db`, in the
directory of the executable. If two windows edit the sites at once, the one
that saves second is asked to reload instead of overwriting the other.

This is a Go rewrite of legacy-proxy-generator and accepts the same
configuration files. Certificates are looked up with
[acme.sh](https://github.com/acmesh-official/acme.sh).

## Run

```bash
./build.sh                          # builds dist/simple-proxy-<os>-<arch>
dist/simple-proxy-linux-amd64       # serves http://127.0.0.1:8080
```

`build.sh` builds static, stripped binaries, rebuilding the UI first when npm
is available. It also cross-compiles: `./build.sh linux/arm64` or
`./build.sh all`. Plain `go build .` works too.

| Flag             | Default                 | Description                                              |
| ---------------- | ----------------------- | -------------------------------------------------------- |
| `-addr`          | `127.0.0.1:8080`        | Address to serve HTTP on. There is no authentication, so keep it private. |
| `-db`            | `sites.db` next to the executable | SQLite database storing the sites. Set it when using `go run`, whose executable is temporary. |
| `-acme`          | `~/.acme.sh/acme.sh`    | The acme.sh script used to look up certificates.         |
| `-fallback-cert` | `/certs/fallback.cer`   | Certificate presented to requests for unknown hosts.     |
| `-fallback-key`  | `/certs/fallback.key`   | Private key of `-fallback-cert`.                         |

The generated configuration always contains catch-all servers that reject
requests for unknown hosts on ports 80 and 443. The latter needs a certificate,
e.g. a self-signed one:

```bash
openssl req -x509 -newkey rsa:4096 -nodes -days 3650 -subj "/CN=example.com" \
  -keyout fallback.key -out fallback.cer
```

## Configuration

See [config.yaml.example](config.yaml.example). JSON is accepted too.

```yaml
servers:
- server_name: example.com www.example.com  # nginx server_name
  enable: true             # default: true
  listen_port: 8443        # default: 80, or 443 with https
  https: true              # default: true if cert_name is set
  cert_name: "*.example.com"  # acme.sh certificate, default: first server_name
  gzip: true               # default: false
  options:                 # extra directives for the server block
  - client_max_body_size 64m
  default:                 # the location serving "/"
    enable: true
    path: /                # default: /
    ip: 127.0.0.1          # default: 127.0.0.1
    port: 3000             # required
    websocket: true        # proxy WebSocket upgrades
    disable_cache: true    # disable caching by nginx and clients
    realtime: true         # disable buffering, e.g. for server-sent events
    options:               # extra directives for the location block
    - add_header X-Example "value"
    - key: proxy_read_timeout  # the {key, value} form also works
      value: 300s
  locations:               # more locations, same fields as default
  - path: /api             # required
    port: 3001             # ip and port default to those of default
```

HTTPS servers get a companion server on port 80 that redirects to HTTPS.

Unknown fields are rejected, and all problems are reported at once. Options
must be single directives: `;`, `{`, `}` and `#` have to be quoted or escaped.

## HTTP API

| Request                 | Response                                                         |
| ----------------------- | ---------------------------------------------------------------- |
| `POST /api/generate`    | The nginx configuration generated from the YAML or JSON body.   |
| `POST /api/validate`    | `204` if the configuration in the body is valid.                 |
| `GET /api/certificates` | `{"certificates": [...]}`, the certificates acme.sh manages.     |
| `GET /api/example`      | The example configuration.                                       |
| `GET /api/sites`        | The stored sites, as `{"revision": 1, "sites": [{"id": "...", "config": {...}}]}`. |
| `PUT /api/sites`        | Replace the stored sites; the body is like the response of `GET`. |

Each stored `config` is one entry of `servers`, and may be an incomplete
draft. A `PUT` carries the revision it is based on and fails with `409` if the
sites were changed since then.

Errors return `400` for invalid configuration, `422` for missing certificates
and `500` (`503` for `/api/certificates`) if acme.sh fails. Responses are plain
text, unless the request has `Accept: application/json`: then `generate`
returns `{"config": "..."}`, and errors list the invalid fields as in
`{"message": "...", "fields": [{"field": "servers[0].default.port", "message": "70000 is not a valid port", "code": "invalid_port", "value": "70000"}]}`.
The `code` (see `config.ProblemCode`, plus `cert_not_found`) lets clients
describe a problem in their own words, as the UI does in Chinese.

```bash
curl --data-binary @config.yaml http://127.0.0.1:8080/api/generate > proxy.conf
```

## Development

```
main.go            flags and HTTP server
internal/config    YAML schema, defaults and validation
internal/nginx     configuration generator (nginx.conf.tmpl)
internal/acme      certificate lookup through acme.sh
internal/store     SQLite storage of the sites (modernc.org/sqlite, no cgo)
internal/web       HTTP handlers, embeds the UI from internal/web/dist
web/               UI: React, Tailwind CSS and shadcn/ui
web/src/lib/model.ts  the UI's model and its conversion to and from YAML
web/src/lib/i18n/  the UI's messages in English (en.ts) and Chinese (zh.ts)
```

The built UI in `internal/web/dist` is committed, so building the server only
needs Go. After changing `web/`, rebuild it:

```bash
npm --prefix web ci
go generate ./internal/web    # runs npm run build
```

`npm --prefix web run dev` serves the UI with hot reload and proxies `/api` to
a server running on the default address.

Run `go test ./...` and `npm --prefix web test` for the tests, and
`go test ./internal/nginx -update` to rewrite the golden files in
`internal/nginx/testdata` after intended output changes.

## Differences from legacy-proxy-generator

- `listen_port` with HTTPS: the redirect server listens on port 80 and
  redirects to the custom port. Previously both servers listened on
  `listen_port`, which nginx rejects.
- Setting `cert_name` enables HTTPS unless `https: false`, as documented in
  the legacy example.
- Server-level `options` are applied; they used to be ignored.
- Options may be written as strings (`- add_header X "v"`), as the legacy
  example did, besides the `{key, value}` form.
- Location `port` may be omitted to use the default location's port.
- IPv6 upstreams are bracketed in `proxy_pass`.
- Certificates are referenced in place rather than copied (`--copy-cert`).
  Mount the acme.sh directory into the nginx container at the same path, or
  keep using the legacy copy step.
