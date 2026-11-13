// Client for the HTTP API served by the Go server (internal/web).

export type GenerateResult =
  | { ok: true; config: string }
  // status is 0 when the server could not be reached.
  | { ok: false; status: number; message: string }

export async function generateConfig(source: string): Promise<GenerateResult> {
  try {
    const res = await fetch("api/generate", {
      method: "POST",
      headers: { "Content-Type": "application/yaml" },
      body: source,
    })
    const text = await res.text()
    return res.ok
      ? { ok: true, config: text }
      : { ok: false, status: res.status, message: text.trim() }
  } catch (err) {
    return { ok: false, status: 0, message: String(err) }
  }
}

export async function fetchExample(): Promise<string> {
  const res = await fetch("api/example")
  if (!res.ok) {
    throw new Error(`loading the example failed: HTTP ${res.status}`)
  }
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
