// How to set up MCP clients for the console's MCP endpoint.

// McpClient is a kind of MCP client that setups are shown for.
export type McpClient =
  "claude-code" | "cursor" | "vscode" | "claude-desktop" | "other"

export const mcpClients: readonly McpClient[] = [
  "claude-code",
  "cursor",
  "vscode",
  "claude-desktop",
  "other",
]

// McpSetup is how to set up a client: a command to run, or configuration
// to add to its file.
export type McpSetup = {
  client: McpClient
  file?: string // the configuration file to add text to
  text: string
}

// The name of the server in the clients' configuration.
const name = "simple-proxy"

// mcpSetups returns how to set up each kind of client for the MCP endpoint
// at url, with the given API token.
export function mcpSetups(url: string, token: string): McpSetup[] {
  const authorization = `Bearer ${token}`
  const json = (value: unknown) => JSON.stringify(value, null, 2)
  return [
    {
      client: "claude-code",
      text: `claude mcp add --transport http ${name} ${url} --header "Authorization: ${authorization}"`,
    },
    {
      client: "cursor",
      file: "~/.cursor/mcp.json",
      text: json({
        mcpServers: {
          [name]: { url, headers: { Authorization: authorization } },
        },
      }),
    },
    {
      client: "vscode",
      file: ".vscode/mcp.json",
      text: json({
        servers: {
          [name]: {
            type: "http",
            url,
            headers: { Authorization: authorization },
          },
        },
      }),
    },
    {
      // Claude Desktop runs local (stdio) servers from its configuration
      // file: mcp-remote bridges to the endpoint. The header is passed in an
      // environment variable, and without spaces in the arguments, which
      // some clients do not quote.
      client: "claude-desktop",
      file: "claude_desktop_config.json",
      text: json({
        mcpServers: {
          [name]: {
            command: "npx",
            args: [
              "-y",
              "mcp-remote",
              url,
              "--header",
              "Authorization:${AUTH_HEADER}",
              ...(needsAllowHttp(url) ? ["--allow-http"] : []),
            ],
            env: { AUTH_HEADER: authorization },
          },
        },
      }),
    },
    {
      client: "other",
      text: json({
        mcpServers: {
          [name]: {
            type: "http",
            url,
            headers: { Authorization: authorization },
          },
        },
      }),
    },
  ]
}

// needsAllowHttp reports whether mcp-remote needs --allow-http to connect
// to url: it only connects over plain HTTP to the machine it runs on
// without it.
export function needsAllowHttp(url: string): boolean {
  const u = new URL(url)
  return (
    u.protocol === "http:" &&
    !["localhost", "127.0.0.1", "[::1]"].includes(u.hostname)
  )
}
