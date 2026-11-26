// Client for the HTTP API served by the Go server (internal/web).

import type { ConfigJSON, ServerJSON } from "@/lib/model"

export type FieldProblem = {
  field: string
  message: string // in English
  code?: string // identifies the problem, see describeProblem
  value?: string
}

export type ApiError = {
  status: number // 0 if the server could not be reached
  message: string
  // code identifies the problem with the request as a whole, such as
  // "invalid_credentials", for clients to describe it in their own words.
  code?: string
  fields: FieldProblem[]
  output?: string // of nginx or acme.sh
}

// RequestError is thrown for failed requests.
export class RequestError extends Error {
  readonly error: ApiError
  constructor(error: ApiError) {
    super(error.message)
    this.error = error
  }
}

// StoredSite is a site as stored on the server. Problems are set for invalid
// sites stored by earlier versions, which are not applied.
export type StoredSite = {
  id: string
  config: ServerJSON
  generation: number // counts the changes of config
  created_at?: string // when the site was added
  updated_at?: string // when config last changed
  problems?: FieldProblem[]
}

// Preview is what saving a site would change in the nginx configuration.
export type Preview = {
  current: string
  proposed: string
  // site is the configuration of the site alone, as it would be enabled;
  // empty if it cannot be generated, as only disabled sites may.
  site: string
  generation: number // of the stored site; 0 for a new site
}

export type Status = {
  applies: boolean
  config_path?: string
  test_command?: string
  reload_command?: string
  // nginx as version_command describes it once known, or why it is not.
  version_command?: string
  nginx?: NginxInfo
  nginx_error?: string
  applied_at?: string
  error?: string
  output?: string
  acme: boolean // acme.sh can list and issue certificates
  acme_webroot?: string
  cert_dir: string // where uploaded and signed certificates are written
  commit?: string // the full hash of the commit the server was built from
  modified?: boolean // built with uncommitted changes
}

// NginxInfo describes nginx, as nginx -V does.
export type NginxInfo = {
  version: string // such as "nginx/1.27.3"
  build?: string // e.g. its OpenSSL and configure arguments
}

export type CertificateSource = "acme" | "uploaded" | "signed"

// KeyType is the type of the key of a signed certificate.
export type KeyType = "ec" | "rsa"

export type Certificate = {
  // id identifies an uploaded or signed certificate for good, while its name
  // may change. acme.sh's certificates have none.
  id?: string
  name: string
  source: CertificateSource
  domains: string[]
  key: string // such as "EC P-256", or "ec-256" for acme.sh
  issuer: string
  created?: string
  expires?: string // not known for acme.sh
  renews?: string // acme.sh's next renewal
  // How a signed certificate was signed, to sign it again.
  ca?: string
  key_type?: KeyType
  days?: number
}

export type CertificateList = {
  certificates: Certificate[]
  acme_error?: string // acme.sh failed to list its certificates
}

// Authority is a certificate authority (CA) that signs certificates.
export type Authority = {
  id: string
  name: string
  subject: string
  issuer: string
  key: string
  created: string
  expires: string
  signed: number // certificates kept that it signed
}

// SiteRef refers to a site.
export type SiteRef = {
  id: string
  name: string // its first server name, if any
  enabled: boolean
  raw?: boolean // configured by a raw configuration
}

// CertificateDetails describes a certificate in full.
export type CertificateDetails = Certificate & {
  subject?: string
  not_before?: string
  serial?: string // colon-separated hex bytes
  sha256?: string // fingerprint, likewise
  chain: ChainEntry[] // the issuers following the certificate
  updated?: string
  cert_file?: string // the files nginx reads
  key_file?: string
  sites: SiteRef[] // that serve it, enabled or not
  problem?: string // acme.sh's files could not be read
}

// AuthorityDetails describes a CA in full.
export type AuthorityDetails = Authority & {
  not_before: string
  serial: string
  sha256: string
  chain: ChainEntry[]
  certificates: string[] // the names of those it signed
}

// Upload is a certificate or CA to upload: its PEM chain and private key.
// Without a name, it is named after the certificate.
export type Upload = {
  name: string
  cert: string
  key: string
  replace: boolean
}

// CertificatePreview describes what an upload or a signing request would
// store, as far as the server can tell, and the problems that keep it from
// being stored.
export type CertificatePreview = {
  name: string // as it would be stored
  subject: string
  issuer: string
  domains: string[]
  key: string
  not_before?: string
  not_after?: string
  chain: ChainEntry[] // the issuers following the certificate
  has_key: boolean // its private key is known
  problems: FieldProblem[]
}

export type ChainEntry = {
  subject: string
  not_after: string
  root: boolean // self-signed: sites do not serve it
}

export type SignRequest = {
  name: string
  authority: string
  domains: string[]
  days: number
  key_type: KeyType
  replace: boolean
}

// Role is what an account may do: "" for nothing (new accounts), sites
// given to a site operator, all sites for a platform operator, and
// everything for an administrator.
export type Role = "" | "site_operator" | "platform_operator" | "admin"

export const roles: readonly Role[] = [
  "",
  "site_operator",
  "platform_operator",
  "admin",
]

// SignInMethod is how an account signs in.
export type SignInMethod = "password" | "oidc"

export type User = {
  id: string
  username: string
  display_name: string // may be empty
  email: string // may be empty
  role: Role
  sites: string[] // IDs of the sites of a site operator
  disabled: boolean // cannot sign in
  method: SignInMethod
  // managed is set for the super administrators of the config file, whose
  // role cannot be changed here, and who cannot be disabled or deleted.
  managed: boolean
  password_managed: boolean // the password is set in the config file
  created_at: string
  last_login_at?: string // absent if never
}

// Permissions are what the signed-in account may do, following its role.
export type Permissions = {
  sites: boolean // see and edit sites: its own, or all of them
  all_sites: boolean // add, delete and import sites, apply, nginx config
  certificates: boolean // change certificates and CAs
  users: boolean // manage users and settings
  // history allows seeing the statistics of the versions of the sites, and
  // clearing old ones: super administrators only.
  history: boolean
}

export type Me = { user: User; permissions: Permissions }

// VersionStats counts the versions kept of the sites, one for every
// successful change, by the day they were created.
export type VersionStats = {
  versions: number
  purgeable: number // what clearing may delete: all but each site's current one
  bytes: number // the size of their configurations
  sites: number // with versions, deleted ones included
  oldest?: string // when the oldest version was created; absent without any
  newest?: string
  days: VersionDay[] // with versions, oldest first
}

// VersionDay counts the versions created on a day, in the time zone the
// statistics were asked for.
export type VersionDay = {
  date: string // such as "2026-11-27"
  versions: number
  purgeable: number
  bytes: number
  created: number // by kind of change
  updated: number
  deleted: number
  initial: number // the configurations sites had when versions began
}

// AuthConfig tells how to sign in.
export type AuthConfig = {
  password: boolean // with a username and a password
  registration: boolean // new accounts may sign up
  oidc: { name: string } | null // with the OpenID Connect provider named
}

export type Settings = { registration: boolean }

// ApiToken lets scripts and AI assistants (over MCP) act as the account
// that created it, with exactly its permissions. Requests send its secret
// as "Authorization: Bearer <secret>".
export type ApiToken = {
  id: string
  name: string // what it is for
  prefix: string // the start of the secret, such as "sp_7KQ2M"
  created_at: string
  last_used_at?: string // absent if never used
  expires_at?: string // absent if it never expires
}

// NewApiToken is a token just created, with its secret, which is not shown
// again.
export type NewApiToken = { token: ApiToken; secret: string }

// consoleUrl returns the address of the console, which may live under a
// path prefix, without the fragment the UI routes with.
export function consoleUrl(): string {
  const url = new URL(".", location.href)
  url.hash = ""
  url.search = ""
  return url.href
}

// mcpUrl returns the address of the MCP endpoint of the console.
export function mcpUrl(): string {
  return new URL("mcp", consoleUrl()).href
}

// NewUser is a password account to add.
export type NewUser = {
  username: string
  display_name: string
  email: string
  password: string
  role: Role
  sites: string[]
}

// UserChange changes the fields given of an account.
export type UserChange = Partial<
  Pick<User, "display_name" | "email" | "role" | "sites" | "disabled">
> & { password?: string }

// Requests failing with 401 mean that the session ended, except for these,
// which find out whether there is one, or start one.
const signingIn = ["api/auth/me", "api/auth/login", "api/auth/register"]

const unauthorizedListeners = new Set<() => void>()

// onUnauthorized calls listener whenever a request fails because the
// session ended, e.g. after signing out elsewhere. It returns a function
// that stops calling it.
export function onUnauthorized(listener: () => void): () => void {
  unauthorizedListeners.add(listener)
  return () => {
    unauthorizedListeners.delete(listener)
  }
}

async function request<T>(
  path: string,
  init: RequestInit & { json?: unknown } = {}
): Promise<T> {
  const { json, ...rest } = init
  let res: Response
  try {
    res = await fetch(path, {
      ...rest,
      headers: {
        Accept: "application/json",
        ...(json !== undefined && { "Content-Type": "application/json" }),
      },
      body: json !== undefined ? JSON.stringify(json) : rest.body,
    })
  } catch (err) {
    throw new RequestError({ status: 0, message: String(err), fields: [] })
  }
  if (!res.ok) {
    const error = await readError(res)
    if (res.status === 401 && !signingIn.includes(path)) {
      for (const listener of unauthorizedListeners) listener()
    }
    throw new RequestError(error)
  }
  if (res.status === 204) return undefined as T
  const type = res.headers.get("Content-Type") ?? ""
  return (type.includes("json") ? res.json() : res.text()) as Promise<T>
}

async function readError(res: Response): Promise<ApiError> {
  const text = await res.text()
  try {
    const body = JSON.parse(text) as Partial<ApiError>
    return {
      status: res.status,
      message: body.message ?? text,
      code: body.code,
      fields: body.fields ?? [],
      output: body.output,
    }
  } catch {
    return {
      status: res.status,
      message: text.trim() || res.statusText,
      fields: [],
    }
  }
}

export const api = {
  sites: () =>
    request<{ sites: StoredSite[] }>("api/sites").then((r) => r.sites),
  createSite: (config: ServerJSON) =>
    request<StoredSite>("api/sites", { method: "POST", json: { config } }),
  // updateSite and deleteSite fail with 409 if the site is no longer at
  // generation, i.e. someone else changed it.
  updateSite: (id: string, config: ServerJSON, generation: number) =>
    request<StoredSite>(`api/sites/${encodeURIComponent(id)}`, {
      method: "PUT",
      json: { config, generation },
    }),
  deleteSite: (id: string, generation: number) =>
    request<void>(
      `api/sites/${encodeURIComponent(id)}?generation=${generation}`,
      { method: "DELETE" }
    ),
  previewSite: (id: string | null, config: ServerJSON, signal?: AbortSignal) =>
    request<Preview>("api/sites/preview", {
      method: "POST",
      json: { id: id ?? "", config },
      signal,
    }),
  // rawSite returns the configuration generated for a site, as a raw
  // configuration to edit by hand.
  rawSite: (config: ServerJSON) =>
    request<{ raw: string }>("api/sites/raw", {
      method: "POST",
      json: { config },
    }).then((r) => r.raw),
  replaceSites: (configs: ServerJSON[]) =>
    request<{ sites: StoredSite[] }>("api/sites", {
      method: "PUT",
      json: { configs },
    }).then((r) => r.sites),

  status: () => request<Status>("api/status"),
  apply: () => request<Status>("api/apply", { method: "POST" }),
  nginxConf: () => request<string>("api/nginx.conf"),

  certificates: () => request<CertificateList>("api/certificates"),
  issueCertificate: (domains: string[], dns: string) =>
    request<{ output: string }>("api/certificates", {
      method: "POST",
      json: { domains, dns },
    }).then((r) => r.output),
  uploadCertificate: (upload: Upload) =>
    request<Certificate>("api/certificates/upload", {
      method: "POST",
      json: upload,
    }),
  signCertificate: (req: SignRequest) =>
    request<Certificate>("api/certificates/sign", {
      method: "POST",
      json: req,
    }),
  previewUpload: (upload: Upload, signal?: AbortSignal) =>
    request<CertificatePreview>("api/certificates/upload/preview", {
      method: "POST",
      json: upload,
      signal,
    }),
  previewSign: (req: SignRequest, signal?: AbortSignal) =>
    request<CertificatePreview>("api/certificates/sign/preview", {
      method: "POST",
      json: req,
      signal,
    }),
  certificate: (name: string) =>
    request<CertificateDetails>(`api/certificates/${encodeURIComponent(name)}`),
  // renameCertificate also changes the sites using the certificate to refer
  // to it by its new name.
  renameCertificate: (name: string, newName: string) =>
    request<Certificate>(`api/certificates/${encodeURIComponent(name)}`, {
      method: "PATCH",
      json: { name: newName },
    }),
  deleteCertificate: (name: string) =>
    request<void>(`api/certificates/${encodeURIComponent(name)}`, {
      method: "DELETE",
    }),

  authorities: () =>
    request<{ authorities: Authority[] }>("api/authorities").then(
      (r) => r.authorities
    ),
  addAuthority: (upload: Omit<Upload, "replace">) =>
    request<Authority>("api/authorities", { method: "POST", json: upload }),
  previewAuthority: (upload: Upload, signal?: AbortSignal) =>
    request<CertificatePreview>("api/authorities/preview", {
      method: "POST",
      json: { ...upload, replace: false },
      signal,
    }),
  authority: (name: string) =>
    request<AuthorityDetails>(`api/authorities/${encodeURIComponent(name)}`),
  // renameAuthority also renames the CA of the certificates it signed.
  renameAuthority: (name: string, newName: string) =>
    request<Authority>(`api/authorities/${encodeURIComponent(name)}`, {
      method: "PATCH",
      json: { name: newName },
    }),
  deleteAuthority: (name: string) =>
    request<void>(`api/authorities/${encodeURIComponent(name)}`, {
      method: "DELETE",
    }),
  authorityCertUrl: (name: string) =>
    `api/authorities/${encodeURIComponent(name)}/cert.pem`,

  authConfig: () => request<AuthConfig>("api/auth/config"),
  // me fails with 401 if not signed in.
  me: () => request<Me>("api/auth/me"),
  login: (username: string, password: string) =>
    request<Me>("api/auth/login", {
      method: "POST",
      json: { username, password },
    }),
  // register signs up a new account, without a role, and signs it in.
  register: (username: string, password: string, displayName: string) =>
    request<Me>("api/auth/register", {
      method: "POST",
      json: { username, password, display_name: displayName },
    }),
  logout: () => request<void>("api/auth/logout", { method: "POST" }),
  // changePassword changes the password of the signed-in account, and signs
  // out its other sessions.
  changePassword: (current: string, password: string) =>
    request<void>("api/auth/password", {
      method: "POST",
      json: { current, password },
    }),
  // oidcLoginUrl starts signing in with the OpenID Connect provider, which
  // returns to the console afterwards.
  oidcLoginUrl: "api/auth/oidc/login",

  users: () => request<{ users: User[] }>("api/users").then((r) => r.users),
  createUser: (user: NewUser) =>
    request<User>("api/users", { method: "POST", json: user }),
  updateUser: (id: string, change: UserChange) =>
    request<User>(`api/users/${encodeURIComponent(id)}`, {
      method: "PATCH",
      json: change,
    }),
  deleteUser: (id: string) =>
    request<void>(`api/users/${encodeURIComponent(id)}`, { method: "DELETE" }),
  settings: () => request<Settings>("api/settings"),
  updateSettings: (settings: Settings) =>
    request<Settings>("api/settings", { method: "PUT", json: settings }),

  // versionStats counts the versions by day, for days offset minutes east
  // of UTC, such as -new Date().getTimezoneOffset() for the browser's.
  versionStats: (offset: number) =>
    request<VersionStats>(`api/versions/stats?offset=${offset}`),
  // purgeVersions deletes the versions created before the given time, an
  // RFC 3339 string, except the current one of each site, and returns how
  // many it deleted.
  purgeVersions: (before: string) =>
    request<{ deleted: number }>("api/versions/purge", {
      method: "POST",
      json: { before },
    }).then((r) => r.deleted),

  // tokens lists the API tokens of the signed-in account, newest first.
  tokens: () =>
    request<{ tokens: ApiToken[] }>("api/auth/tokens").then((r) => r.tokens),
  // createToken creates an API token expiring in the given number of days,
  // or never for 0. Its secret is only ever returned here.
  createToken: (name: string, expiresInDays: number) =>
    request<NewApiToken>("api/auth/tokens", {
      method: "POST",
      json: { name, expires_in_days: expiresInDays },
    }),
  deleteToken: (id: string) =>
    request<void>(`api/auth/tokens/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),

  validate: (config: ConfigJSON, signal?: AbortSignal) =>
    request<void>("api/validate", { method: "POST", json: config, signal }),
  example: () => request<string>("api/example"),
}

// errorOf returns the ApiError of a failed request.
export function errorOf(err: unknown): ApiError {
  return err instanceof RequestError
    ? err.error
    : { status: 0, message: String(err), fields: [] }
}

export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard) {
    return navigator.clipboard.writeText(text)
  }
  // navigator.clipboard only exists in secure contexts, i.e. not when the UI
  // is opened over plain HTTP on an address other than localhost.
  const el = Object.assign(document.createElement("textarea"), { value: text })
  document.body.append(el)
  el.select()
  const ok = document.execCommand("copy")
  el.remove()
  if (!ok) throw new Error("copying to the clipboard failed")
}

export function downloadText(filename: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }))
  Object.assign(document.createElement("a"), {
    href: url,
    download: filename,
  }).click()
  setTimeout(() => URL.revokeObjectURL(url))
}
