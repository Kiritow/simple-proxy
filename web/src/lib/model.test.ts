import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"

import {
  ImportError,
  certCovers,
  domainsOf,
  fromStored,
  newRoute,
  newSite,
  parseYaml,
  rawCertificates,
  rawServerNames,
  resolveField,
  sameConfig,
  routeKey,
  serialize,
  siteKey,
  toServer,
  toYaml,
  uncovered,
} from "./model.ts"

const example = readFileSync(
  new URL("../../../config.yaml.example", import.meta.url),
  "utf8"
)

test("parseYaml reads the example", () => {
  const [site] = parseYaml(example)
  assert.equal(site.serverName, "example.com")
  assert.equal(site.https, false)
  assert.equal(site.gzip, true)
  assert.deepEqual(
    site.options.map((d) => d.text),
    ["client_max_body_size 64m"]
  )
  assert.equal(site.root.path, "/")
  assert.equal(site.root.host, "127.0.0.1")
  assert.equal(site.root.port, "3000")
  const [api] = site.routes
  assert.equal(api.path, "/api")
  assert.equal(api.host, "") // inherited
  assert.equal(api.port, "3001")
  assert.equal(api.websocket && api.disableCache && api.realtime, true)
  assert.deepEqual(
    api.options.map((d) => d.text),
    ['add_header X-Example "value"', "proxy_read_timeout 300s"] // both forms
  )
})

test("YAML round trip keeps the configuration", () => {
  const sites = parseYaml(example)
  const again = parseYaml(toYaml(sites))
  assert.deepEqual(serialize(again).config, serialize(sites).config)
})

test("serialize omits defaults", () => {
  const site = newSite()
  site.serverName = "  a.example.com   b.example.com "
  site.root.port = "3000"
  site.certName = "ignored.example.com" // HTTPS is off
  const { config } = serialize([site])
  assert.deepEqual(config, {
    servers: [
      { server_name: "a.example.com b.example.com", default: { port: 3000 } },
    ],
  })
})

test("serialize writes HTTPS, disabled items and non-empty directives", () => {
  const site = newSite()
  site.serverName = "example.com"
  site.https = true
  site.certName = "*.example.com"
  site.enabled = false
  site.root.enabled = false
  site.options = [
    { id: "1", text: " " },
    { id: "2", text: "client_max_body_size 1m" },
  ]
  const { config } = serialize([site])
  assert.deepEqual(config.servers[0], {
    server_name: "example.com",
    enable: false,
    https: true,
    cert_name: "*.example.com",
    options: ["client_max_body_size 1m"],
    default: { enable: false },
  })
})

test("resolveField maps server error paths to inputs", () => {
  const site = newSite()
  const route = newRoute("/api")
  route.options = [
    { id: "x", text: "" }, // skipped, so the next one is options[0]
    { id: "y", text: "proxy_read_timeout 1s" },
  ]
  site.routes = [route]
  const { owners } = serialize([site])
  const s = siteKey(site)
  const r = routeKey(site, route)
  assert.equal(
    resolveField(owners, "servers[0].server_name"),
    `${s}.server_name`
  )
  assert.equal(
    resolveField(owners, "servers[0].default.port"),
    `${routeKey(site, site.root)}.port`
  )
  assert.equal(
    resolveField(owners, "servers[0].locations[0].path"),
    `${r}.path`
  )
  assert.equal(
    resolveField(owners, "servers[0].locations[0].options[0]"),
    `${r}/d:y`
  )
  assert.equal(resolveField(owners, "servers[1].server_name"), undefined)
  assert.equal(resolveField(owners, "nonsense"), undefined)
})

test("parseYaml derives HTTPS from cert_name", () => {
  const [implicit, explicit] = parseYaml(`
servers:
- server_name: a.example.com
  cert_name: "*.example.com"
  default: {port: 1}
- server_name: b.example.com
  https: false
  cert_name: "*.example.com"
  default: {port: 1}
`)
  assert.equal(implicit.https, true)
  assert.equal(explicit.https, false)
})

test("parseYaml rejects unknown fields and bad documents", () => {
  const problems = (text: string) => {
    try {
      parseYaml(text)
    } catch (err) {
      assert.ok(err instanceof ImportError)
      return err.problems
    }
    assert.fail("parseYaml succeeded")
  }
  assert.deepEqual(
    problems(
      "servers:\n- server_name: a\n  websockets: true\n  default: {port: 1}\n"
    ),
    [{ code: "unknown_field", path: "servers[0].websockets" }]
  )
  assert.deepEqual(problems(""), [{ code: "empty", path: "" }])
  const [syntax] = problems("servers: [")
  assert.equal(syntax.code, "syntax")
  assert.match(syntax.detail ?? "", /flow sequence/i)
  assert.deepEqual(problems("servers: 3"), [
    { code: "expected_list", path: "servers" },
  ])
})

test("certCovers matches exact and single-level wildcard names", () => {
  assert.ok(certCovers("example.com", "example.com"))
  assert.ok(certCovers("*.example.com", "www.example.com"))
  assert.ok(!certCovers("*.example.com", "example.com"))
  assert.ok(!certCovers("*.example.com", "a.b.example.com"))
  assert.ok(!certCovers("example.com", "www.example.com"))
})

test("uncovered checks every name of the certificate", () => {
  const cert = ["example.com", "www.example.com", "*.api.example.com"]
  assert.deepEqual(
    uncovered(["example.com", "www.example.com", "v1.api.example.com"], cert),
    []
  )
  assert.deepEqual(uncovered(["example.com", "shop.example.com"], cert), [
    "shop.example.com",
  ])
})

test("stored sites keep their IDs and content", () => {
  const [site] = parseYaml(example)
  const stored = { id: "abc", config: toServer(site) }
  const loaded = fromStored(JSON.parse(JSON.stringify(stored)))
  assert.equal(loaded.id, "abc")
  assert.deepEqual(toServer(loaded), stored.config)
})

test("sameConfig compares what saving would store", () => {
  const [site] = parseYaml(example)
  const loaded = fromStored({ id: "abc", config: toServer(site) })
  assert.ok(sameConfig(site, loaded), "a stored site, loaded again")
  assert.ok(sameConfig(newSite(), newSite()), "blank sites, with other IDs")

  // Blank directives and spaces are not stored.
  const padded = {
    ...loaded,
    serverName: ` ${loaded.serverName} `,
    options: [...loaded.options, { id: "new", text: "  " }],
  }
  assert.ok(sameConfig(site, padded), "blank directive and spaces")

  const changed = { ...loaded, root: { ...loaded.root, port: "4000" } }
  assert.ok(!sameConfig(site, changed), "another port")
  assert.ok(!sameConfig(site, { ...loaded, enabled: false }), "disabled")
  assert.ok(!sameConfig(newSite(), site), "a new site with content")
})

test("an incomplete site survives the round trip", () => {
  const draft = newSite() // no domain, no port
  draft.routes = [newRoute()]
  const loaded = fromStored({ id: "x", config: toServer(draft) })
  assert.equal(loaded.serverName, "")
  assert.equal(loaded.root.port, "")
  assert.equal(loaded.routes.length, 1)
})

test("raw sites serialize to their raw configuration only", () => {
  const raw =
    'server {\n  server_name a.example.com "b.example.com" _; # main\n  ssl_certificate {{cert "Shop \\"2026\\""}};\n}\n'
  const site = {
    ...newSite(),
    serverName: "ignored.example.com",
    raw,
    enabled: false,
  }
  const { config } = serialize([site])
  assert.deepEqual(config.servers[0], { enable: false, raw })
  const [back] = parseYaml(toYaml([site]))
  assert.equal(back.raw, raw)
  assert.equal(back.enabled, false)
  assert.deepEqual(domainsOf(back), ["a.example.com", "b.example.com"])
  assert.deepEqual(rawCertificates(raw), ['Shop "2026"'])
  // Builder sites have none.
  assert.equal(
    parseYaml("servers:\n- server_name: c.example.com\n")[0].raw,
    null
  )
})

test("rawServerNames skips comments and catch-alls", () => {
  assert.deepEqual(
    rawServerNames(
      "# server_name old.example.com;\nserver{server_name _ x.example.com;}"
    ),
    ["x.example.com"]
  )
})
