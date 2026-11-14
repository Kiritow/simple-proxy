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
  problems?: FieldProblem[]
}

export type Status = {
  applies: boolean
  config_path?: string
  test_command?: string
  reload_command?: string
  applied_at?: string
  error?: string
  output?: string
  certificates: boolean
  acme_webroot?: string
}

export type Certificate = {
  name: string
  domains: string[]
  key_length: string
  ca: string
  created: string
  renew: string
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
  updateSite: (id: string, config: ServerJSON) =>
    request<StoredSite>(`api/sites/${encodeURIComponent(id)}`, {
      method: "PUT",
      json: { config },
    }),
  deleteSite: (id: string) =>
    request<void>(`api/sites/${encodeURIComponent(id)}`, { method: "DELETE" }),
  replaceSites: (configs: ServerJSON[]) =>
    request<{ sites: StoredSite[] }>("api/sites", {
      method: "PUT",
      json: { configs },
    }).then((r) => r.sites),

  status: () => request<Status>("api/status"),
  apply: () => request<Status>("api/apply", { method: "POST" }),
  nginxConf: () => request<string>("api/nginx.conf"),

  certificates: () =>
    request<{ certificates: Certificate[] }>("api/certificates").then(
      (r) => r.certificates
    ),
  issueCertificate: (domains: string[], dns: string) =>
    request<{ output: string }>("api/certificates", {
      method: "POST",
      json: { domains, dns },
    }).then((r) => r.output),

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
