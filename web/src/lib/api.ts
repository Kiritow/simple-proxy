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
  if (!res.ok) throw new RequestError(await readError(res))
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
