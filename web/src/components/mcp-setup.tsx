import { useState } from "react"
import { Check, Copy, TriangleAlert } from "lucide-react"

import { CodeBlock } from "@/components/code-view"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription } from "@/components/ui/field"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { copyText } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { mcpSetups, needsAllowHttp, type McpClient } from "@/lib/mcp"

// McpSetup shows how to set up each kind of MCP client for the endpoint at
// url, with the given API token, or a placeholder for it.
export function McpSetup({ url, token }: { url: string; token: string }) {
  const { t } = useI18n()
  const [client, setClient] = useState<McpClient>("claude-code")
  return (
    <Tabs
      value={client}
      onValueChange={(v) => setClient(v as McpClient)}
      className="min-w-0"
    >
      <TabsList className="h-auto w-full flex-wrap">
        {mcpSetups(url, token).map((s) => (
          <TabsTrigger key={s.client} value={s.client}>
            {t.mcp.clients[s.client]}
          </TabsTrigger>
        ))}
      </TabsList>
      {mcpSetups(url, token).map((s) => (
        <TabsContent
          key={s.client}
          value={s.client}
          className="flex min-w-0 flex-col gap-3 pt-2"
        >
          <p className="text-sm text-muted-foreground">
            {t.mcp.clientNotes[s.client]}
          </p>
          {s.client === "claude-desktop" && needsAllowHttp(url) && (
            <Alert>
              <TriangleAlert />
              <AlertDescription>{t.mcp.allowHttp}</AlertDescription>
            </Alert>
          )}
          <Snippet
            title={s.file ? t.mcp.addTo(s.file) : t.mcp.run}
            text={s.text}
          />
        </TabsContent>
      ))}
    </Tabs>
  )
}

// Snippet shows text to copy, such as a command, with a button copying it.
export function Snippet({
  title,
  description,
  text,
}: {
  title: string
  description?: string
  text: string
}) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    await copyText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    <Field className="min-w-0 gap-1.5">
      <div className="flex items-center justify-between gap-2">
        <span className="min-w-0 text-sm font-medium break-words">{title}</span>
        <Button variant="ghost" size="sm" onClick={() => void copy()}>
          {copied ? <Check /> : <Copy />}
          {copied ? t.app.copied : t.app.copy}
        </Button>
      </div>
      {description && <FieldDescription>{description}</FieldDescription>}
      <CodeBlock>{text}</CodeBlock>
    </Field>
  )
}
