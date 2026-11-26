# Simple Proxy

A web console that manages nginx as a reverse proxy, in the spirit of
Nginx Proxy Manager. Add a site (its domains and the upstream service to
forward them to), and saving applies it to nginx right away.

- **Sites:** domains, upstream host and port, extra routes for path
  prefixes, WebSocket, buffering, caching, gzip and raw nginx directives.
  Sites the builder cannot express, e.g. with regex locations, can be written
  as raw nginx configuration instead, starting from what the builder
  generates.
- **HTTPS:** pick a certificate managed by acme.sh, or request a new one from
  the UI (HTTP-01 through nginx, or DNS-01 with an acme.sh DNS API). acme.sh
  renews certificates and reloads nginx. Certificates from other providers
  can be uploaded with their private keys, and replaced when renewed.
- **Your own CA:** upload a certificate authority (its certificate and key)
  to sign certificates for internal sites, such as `nas.lan`, wildcards or
  IP addresses, and renew them with a click. Download the CA certificate to
  install it on the devices that should trust these sites.
- **Safe changes:** while you edit a site, a panel shows what saving would
  change in the nginx configuration, as a diff. Problems such as invalid
  ports, domains used by another site or missing certificates are shown next
  to the inputs as you type. Every change is tested with `nginx -t` before
  nginx reloads; if nginx rejects it, nothing changes, and the UI shows
  nginx's output.
- **Concurrent editing:** each site has a generation, which counts its
  changes. If another window saves or deletes a site you are editing, you
  are told right away, and can load the saved version or keep your edits
  and overwrite it; saving never overwrites someone else's change unknowingly.
- **Import/export:** `config.yaml` files of legacy-proxy-generator can be
  imported, and the sites exported in the same format.
- English and Chinese, following the browser's language.

Sites, uploaded certificates and CAs are stored in an SQLite database,
`sites.db`, in the directory of the executable. It holds private keys, so
only its owner can read it. The console has no login, so it listens on
127.0.0.1 by default; reach it through an SSH tunnel or a VPN. Anyone who can
reach it can sign certificates with your CAs.

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
| `-db`            | `sites.db` next to the executable | SQLite database storing the sites, certificates and CAs. Set it when using `go run`, whose executable is temporary. |
| `-cert-dir`      | `certs` next to `-db`   | Directory where uploaded and signed certificates are written for nginx to read. |
| `-nginx-conf`    | none                    | The configuration file to manage, which nginx must include in its `http` block. Without it, sites are only stored. |
| `-nginx-test`    | `nginx -t`              | Command testing nginx's configuration. |
| `-nginx-reload`  | `nginx -s reload`       | Command reloading nginx; acme.sh also runs it after renewing certificates. |
| `-acme`          | `~/.acme.sh/acme.sh`    | The acme.sh script. Without it, only uploaded and signed certificates are available. |
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

### Uploaded and signed certificates

Certificates kept by simple-proxy are written to `-cert-dir` as
`<hash>/fullchain.pem` and `privkey.pem`, named after a hash of their
contents (keys are readable only by the owner; nginx reads them as root).
Each version of a certificate gets a directory of its own, so replacing or
renewing a certificate changes the paths in the configuration: nginx tests
the new files before using them, and if it rejects them, nothing changes. Old
versions are removed once nginx runs the new ones, and the files are written
again on startup if they are missing.

Sites refer to certificates by name, whatever their source. Names of
certificates and CAs describe them to people, e.g. `Shop – wildcard 2026`:
any text on one line, up to 128 characters, which is never written into
nginx's configuration. Uploaded certificates are named after their first
domain unless named otherwise, as sites look for that name by default. A name
is used by one certificate only: one uploaded or signed here takes precedence
over acme.sh's, so neither can take a name the other has. While a certificate
is being uploaded or signed, the console shows its details (domains, issuer,
validity, key and chain) and any problems with it. Uploads accept PEM
certificates with their chain in any order (the root is not served), and
unencrypted PKCS #1, PKCS #8 or SEC 1 keys, in the same text or separately.
Signed certificates use a new ECDSA P-256 or RSA 2048 key, and the CA's
chain without its root.

### Docker image

The [Dockerfile](Dockerfile) builds an image running simple-proxy and nginx
together, with acme.sh: nginx serves ports 80 and 443, and the console port
81. Sites, uploaded and signed certificates, CAs and acme.sh's account and
certificates are kept in the volume `/data`.

```bash
docker build -t simple-proxy .
docker run -d --name simple-proxy --restart unless-stopped \
  -p 80:80 -p 443:443 -p 127.0.0.1:81:81 \
  -v simple-proxy:/data \
  simple-proxy
```

The console has no login: publish port 81 on 127.0.0.1 only, as above, and
reach it through an SSH tunnel or a VPN.

- **Upstreams** are reached from inside the container, where `127.0.0.1` is
  the container itself. For services on the host, add
  `--add-host host.docker.internal:host-gateway` and use that name as the
  upstream host, or run with `--network host`. With host networking the
  console listens on port 81 of every interface, so append
  `-addr 127.0.0.1:81`.
- **Flags** given after the image name are passed to simple-proxy and
  override the image's defaults, e.g. `-addr`; any other command runs instead
  of the services, e.g. `bash`.
- **acme.sh** requests certificates from Let's Encrypt; set `ACME_SERVER`
  to use another CA, such as `zerossl`. Pass the credentials of a DNS API as
  environment variables, e.g. `-e CF_Token=...`; acme.sh keeps them in
  `/data/acme.sh` once used. It checks for certificates to renew daily, and
  reloads nginx after renewing one.
- The certificate of the catch-all HTTPS server is generated in `/data` on
  first start.
- If nginx or simple-proxy exits, so does the container, for its restart
  policy to start it again. nginx starts without the sites, which
  simple-proxy applies right after, so that sites nginx rejects cannot keep
  the container from starting.
- Build arguments: `NGINX_IMAGE` (default `nginx:stable`) and
  `ACME_SH_VERSION`.

### nginx in a container

The configuration refers to certificates where acme.sh keeps them, to
`-cert-dir`, and to `-acme-webroot`, so mount those into the container at the
same paths. The file is rewritten in place, so it may be mounted on its own as
well as with its directory.

```bash
podman run -d --name nginx --network host \
  -v /srv/proxy/conf.d:/etc/nginx/conf.d \
  -v /srv/proxy/certs:/certs:ro \
  -v /srv/proxy/local-certs:/srv/proxy/local-certs:ro \
  -v $HOME/.acme.sh:$HOME/.acme.sh:ro \
  -v /var/www/acme:/var/www/acme:ro \
  docker.io/nginx:latest

simple-proxy -nginx-conf /srv/proxy/conf.d/proxy.conf \
  -cert-dir /srv/proxy/local-certs \
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
  cert_name: "*.example.com"  # certificate name, default: first server_name
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

A site with `raw` is configured by that text alone, written by hand: nginx
configuration for the `http` context, such as server blocks, generated as is.
Only `enable` may be set besides it. Certificates are referred to by name, and
their paths filled in, so that raw sites follow renewals and replacements:

```yaml
- raw: |
    server {
      listen 443 ssl;
      server_name example.com;
      ssl_certificate     {{cert "example.com"}};
      ssl_certificate_key {{key "example.com"}};
      location ~ ^/api/v[0-9]+/ { proxy_pass http://127.0.0.1:3001; }
    }
```

simple-proxy checks blocks, quotes and references, finds the `server_name`s
to report domains used by other sites, and leaves the rest to `nginx -t`.
Raw sites get only what they contain: no redirect to HTTPS and no ACME
challenges unless written in them, as the builder writes them when converting
a site.

When server names are too long for nginx's default
`server_names_hash_bucket_size`, the configuration sets a larger one; it must
then not be set elsewhere in nginx's configuration.

## HTTP API

The console is built on this API, which scripts can use as well. A site's
`config` is one entry of `servers` above, in JSON.

| Request                   | Response |
| ------------------------- | -------- |
| `GET /api/sites`          | `{"sites": [{"id": "...", "config": {...}, "generation": 1}]}` |
| `POST /api/sites`         | Add a site: `{"config": {...}}`. |
| `PUT /api/sites/{id}`     | Change a site: `{"config": {...}, "generation": 1}`. |
| `DELETE /api/sites/{id}`  | Remove a site: `?generation=1`. |
| `POST /api/sites/raw` | The configuration the builder generates for a site, as raw configuration: `{"config": {...}}` gives `{"raw": "..."}`. |
| `POST /api/sites/preview` | What saving a site would change, without saving it: `{"id": "...", "config": {...}}` (no `id` for a new site) gives `{"current": "...", "proposed": "...", "generation": 1}`. |
| `PUT /api/sites`          | Replace all sites: `{"configs": [{...}]}`. |
| `GET /api/status`         | How sites are applied to nginx, and the last error. |
| `POST /api/apply`         | Apply the stored sites again. |
| `GET /api/nginx.conf`     | The configuration generated from the sites. |
| `GET /api/certificates`   | All certificates: `{"certificates": [{"name": "...", "source": "acme", ...}], "acme_error": "..."}`, where `source` is `acme`, `uploaded` or `signed`. |
| `POST /api/certificates`  | Request one from acme.sh: `{"domains": ["example.com"], "dns": "dns_cf"}`; without `dns`, over HTTP. |
| `POST /api/certificates/upload` | Upload one: `{"name": "...", "cert": "PEM", "key": "PEM", "replace": false}`; without `name`, it is named after its first domain. |
| `POST /api/certificates/sign` | Have a CA sign one: `{"authority": "...", "domains": ["nas.lan"], "days": 365, "key_type": "ec", "name": "...", "replace": false}`; `key_type` is `ec` or `rsa`. |
| `POST /api/certificates/upload/preview`, `/sign/preview` | What uploading or signing would store, without storing it: `{"name": "...", "subject": "...", "issuer": "...", "domains": [...], "not_after": "...", "chain": [...], "has_key": true, "problems": [...]}`, as far as it can be read, with the problems that would make it fail. |
| `DELETE /api/certificates/{name}` | Delete an uploaded or signed certificate that no enabled site uses. |
| `GET /api/authorities`    | The CAs: `{"authorities": [{"name": "...", "subject": "...", "expires": "...", "signed": 1}]}`. |
| `POST /api/authorities`   | Upload a CA: `{"name": "...", "cert": "PEM", "key": "PEM"}`; without `name`, it is named after its common name. `POST /api/authorities/preview` previews it. |
| `DELETE /api/authorities/{name}` | Delete a CA; the certificates it signed stay. |
| `GET /api/authorities/{name}/cert.pem` | The CA's certificate, to install on clients. |
| `POST /api/generate`      | The configuration generated from a YAML or JSON configuration, without storing anything. |
| `POST /api/validate`      | `204` if the configuration in the body is valid. |

Changes and deletions carry the generation of the site they are based on,
and fail with `409` if the site has changed since; without a generation they
always apply. Changes respond `400` for invalid sites, `422` for missing
certificates, and `502` with nginx's output if nginx rejects the
configuration. Private keys are never returned. Errors are JSON:

```json
{"message": "...", "output": "nginx: [emerg] ...",
 "fields": [{"field": "servers[0].default.port", "message": "70000 is not a valid port", "code": "invalid_port", "value": "70000"}]}
```

The `code` (see `config.ProblemCode` and `certs.ProblemCode`, plus
`cert_not_found`) lets clients
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
internal/certs     uploaded certificates and CAs: parsing, signing, files
internal/store     SQLite storage of sites, certificates and CAs (modernc.org/sqlite, no cgo)
internal/manager   site and certificate changes: check, apply, then store
internal/web       HTTP API, embeds the UI from internal/web/dist
web/               UI: React, Tailwind CSS and shadcn/ui
web/src/lib/i18n/  the UI's messages in English (en.ts) and Chinese (zh.ts)
Dockerfile         the image with nginx and acme.sh; docker/entrypoint.sh runs them
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
