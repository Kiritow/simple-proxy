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
- **Import/export:** sites can be exported to a `config.yaml` file and
  imported from one.
- **Users and roles:** sign in with a username and password, or with an
  OpenID Connect provider. Site operators manage the sites given to them,
  platform operators all sites, and administrators everything, certificates
  and users included. The super administrators are listed in a
  configuration file; see [Users and roles](#users-and-roles).
- English and Chinese, following the browser's language.

Sites, uploaded certificates, CAs and users are stored in an SQLite
database, `sites.db`, in the directory of the executable. It holds private
keys, so only its owner can read it. The console listens on 127.0.0.1 by
default; reach it through an SSH tunnel or a VPN, or put it behind a reverse
proxy with HTTPS before opening it to others.

## Run

```bash
./build.sh                          # builds dist/simple-proxy-<os>-<arch>
dist/simple-proxy-linux-amd64 -nginx-conf /etc/nginx/conf.d/simple-proxy.conf \
  -acme-webroot /var/www/acme      # serves http://127.0.0.1:8080
```

`build.sh` builds the UI, then static, stripped binaries embedding it, so it
needs Node.js (npm) besides Go. It also cross-compiles: `./build.sh
linux/arm64` or `./build.sh all`. Plain `go build .` works too once the UI is
built (see [Development](#development)); without it, Go fails with `pattern
dist: no matching files found`.

| Flag             | Default                 | Description |
| ---------------- | ----------------------- | ----------- |
| `-addr`          | `127.0.0.1:8080`        | Address to serve the console on. |
| `-db`            | `sites.db` next to the executable | SQLite database storing the sites, certificates, CAs and users. Set it when using `go run`, whose executable is temporary. |
| `-config`        | `simple-proxy.yaml` next to `-db`, if it exists | The [configuration file](#users-and-roles): super administrators and how users sign in. |
| `-cert-dir`      | `certs` next to `-db`   | Directory where uploaded and signed certificates are written for nginx to read. |
| `-nginx-conf`    | none                    | The configuration file to manage, which nginx must include in its `http` block. Without it, sites are only stored. |
| `-nginx-test`    | `nginx -t`              | Command testing nginx's configuration. |
| `-nginx-reload`  | `nginx -s reload`       | Command reloading nginx; acme.sh also runs it after renewing certificates. |
| `-nginx-version` | `-nginx-test` with `-V` for `-t` | Command printing nginx's version and build like `nginx -V`, shown in the console, e.g. `podman exec nginx nginx -V` for `podman exec nginx nginx -t`. |
| `-acme`          | `~/.acme.sh/acme.sh`    | The acme.sh script. Without it, only uploaded and signed certificates are available. |
| `-acme-webroot`  | none                    | Directory that nginx serves at `/.well-known/acme-challenge/` on port 80, to request certificates over HTTP. Without it, only DNS validation is available. |
| `-fallback-cert` | `/certs/fallback.cer`   | Certificate presented to HTTPS requests for unknown hosts. |
| `-fallback-key`  | `/certs/fallback.key`   | Private key of `-fallback-cert`. |

Commands are split at spaces and run without a shell; use a script for
anything more complex.

### Users and roles

Everyone signs in to the console. What they may do depends on their role:

| Role              | May |
| ----------------- | --- |
| (none)            | Sign in, and nothing else. New accounts start without a role. |
| Site operator     | See and change the sites an administrator gives them, and turn them on and off, but not their raw configuration or extra directives (see below). Their previews show only their sites. |
| Platform operator | Manage all sites: add, change, delete, import and export them, apply them to nginx again, and see the whole nginx configuration. |
| Administrator     | Everything: also request, upload, sign, rename and delete certificates and CAs, manage users, and open or close signing up. |

Everyone with a role sees the certificates and CAs, to choose one for a site.
Administrators manage users on the Users page: they add accounts with a
password, give roles and sites, disable accounts (which signs them out) and
set new passwords. Roles take effect right away, also in open sessions.
Deleting a site takes it from its site operators, and so does importing
sites, which replaces all of them.
Nobody can change their own role, or disable or delete themselves, and an
enabled administrator always remains.

**The configuration file,** `simple-proxy.yaml` next to `-db` (or `-config`),
lists the super administrators, and sets up signing in. See
[simple-proxy.yaml.example](simple-proxy.yaml.example):

```yaml
admins:
- username: admin              # an account signing in with a password
  password: change-me-please   # or a hash from `simple-proxy hash-password`
- email: ops@example.com       # an account of the OIDC provider, by verified email (or subject:)
password_login: true           # the default
oidc:
  issuer: https://id.example.com/realms/main
  client_id: simple-proxy
  client_secret: "..."
  redirect_url: https://proxy.example.com/api/auth/oidc/callback
```

On every start, the super administrators are made administrators, and
accounts with a username are created if needed; the console cannot change
their role, disable or delete them, and only they change their own email and
password. With `password`, that is the account's password, which the console
cannot change. Without it, an account that is not a super administrator yet
gets a random password, which is logged once (`docker logs`), to be changed
in the console, and its sessions end: whoever signed up with that username
may not be who the file means. Usernames are for accounts signing in with a
password; list accounts of the OIDC provider by `email` or `subject`.
Super administrators removed from the file lose their role on the next
start, and those listed by email when the provider reports another one. Without any super administrator or administrator, simple-proxy
creates `admin` with a random password, and logs it. To get back in after
losing a password, set one in the file and restart.

```bash
simple-proxy hash-password     # asks for a password, prints its hash for the file
```

**Signing up** is open by default: anyone who can reach the console can
create an account with a password, which has no role until an administrator
gives it one. Accounts of the OIDC provider are created when they first sign
in. Administrators close signing up with the switch on the Users page; then
only existing accounts and super administrators can sign in.

**OpenID Connect:** register simple-proxy at the provider (Keycloak,
Authentik, Google, Microsoft Entra ID, ...) as a client using the
authorization code flow, with `redirect_url` as its redirect URI: the
console's address followed by `api/auth/oidc/callback`. The sign-in page then
offers "Sign in with" the provider's `name` (default: the issuer's host).
simple-proxy uses PKCE and checks the ID token's signature, issuer, audience,
expiry and nonce. Accounts are identified by the provider's subject; their
display name and email are updated at each sign-in, and their username is
taken from `preferred_username` or `email` once. Set `password_login: false`
to sign in with the provider only.

Sessions last 7 days without use, and 30 days at most; changing a password
ends the other sessions, and removing or changing the OIDC provider ends
those signed in with it. Failed attempts to sign in are limited to 10 per 15
minutes for each client address and username, and 100 for each address;
attempts to sign up to 20 per hour for each address. Behind a reverse proxy,
all clients share its address, so list it in `trusted_proxies` for its
`X-Forwarded-For` header to tell theirs:

```yaml
trusted_proxies: [127.0.0.1, 10.0.0.0/8]
```

Requests changing anything must come from the console itself, so other
websites cannot make them in your name. Over HTTPS, the cookies are named
`__Host-...`, so that other hosts of the domain cannot set them.

Site operators cannot change the raw configuration or the extra directives
of their sites, which platform operators and administrators set for them:
nginx runs those with its privileges, so they could listen on other
addresses, use any certificate or write files, beyond their sites. Platform
operators can, so give that role to people trusted with nginx's
configuration.

### Versions of sites

Every change of a site that nginx accepted is kept as a version in the
database: adding it, changing it (also turning it on or off, or renaming the
certificate it uses), and deleting it, with its configuration, who made the
change and when. Importing sites deletes all of them and adds the imported
ones. Sites stored before versions were kept start with an initial version.

Super administrators see on the History page how many versions there are,
and how much space they take, by the day they were made, and clear those made
before a day. The latest version of each site is never cleared, as it is the
site's current configuration. Without super administrators in the
configuration file, all administrators can.

### AI assistants (MCP)

AI assistants such as Claude Code manage the sites through the Model Context
Protocol: the console serves MCP at `/mcp` (Streamable HTTP), next to its
API. An assistant acts as a user, with an API token that the user creates in
the account menu ("API tokens"), which also shows how to set up clients:

```bash
claude mcp add --transport http simple-proxy http://127.0.0.1:8081/mcp \
  --header "Authorization: Bearer sp_..."
```

The console's MCP page explains it step by step: the setup of Claude Code,
Cursor, VS Code, Claude Desktop (through `mcp-remote`) and other clients,
examples of what to ask, and the tools that assistants acting as you get.

The assistant may do exactly what the token's user may do in the console:
its tools make the same API requests, which are checked the same way, and
it only gets the tools that the user's role allows.

| Tools | For |
| ----- | --- |
| `whoami`, `list_sites`, `get_site`, `preview_site`, `update_site`, `set_site_enabled`, `get_status`, `list_certificates`, `get_certificate`, `list_authorities` | Everyone with a role; site operators only see and change their sites, and not their raw configuration or extra directives. |
| `create_site`, `delete_site`, `get_nginx_config`, `apply_sites` | Platform operators and administrators. |
| `issue_certificate`, `sign_certificate`, `upload_certificate`, `rename_certificate`, `delete_certificate` | Administrators. |

`preview_site` checks a change without saving it, and shows the nginx
configuration of the site with a diff, and `update_site` saves it based on
the site's generation, so that the assistant does not overwrite changes made
meanwhile. Changes are applied to nginx right away, and kept as versions by
the user, like those made in the console.

Tokens work for the HTTP API as well (`curl -H "Authorization: Bearer
sp_..."`). They expire after the time chosen (30 days to never), stop
working once revoked or their user is disabled, and are stored as hashes:
their secret is shown once.

### How changes are applied

On every change, and once on startup, the configuration of all enabled sites
is written to `-nginx-conf`, tested with `-nginx-test`, and loaded with
`-nginx-reload`. If the test or the reload fails, the previous file is
restored, so the file always holds a configuration nginx accepted. The header
shows whether nginx runs the stored sites; its dialog shows nginx's version
and build (OpenSSL, configure arguments), the commands, the last error and
the generated configuration, and can apply the sites again, e.g. after nginx
restarted. The version is found out with `-nginx-version` on startup and
whenever the sites are applied again, so upgrades show up then.

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

Certificates and CAs added here also get an ID, a UUID that stays the same
when they are renamed or replaced; the lists show its start, and copy all of
it on click. Their details (ID, serial number, SHA-256 fingerprint, validity,
chain, the sites using them, and the files nginx reads) open from the lists.
Renaming a certificate changes the sites using it, by `cert_name`, by
default, or with `{{cert "..."}}` and `{{key "..."}}` in a raw
configuration, to refer to it by its new name, so they keep serving it;
renaming a CA does the same for the certificates it signed, so it can still
renew them.
acme.sh's certificates show their details too, read from acme.sh's files, but
keep the names acme.sh gives them.

### Docker image

The [Dockerfile](Dockerfile) builds an image running simple-proxy and nginx
together, with acme.sh: nginx serves ports 80 and 443, and the console port
81. Sites, uploaded and signed certificates, CAs and acme.sh's account and
certificates are kept in the volume `/data`.

```bash
docker build --build-arg COMMIT=$(git rev-parse HEAD) -t simple-proxy .
docker run -d --name simple-proxy --restart unless-stopped \
  -p 80:80 -p 443:443 -p 127.0.0.1:81:81 \
  -v simple-proxy:/data \
  simple-proxy
```

Put the [configuration file](#users-and-roles) at `simple-proxy.yaml` in the
volume, e.g. `docker cp simple-proxy.yaml simple-proxy:/data/`; without one,
the console logs the password of a new `admin` account on first start. Publish
port 81 on 127.0.0.1 only, as above, and reach it through an SSH tunnel or a
VPN, or a reverse proxy with HTTPS.

- **Upstreams** are reached from inside the container, where `127.0.0.1` is
  the container itself. For services on the host, add
  `--add-host host.docker.internal:host-gateway` and use that name as the
  upstream host, or run with `--network host`. With host networking the
  console listens on port 81 of every interface, so append
  `-addr 127.0.0.1:81`.
- **Flags** given after the image name are passed to simple-proxy and
  override the image's defaults, e.g. `-addr`; any other command runs instead
  of the services, e.g. `bash`, or `simple-proxy hash-password`.
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
- Build arguments: `NGINX_IMAGE` (default `nginx:stable`),
  `ACME_SH_VERSION`, and `COMMIT`, the commit the console's footer shows,
  since the build context has no `.git`; without it, the footer shows none.
  Binaries built with `go build` or `build.sh` in a git checkout know their
  commit by themselves.

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

The console imports and exports sites as a `config.yaml` in this format. See
[config.yaml.example](config.yaml.example); JSON is accepted too.

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

Requests need a signed-in user: the browser's session cookie, or for
scripts, an [API token](#ai-assistants-mcp) (`Authorization: Bearer sp_...`),
or a username and password with HTTP Basic auth (`curl -u`). Otherwise
they fail with `401`, and with `403` if the user's role does not allow them
(see [Users and roles](#users-and-roles)). Site operators only get, change and
preview their own sites.

| Request                   | Response |
| ------------------------- | -------- |
| `GET /api/auth/config`    | How to sign in: `{"password": true, "registration": true, "oidc": {"name": "..."}}`; needs no sign-in. |
| `POST /api/auth/login`    | Sign in: `{"username": "...", "password": "..."}` gives `{"user": {...}, "permissions": {...}}` and the session cookie. |
| `POST /api/auth/register` | Sign up, if open: `{"username": "...", "password": "...", "display_name": "..."}`. |
| `POST /api/auth/logout`   | Sign out. |
| `GET /api/auth/me`        | The signed-in user, and what they may do: `{"user": {...}, "permissions": {"sites": true, "all_sites": false, "certificates": false, "users": false, "history": false}}`. |
| `GET /api/auth/tokens`    | Your API tokens: `{"tokens": [{"id": "...", "name": "...", "prefix": "sp_ABCD", "created_at": "...", "last_used_at": "...", "expires_at": "..."}]}`. |
| `POST /api/auth/tokens`   | Create one: `{"name": "...", "expires_in_days": 90}` (0 for never) gives `{"token": {...}, "secret": "sp_..."}`, the only time the secret is shown. |
| `DELETE /api/auth/tokens/{id}` | Revoke one of yours. |
| `POST /mcp`               | [MCP](#ai-assistants-mcp), for AI assistants. |
| `POST /api/auth/password` | Change your password: `{"current": "...", "password": "..."}`. |
| `GET /api/auth/oidc/login` | Sign in with the OIDC provider, which sends the browser back to `/api/auth/oidc/callback`. |
| `GET /api/users`          | Administrators: the users, `{"users": [{"id": "...", "username": "...", "role": "site_operator", "sites": ["..."], "method": "password", "managed": false, ...}]}`. |
| `POST /api/users`         | Add one, signing in with a password: `{"username": "...", "password": "...", "role": "...", "sites": [...]}`. |
| `PATCH /api/users/{id}`   | Change one: any of `display_name`, `email`, `role`, `sites`, `disabled` and `password`. |
| `DELETE /api/users/{id}`  | Delete one. |
| `GET /api/settings`, `PUT` | Administrators: `{"registration": true}`, whether new accounts may sign up. |
| `GET /api/versions/stats?offset=480` | Super administrators: the [versions of sites](#versions-of-sites), `{"versions": 12, "purgeable": 9, "bytes": 3456, "sites": 4, "oldest": "...", "newest": "...", "days": [{"date": "2026-11-27", "versions": 5, "purgeable": 4, "bytes": 1234, "created": 1, "updated": 3, "deleted": 1, "initial": 0}]}`, by day at the UTC offset in minutes. |
| `POST /api/versions/purge` | Super administrators: delete the versions made before `{"before": "2026-11-01T00:00:00+08:00"}`, but each site's latest one: `{"deleted": 9}`. |
| `GET /api/sites`          | `{"sites": [{"id": "...", "config": {...}, "generation": 1}]}` |
| `POST /api/sites`         | Add a site: `{"config": {...}}`. |
| `PUT /api/sites/{id}`     | Change a site: `{"config": {...}, "generation": 1}`. |
| `DELETE /api/sites/{id}`  | Remove a site: `?generation=1`. |
| `POST /api/sites/raw` | The configuration the builder generates for a site, as raw configuration: `{"config": {...}}` gives `{"raw": "..."}`. |
| `POST /api/sites/preview` | What saving a site would change, without saving it: `{"id": "...", "config": {...}}` (no `id` for a new site) gives `{"current": "...", "proposed": "...", "generation": 1}`. |
| `PUT /api/sites`          | Replace all sites: `{"configs": [{...}]}`. |
| `GET /api/status`         | How sites are applied to nginx, the last error, and nginx's version: `"nginx": {"version": "nginx/1.27.3", "build": "..."}`, or `"nginx_error"` if it could not be found out. |
| `POST /api/apply`         | Apply the stored sites again. |
| `GET /api/nginx.conf`     | The configuration generated from the sites. |
| `GET /api/certificates`   | All certificates: `{"certificates": [{"id": "...", "name": "...", "source": "uploaded", ...}], "acme_error": "..."}`, where `source` is `acme`, `uploaded` or `signed`; acme.sh's have no `id`. |
| `GET /api/certificates/{name}` | One in full: its list entry with `subject`, `not_before`, `serial`, `sha256`, `chain`, `cert_file`, `key_file` and the `sites` using it (`[{"id": "...", "name": "...", "enabled": true}]`). |
| `PATCH /api/certificates/{name}` | Rename an uploaded or signed one: `{"name": "..."}`. Sites using it follow. |
| `POST /api/certificates`  | Request one from acme.sh: `{"domains": ["example.com"], "dns": "dns_cf"}`; without `dns`, over HTTP. |
| `POST /api/certificates/upload` | Upload one: `{"name": "...", "cert": "PEM", "key": "PEM", "replace": false}`; without `name`, it is named after its first domain. |
| `POST /api/certificates/sign` | Have a CA sign one: `{"authority": "...", "domains": ["nas.lan"], "days": 365, "key_type": "ec", "name": "...", "replace": false}`; `key_type` is `ec` or `rsa`. |
| `POST /api/certificates/upload/preview`, `/sign/preview` | What uploading or signing would store, without storing it: `{"name": "...", "subject": "...", "issuer": "...", "domains": [...], "not_after": "...", "chain": [...], "has_key": true, "problems": [...]}`, as far as it can be read, with the problems that would make it fail. |
| `DELETE /api/certificates/{name}` | Delete an uploaded or signed certificate that no enabled site uses. |
| `GET /api/authorities`    | The CAs: `{"authorities": [{"id": "...", "name": "...", "subject": "...", "expires": "...", "signed": 1}]}`. |
| `POST /api/authorities`   | Upload a CA: `{"name": "...", "cert": "PEM", "key": "PEM"}`; without `name`, it is named after its common name. `POST /api/authorities/preview` previews it. |
| `GET /api/authorities/{name}` | One CA in full, with `serial`, `sha256`, `chain` and the names of the `certificates` it signed. |
| `PATCH /api/authorities/{name}` | Rename a CA: `{"name": "..."}`. The certificates it signed follow. |
| `DELETE /api/authorities/{name}` | Delete a CA; the certificates it signed stay. |
| `GET /api/authorities/{name}/cert.pem` | The CA's certificate, to install on clients. |
| `POST /api/generate`      | The configuration generated from a YAML or JSON configuration, without storing anything. |
| `POST /api/validate`      | `204` if the configuration in the body is valid. |

Changes and deletions carry the generation of the site they are based on,
and fail with `409` if the site has changed since; without a generation they
always apply. Changes respond `400` for invalid sites, `422` for missing
certificates, and `502` with nginx's output if nginx rejects the
configuration. Private keys are never returned. Errors are JSON, with a
`code` for refusals such as `unauthenticated`, `forbidden` or `last_admin`:

```json
{"message": "...", "code": "...", "output": "nginx: [emerg] ...",
 "fields": [{"field": "servers[0].default.port", "message": "70000 is not a valid port", "code": "invalid_port", "value": "70000"}]}
```

The `code` of fields (see `config.ProblemCode`, `certs.ProblemCode` and
`auth.ProblemCode`, plus `cert_not_found`) lets clients
describe a problem in their own words, as the UI does in Chinese. `generate`
and `validate` answer in plain text unless asked for JSON:

```bash
curl -u admin --data-binary @config.yaml http://127.0.0.1:8080/api/generate > proxy.conf
```

## Development

```
main.go            flags and wiring
internal/config    configuration format, defaults and validation
internal/nginx     configuration generator (nginx.conf.tmpl)
internal/apply     writes, tests and reloads nginx's configuration
internal/acme      certificates through acme.sh
internal/certs     uploaded certificates and CAs: parsing, signing, files
internal/store     SQLite storage of sites, certificates, CAs, users and sessions (modernc.org/sqlite, no cgo)
internal/auth      users: passwords, OIDC, sessions, roles, and the configuration file
internal/manager   site and certificate changes: check, apply, then store
internal/web       HTTP API and MCP (mcp.go), embeds the UI from internal/web/dist
web/               UI: React, Tailwind CSS and shadcn/ui
web/src/lib/i18n/  the UI's messages in English (en.ts) and Chinese (zh.ts)
Dockerfile         the image with nginx and acme.sh; docker/entrypoint.sh runs them
```

The UI is built into `internal/web/dist`, which the server embeds and git
ignores: build it before building or testing the server with Go, which fails
without it, and again after changing `web/`:

```bash
npm --prefix web ci
go generate ./internal/web    # runs npm run build
```

`npm --prefix web run dev` serves the UI with hot reload and proxies `/api` to
a server running on the default address.

Run `go test ./...` and `npm --prefix web test` for the tests, and
`go test ./internal/nginx -update` to rewrite the golden files in
`internal/nginx/testdata` after intended output changes.
