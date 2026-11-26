import assert from "node:assert/strict"
import { test } from "node:test"

import { mcpClients, mcpSetups, needsAllowHttp } from "./mcp.ts"

test("mcpSetups has a setup for every client, with the URL and the token", () => {
  const url = "https://proxy.example.com/console/mcp"
  const setups = mcpSetups(url, "sp_SECRET")
  assert.deepEqual(
    setups.map((s) => s.client),
    [...mcpClients]
  )
  for (const s of setups) {
    assert.ok(s.text.includes(url), s.client)
    assert.ok(s.text.includes("Bearer sp_SECRET"), s.client)
  }
  assert.equal(
    setups[0].text,
    `claude mcp add --transport http simple-proxy ${url} --header "Authorization: Bearer sp_SECRET"`
  )
})

test("the configuration files are valid JSON", () => {
  for (const s of mcpSetups("http://127.0.0.1:8081/mcp", "sp_X")) {
    if (s.file) assert.doesNotThrow(() => JSON.parse(s.text), s.client)
  }
  const vscode = JSON.parse(
    mcpSetups("http://127.0.0.1:8081/mcp", "sp_X")[2].text
  )
  assert.deepEqual(vscode.servers["simple-proxy"], {
    type: "http",
    url: "http://127.0.0.1:8081/mcp",
    headers: { Authorization: "Bearer sp_X" },
  })
})

test("mcp-remote gets --allow-http for plain HTTP to other machines", () => {
  const args = (url: string) =>
    JSON.parse(mcpSetups(url, "sp_X")[3].text).mcpServers["simple-proxy"]
      .args as string[]
  assert.ok(!args("http://127.0.0.1:8081/mcp").includes("--allow-http"))
  assert.ok(!args("https://proxy.example.com/mcp").includes("--allow-http"))
  assert.ok(args("http://10.0.0.5:81/mcp").includes("--allow-http"))
  // The header has no spaces, as some clients do not quote arguments.
  assert.ok(args("https://p/mcp").includes("Authorization:${AUTH_HEADER}"))
  assert.equal(needsAllowHttp("http://localhost:81/mcp"), false)
  assert.equal(needsAllowHttp("http://[::1]:81/mcp"), false)
})
