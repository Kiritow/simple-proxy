// The builder's model of a configuration, and its conversion to and from the
// schema of internal/config (see config.yaml.example).

import { parse, stringify } from "yaml"

export type Directive = { id: string; text: string }

export type Route = {
  id: string
  enabled: boolean
  path: string
  host: string // empty: inherited
  port: string // empty: inherited
  websocket: boolean
  disableCache: boolean
  realtime: boolean
  options: Directive[]
}

export type Site = {
  id: string
  enabled: boolean
  serverName: string
  https: boolean
  certName: string // empty: named after the first domain
  listenPort: string // empty: 80 or 443
  gzip: boolean
  options: Directive[]
  root: Route // the default location; its upstream is inherited by routes
  routes: Route[]
  // raw is the raw nginx configuration of the site, written by hand in
  // place of the builder's fields, or null for sites built from them.
  raw: string | null
}

export const DEFAULT_HOST = "127.0.0.1"

let seq = 0
export const newId = () => Date.now().toString(36) + (seq++).toString(36)

export function newRoute(path = ""): Route {
  return {
    id: newId(),
    enabled: true,
    path,
    host: "",
    port: "",
    websocket: false,
    disableCache: false,
    realtime: false,
    options: [],
  }
}

export function newSite(): Site {
  return {
    id: newId(),
    enabled: true,
    serverName: "",
    https: false,
    certName: "",
    listenPort: "",
    gzip: false,
    options: [],
    root: newRoute("/"),
    routes: [],
    raw: null,
  }
}

// rawTemplate is where raw configurations written from scratch start.
export const rawTemplate = `server {
  listen 80;
  server_name example.com;

  location / {
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_pass http://127.0.0.1:3000;
  }
}
`

// newRawSite returns a new site with a raw configuration, from rawTemplate.
export function newRawSite(): Site {
  return { ...newSite(), raw: rawTemplate }
}

export const domainsOf = (s: Site) =>
  s.raw !== null
    ? rawServerNames(s.raw)
    : s.serverName.split(/\s+/).filter(Boolean)

// rawServerNames returns the names in the server_name directives of a raw
// configuration, roughly: the server parses it properly.
export function rawServerNames(raw: string): string[] {
  const text = raw.replace(/#[^\n]*/g, "")
  const names: string[] = []
  for (const m of text.matchAll(/(?:^|[\s;{}])server_name\s+([^;{}]*);/g)) {
    for (const word of m[1].split(/\s+/)) {
      const name = word.replace(/^(["'])(.*)\1$/, "$2")
      if (name && name !== "_" && !names.includes(name)) names.push(name)
    }
  }
  return names
}

// rawCertificates returns the names of the certificates a raw configuration
// refers to, as {{cert "name"}}.
export function rawCertificates(raw: string): string[] {
  const names: string[] = []
  for (const m of raw.matchAll(/\{\{\s*cert\s+("(?:[^"\\]|\\.)*")\s*\}\}/g)) {
    try {
      const name = JSON.parse(m[1]) as string
      if (!names.includes(name)) names.push(name)
    } catch {
      // Not a valid reference; the server reports it.
    }
  }
  return names
}

// certificateOf returns the name of the certificate served by s.
export const certificateOf = (s: Site) => s.certName || domainsOf(s)[0] || ""

// certificatesOf returns the names of the certificates a site serves: the
// one of an HTTPS site, or those its raw configuration refers to.
export function certificatesOf(s: Site): string[] {
  if (s.raw !== null) return rawCertificates(s.raw)
  return s.https ? [certificateOf(s)] : []
}

// upstreamOf describes where the main route of s goes, if it is on, such as
// "http://127.0.0.1:3000".
export function upstreamOf(s: Site): string | undefined {
  if (!s.root.enabled) return undefined
  return upstream(s.root.host, s.root.port)
}

// routeUpstreamOf describes where a route of s goes: its empty upstream
// fields are those of the main route.
export function routeUpstreamOf(s: Site, r: Route): string {
  return upstream(r.host || s.root.host, r.port || s.root.port)
}

function upstream(host: string, port: string): string {
  const h = host.trim() || DEFAULT_HOST
  return `http://${h.includes(":") ? `[${h}]` : h}:${port.trim() || "?"}`
}

// matchesSearch tells whether a site matches a search: whether each of its
// words, in any case, is part of what the sites list shows of the site, or
// of its raw configuration. Its domains, certificates, upstreams, and the
// paths of its routes are searched.
export function matchesSearch(s: Site, search: string): boolean {
  const words = search.toLowerCase().split(/\s+/).filter(Boolean)
  if (words.length === 0) return true
  const text = [
    ...domainsOf(s),
    ...certificatesOf(s),
    s.raw ?? "",
    ...(s.raw !== null
      ? []
      : [
          upstreamOf(s) ?? "",
          ...s.routes.flatMap((r) => [r.path, routeUpstreamOf(s, r)]),
        ]),
  ]
    .join("\n")
    .toLowerCase()
  return words.every((w) => text.includes(w))
}

// certCovers reports whether the certificate named cert is valid for domain.
export function certCovers(cert: string, domain: string): boolean {
  if (cert === domain) return true
  if (!cert.startsWith("*.")) return false
  const suffix = cert.slice(1) // ".example.com"
  const label = domain.slice(0, -suffix.length)
  return domain.endsWith(suffix) && label !== "" && !label.includes(".")
}

// uncovered returns the domains that none of certDomains, the names a
// certificate is valid for, covers.
export function uncovered(domains: string[], certDomains: string[]): string[] {
  return domains.filter((d) => !certDomains.some((c) => certCovers(c, d)))
}

// Keys identify the inputs of sites, routes and directives, so that problems
// reported by the server can be shown next to them. A field's key is the key
// of its owner followed by the field's name in the schema, e.g. ".port".

export const siteKey = (s: Site) => `s:${s.id}`
export const routeKey = (s: Site, r: Route) => `${siteKey(s)}/r:${r.id}`
export const directiveKey = (owner: string, d: Directive) =>
  `${owner}/d:${d.id}`

// The JSON form of the configuration schema.

type LocationJSON = {
  enable?: false
  path?: string
  ip?: string
  port?: number
  websocket?: true
  disable_cache?: true
  realtime?: true
  options?: string[]
}

export type ServerJSON = {
  server_name?: string
  enable?: false
  listen_port?: number
  https?: true
  cert_name?: string
  gzip?: true
  options?: string[]
  default?: LocationJSON
  locations?: LocationJSON[]
  raw?: string
}

export type ConfigJSON = { servers: ServerJSON[] }

// Serialized is a configuration together with the key of every site, route
// and directive in it, indexed by their paths in server-side errors, such as
// "servers[0].locations[1]".
export type Serialized = { config: ConfigJSON; owners: Map<string, string> }

export function serialize(sites: Site[]): Serialized {
  const owners = new Map<string, string>()

  const options = (ds: Directive[], owner: string, at: string) => {
    const out: string[] = []
    for (const d of ds) {
      const text = d.text.trim()
      if (!text) continue
      owners.set(`${at}.options[${out.length}]`, directiveKey(owner, d))
      out.push(text)
    }
    return out.length > 0 ? out : undefined
  }

  const location = (site: Site, r: Route, at: string): LocationJSON => {
    const owner = routeKey(site, r)
    owners.set(at, owner)
    const path = r.path.trim()
    return compact({
      enable: r.enabled ? undefined : false,
      path: r === site.root && path === "/" ? undefined : path || undefined,
      ip: r.host.trim() || undefined,
      port: toNumber(r.port),
      websocket: r.websocket || undefined,
      disable_cache: r.disableCache || undefined,
      realtime: r.realtime || undefined,
      options: options(r.options, owner, at),
    })
  }

  const servers = sites.map((site, i): ServerJSON => {
    const at = `servers[${i}]`
    owners.set(at, siteKey(site))
    if (site.raw !== null) {
      return compact({
        enable: site.enabled ? undefined : false,
        raw: site.raw,
      })
    }
    return compact({
      server_name: domainsOf(site).join(" "),
      enable: site.enabled ? undefined : false,
      listen_port: toNumber(site.listenPort),
      https: site.https || undefined,
      cert_name: (site.https && site.certName.trim()) || undefined,
      gzip: site.gzip || undefined,
      options: options(site.options, siteKey(site), at),
      default: location(site, site.root, `${at}.default`),
      locations:
        site.routes.length > 0
          ? site.routes.map((r, j) =>
              location(site, r, `${at}.locations[${j}]`)
            )
          : undefined,
    })
  })
  return { config: { servers }, owners }
}

// resolveField returns the key of the input for a path in a server-side
// error, such as "servers[0].locations[1].port", or undefined.
export function resolveField(
  owners: Map<string, string>,
  path: string
): string | undefined {
  for (let prefix = path; ;) {
    const owner = owners.get(prefix)
    if (owner !== undefined) return owner + path.slice(prefix.length)
    const parent = prefix.replace(/(\.\w+|\[\d+\])$/, "")
    if (parent === prefix) return undefined
    prefix = parent
  }
}

export function toYaml(sites: Site[]): string {
  return stringify(serialize(sites).config, { lineWidth: 0 })
}

export type ImportProblem = {
  code:
    | "empty"
    | "syntax"
    | "unknown_field"
    | "expected_mapping"
    | "expected_list"
    | "expected_directive"
  path: string // such as "servers[0].default"; empty for the whole document
  detail?: string // the parser's message, for syntax errors
}

export class ImportError extends Error {
  readonly problems: ImportProblem[]
  constructor(problems: ImportProblem[]) {
    super(problems.map((p) => `${p.path}: ${p.detail ?? p.code}`).join("\n"))
    this.problems = problems
  }
}

const serverFields = new Set([
  "server_name",
  "enable",
  "listen_port",
  "https",
  "cert_name",
  "gzip",
  "options",
  "default",
  "locations",
  "raw",
])

const locationFields = new Set([
  "enable",
  "path",
  "ip",
  "port",
  "websocket",
  "disable_cache",
  "realtime",
  "options",
])

// parseYaml converts a YAML (or JSON) configuration into sites.
export function parseYaml(text: string): Site[] {
  let doc: unknown
  try {
    doc = parse(text)
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    throw new ImportError([{ code: "syntax", path: "", detail }])
  }
  if (doc == null) throw new ImportError([{ code: "empty", path: "" }])
  return sitesFromConfig(doc)
}

// A site as stored on the server: one entry of servers.
type Stored = { id: string; config: ServerJSON }

// toServer converts site into one entry of servers.
export function toServer(site: Site): ServerJSON {
  return serialize([site]).config.servers[0]
}

// sameConfig tells whether two sites have the same configuration, i.e.
// saving one in place of the other would change nothing. What the server
// ignores does not count: IDs, blank directives, surrounding spaces.
export function sameConfig(a: Site, b: Site): boolean {
  return JSON.stringify(toServer(a)) === JSON.stringify(toServer(b))
}

export function fromStored(stored: Stored): Site {
  const [site] = sitesFromConfig({ servers: [stored.config] })
  return { ...site, id: stored.id }
}

// sitesFromConfig converts a parsed configuration into sites. Like the
// server, it rejects unknown fields; other problems are left for the server
// to report once the sites are in the builder.
function sitesFromConfig(doc: unknown): Site[] {
  const problems: ImportProblem[] = []
  const mapping = (v: unknown, at: string, fields: Set<string>) => {
    if (v == null) return {}
    if (typeof v !== "object" || Array.isArray(v)) {
      problems.push({ code: "expected_mapping", path: at })
      return {}
    }
    for (const key of Object.keys(v)) {
      if (!fields.has(key)) {
        problems.push({ code: "unknown_field", path: `${at}.${key}` })
      }
    }
    return v as Record<string, unknown>
  }
  const list = (v: unknown, at: string): unknown[] => {
    if (v == null) return []
    if (Array.isArray(v)) return v
    problems.push({ code: "expected_list", path: at })
    return []
  }
  const str = (v: unknown) => (v == null ? "" : String(v))

  const directives = (v: unknown, at: string): Directive[] =>
    list(v, at).map((o, i) => {
      if (typeof o === "string") return { id: newId(), text: o.trim() }
      if (o && typeof o === "object" && "key" in o) {
        const { key, value } = o as { key: unknown; value?: unknown }
        return { id: newId(), text: `${str(key)} ${str(value)}`.trim() }
      }
      problems.push({ code: "expected_directive", path: `${at}[${i}]` })
      return { id: newId(), text: "" }
    })

  const route = (v: unknown, at: string): Route => {
    const l = mapping(v, at, locationFields)
    return {
      id: newId(),
      enabled: l.enable !== false,
      path: str(l.path),
      host: str(l.ip),
      port: str(l.port),
      websocket: l.websocket === true,
      disableCache: l.disable_cache === true,
      realtime: l.realtime === true,
      options: directives(l.options, `${at}.options`),
    }
  }

  const root = mapping(doc, "config", new Set(["servers"]))
  const sites = list(root.servers, "servers").map((v, i): Site => {
    const at = `servers[${i}]`
    const s = mapping(v, at, serverFields)
    const def = route(s.default, `${at}.default`)
    def.path ||= "/"
    return {
      id: newId(),
      enabled: s.enable !== false,
      serverName: str(s.server_name),
      https: s.https === true || (s.https == null && !!s.cert_name),
      certName: str(s.cert_name),
      listenPort: str(s.listen_port),
      gzip: s.gzip === true,
      options: directives(s.options, `${at}.options`),
      root: def,
      routes: list(s.locations, `${at}.locations`).map((l, j) =>
        route(l, `${at}.locations[${j}]`)
      ),
      raw: s.raw == null ? null : str(s.raw),
    }
  })
  if (problems.length > 0) throw new ImportError(problems)
  return sites
}

function toNumber(s: string): number | undefined {
  const n = Number(s)
  return s.trim() === "" || Number.isNaN(n) ? undefined : n
}

// compact drops undefined fields, keeping the YAML export minimal.
function compact<T extends object>(o: T): T {
  return Object.fromEntries(
    Object.entries(o).filter(([, v]) => v !== undefined)
  ) as T
}
