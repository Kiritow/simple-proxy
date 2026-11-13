// Client for the HTTP API served by the Go server (internal/web).

import type { ConfigJSON } from "@/lib/model"

export type FieldProblem = { field: string; message: string }

export type ApiError = {
  status: number // 0 if the server could not be reached
  message: string
  fields: FieldProblem[]
}

export type GenerateResult =
  { ok: true; config: string } | { ok: false; error: ApiError }

const json = {
  Accept: "application/json",
  "Content-Type": "application/json",
}

async function readError(res: Response): Promise<ApiError> {
  const text = await res.text()
  try {
    const body = JSON.parse(text) as {
      message?: string
      fields?: FieldProblem[]
    }
    return {
      status: res.status,
      message: body.message ?? text,
      fields: body.fields ?? [],
    }
  } catch {
    return {
      status: res.status,
      message: text.trim() || res.statusText,
      fields: [],
    }
  }
}

export async function generateConfig(
  config: ConfigJSON
): Promise<GenerateResult> {
  try {
    const res = await fetch("api/generate", {
      method: "POST",
      headers: json,
      body: JSON.stringify(config),
    })
    if (!res.ok) return { ok: false, error: await readError(res) }
    const body = (await res.json()) as { config: string }
    return { ok: true, config: body.config }
  } catch (err) {
    return {
      ok: false,
      error: {
        status: 0,
        message: `Cannot reach the server: ${err}`,
        fields: [],
      },
    }
  }
}

// validateConfig returns the problems with config, or null if it is valid.
// It throws if the server cannot be reached or signal is aborted.
export async function validateConfig(
  config: ConfigJSON,
  signal: AbortSignal
): Promise<ApiError | null> {
  const res = await fetch("api/validate", {
    method: "POST",
    headers: json,
    body: JSON.stringify(config),
    signal,
  })
  return res.ok ? null : readError(res)
}

export async function fetchCertificates(): Promise<string[]> {
  const res = await fetch("api/certificates", {
    headers: { Accept: "application/json" },
  })
  if (!res.ok) throw new Error((await readError(res)).message)
  const body = (await res.json()) as { certificates: string[] }
  return body.certificates
}

export async function fetchExample(): Promise<string> {
  const res = await fetch("api/example")
  if (!res.ok) throw new Error(`loading the example failed: HTTP ${res.status}`)
  return res.text()
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
