# Simple Proxy

A web console that manages nginx as a reverse proxy, in the spirit of
Nginx Proxy Manager. Add a site (its domains and the upstream service to
forward them to), and saving applies it to nginx right away.

- **Sites:** domains, upstream host and port, extra routes for path
  prefixes, WebSocket, buffering, caching, gzip and raw nginx directives.
- **HTTPS:** pick a certificate managed by acme.sh, or request a new one from
  the UI (HTTP-01 through nginx, or DNS-01 with an acme.sh DNS API). acme.sh
  renews certificates and reloads nginx.
- **Safe changes:** every change is tested with `nginx -t` before nginx
  reloads. If nginx rejects it, nothing changes, and the UI shows nginx's
  output. Problems such as invalid ports or domains used by another site are
  shown next to the inputs.
- **Import/export:** `config.yaml` files of legacy-proxy-generator can be
  imported, and the sites exported in the same format.
- English and Chinese, following the browser's language.

Sites are stored in an SQLite database, `sites.db`, in the directory of the
executable. The console has no login, so it listens on 127.0.0.1 by default;
reach it through an SSH tunnel or a VPN.

## Run

```bash
./build.sh                          # builds dist/simple-proxy-<os>-<arch>
dist/simple-proxy-linux-amd64 -nginx-conf /etc/nginx/conf.d/simple-proxy.conf \
  -acme-webroot /var/www/acme      # serves http://127.0.0.1:8080
```

`build.sh` builds static, stripped binaries, rebuilding the UI first when npm
is available. It also cross-compiles: `./build.sh linux/arm64` or
`./build.sh all`. Plain `go build .` works too.

| Flag             | Default                 | Description |
| ---------------- | ----------------------- | ----------- |
| `-addr`          | `127.0.0.1:8080`        | Address to serve the console on. |
| `-db`            | `sites.db` next to the executable | SQLite database storing the sites. Set it when using `go run`, whose executable is temporary. |
| `-nginx-conf`    | none                    | The configuration file to manage, which nginx must include in its `http` block. Without it, sites are only stored. |
| `-nginx-test`    | `nginx -t`              | Command testing nginx's configuration. |
| `-nginx-reload`  | `nginx -s reload`       | Command reloading nginx; acme.sh also runs it after renewing certificates. |
| `-acme`          | `~/.acme.sh/acme.sh`    | The acme.sh script. Without it, HTTPS is unavailable. |
| `-acme-webroot`  | none                    | Directory that nginx serves at `/.well-known/acme-challenge/` on port 80, to request certificates over HTTP. Without it, only DNS validation is available. |
| `-fallback-cert` | `/certs/fallback.cer`   | Certificate presented to HTTPS requests for unknown hosts. |
| `-fallback-key`  | `/certs/fallback.key`   | Private key of `-fallback-cert`. |

Commands are split at spaces and run without a shell; use a script for
anything more complex.

### How changes are applied

On every change, and once on startup, the configuration of all enabled sites
is written to `-nginx-conf`, tested with `-nginx-test`, and loaded with
`-nginx-reload`. If the test or the reload fails, the previous file is
restored, so the file always holds a configuration nginx accepted. The header
shows whether nginx runs the stored sites; its dialog shows the commands, the
last error and the generated configuration, and can apply the sites again,
e.g. after nginx restarted.

The configuration always contains catch-all servers that reject requests for
unknown hosts on ports 80 and 443. The latter needs a certificate, e.g. a
self-signed one:

```bash
openssl req -x509 -newkey rsa:4096 -nodes -days 3650 -subj "/CN=example.com" \
  -keyout fallback.key -out fallback.cer
```

### nginx in a container

The configuration refers to certificates where acme.sh keeps them, and to
`-acme-webroot`, so mount those into the container at the same paths. The
file is rewritten in place, so it may be mounted on its own as well as with
its directory.

```bash
podman run -d --name nginx --network host \
  -v /srv/proxy/conf.d:/etc/nginx/conf.d \
  -v /srv/proxy/certs:/certs:ro \
  -v $HOME/.acme.sh:$HOME/.acme.sh:ro \
  -v /var/www/acme:/var/www/acme:ro \
  docker.io/nginx:latest

simple-proxy -nginx-conf /srv/proxy/conf.d/proxy.conf \
  -nginx-test "podman exec nginx nginx -t" \
  -nginx-reload "podman exec nginx nginx -s reload" \
  -acme-webroot /var/www/acme
```

## Configuration format

Sites use the format of legacy-proxy-generator's `config.yaml`, which the
console imports and exports. See [config.yaml.example](config.yaml.example);
JSON is accepted too.

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

The console is built on this API, which scripts can use as well. A site's
`config` is one entry of `servers` above, in JSON.

| Request                   | Response |
| ------------------------- | -------- |
| `GET /api/sites`          | `{"sites": [{"id": "...", "config": {...}}]}` |
| `POST /api/sites`         | Add a site: `{"config": {...}}`. |
| `PUT /api/sites/{id}`     | Change a site: `{"config": {...}}`. |
| `DELETE /api/sites/{id}`  | Remove a site. |
| `PUT /api/sites`          | Replace all sites: `{"configs": [{...}]}`. |
| `GET /api/status`         | How sites are applied to nginx, and the last error. |
| `POST /api/apply`         | Apply the stored sites again. |
| `GET /api/nginx.conf`     | The configuration generated from the sites. |
| `GET /api/certificates`   | The certificates acme.sh manages. |
| `POST /api/certificates`  | Request one: `{"domains": ["example.com"], "dns": "dns_cf"}`; without `dns`, over HTTP. |
| `POST /api/generate`      | The configuration generated from a YAML or JSON configuration, without storing anything. |
| `POST /api/validate`      | `204` if the configuration in the body is valid. |

Changes respond `400` for invalid sites, `422` for missing certificates, and
`502` with nginx's output if nginx rejects the configuration. Errors are JSON:

```json
{"message": "...", "output": "nginx: [emerg] ...",
 "fields": [{"field": "servers[0].default.port", "message": "70000 is not a valid port", "code": "invalid_port", "value": "70000"}]}
```

The `code` (see `config.ProblemCode`, plus `cert_not_found`) lets clients
describe a problem in their own words, as the UI does in Chinese. `generate`
and `validate` answer in plain text unless asked for JSON:

```bash
curl --data-binary @config.yaml http://127.0.0.1:8080/api/generate > proxy.conf
```

## Development

```
main.go            flags and wiring
internal/config    configuration format, defaults and validation
internal/nginx     configuration generator (nginx.conf.tmpl)
internal/apply     writes, tests and reloads nginx's configuration
internal/acme      certificates through acme.sh
internal/store     SQLite storage of the sites (modernc.org/sqlite, no cgo)
internal/manager   site changes: check, apply, then store
internal/web       HTTP API, embeds the UI from internal/web/dist
web/               UI: React, Tailwind CSS and shadcn/ui
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
- Certificates are referenced where acme.sh keeps them rather than copied
  (`--copy-cert`); mount them into an nginx container at the same paths.
