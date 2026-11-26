import { useState, type ReactNode } from "react"
import {
  CircleAlert,
  History,
  KeyRound,
  MessageSquareText,
  RotateCw,
  UserCheck,
} from "lucide-react"

import { PageHeader } from "@/components/form"
import { McpSetup, Snippet } from "@/components/mcp-setup"
import { TokensDialog } from "@/components/tokens-dialog"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Spinner } from "@/components/ui/spinner"
import { mcpUrl, type McpTool } from "@/lib/api"
import { useAuth } from "@/lib/auth"
import { useMcpTools } from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"

// The placeholder for the token in the setups.
const placeholder = "YOUR_API_TOKEN"

// McpPage explains how to let AI assistants manage the sites over MCP, as
// the signed-in account, and lists the tools they get.
export function McpPage() {
  const { t } = useI18n()
  const { me } = useAuth()
  const { permissions } = me
  const [tokens, setTokens] = useState<{ key: number } | null>(null)
  const url = mcpUrl()

  const examples = [
    ...Object.values(t.mcp.examples.sites),
    ...(permissions.all_sites ? Object.values(t.mcp.examples.allSites) : []),
    ...(permissions.certificates
      ? Object.values(t.mcp.examples.certificates)
      : []),
  ]

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title={t.mcp.title} description={t.mcp.description} />

      <div className="grid gap-4 md:grid-cols-3">
        <Point icon={<UserCheck />} title={t.mcp.asYouTitle}>
          {t.mcp.asYou(t.roles.names[me.user.role])}
        </Point>
        <Point icon={<History />} title={t.mcp.appliedTitle}>
          {t.mcp.applied}
        </Point>
        <Point icon={<KeyRound />} title={t.mcp.tokensTitle}>
          {t.mcp.tokens}
        </Point>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t.mcp.step1Title}</CardTitle>
          <CardDescription>{t.mcp.step1Text}</CardDescription>
        </CardHeader>
        <CardContent>
          <Button onClick={() => setTokens({ key: Date.now() })}>
            <KeyRound />
            {t.mcp.openTokens}
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t.mcp.step2Title}</CardTitle>
          <CardDescription>{t.mcp.step2Text(placeholder)}</CardDescription>
        </CardHeader>
        <CardContent className="flex min-w-0 flex-col gap-5">
          <Snippet
            title={t.mcp.endpoint}
            description={t.mcp.endpointText}
            text={url}
          />
          <McpSetup url={url} token={placeholder} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t.mcp.step3Title}</CardTitle>
          <CardDescription>{t.mcp.step3Text}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <ul className="flex flex-col gap-2">
            {examples.map((example) => (
              <li key={example} className="flex gap-2.5 text-sm">
                <MessageSquareText className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                <span>{example}</span>
              </li>
            ))}
          </ul>
          <ul className="flex list-disc flex-col gap-1.5 pl-5 text-sm text-muted-foreground">
            <li>{t.mcp.tips.preview}</li>
            <li>{t.mcp.tips.review}</li>
            {!permissions.all_sites && <li>{t.mcp.tips.restricted}</li>}
          </ul>
        </CardContent>
      </Card>

      <Tools />

      {tokens && (
        <TokensDialog
          key={tokens.key}
          open
          onOpenChange={(o) => !o && setTokens(null)}
        />
      )}
    </div>
  )
}

// Point is one of the points of how assistants work.
function Point({
  icon,
  title,
  children,
}: {
  icon: ReactNode
  title: string
  children: ReactNode
}) {
  return (
    <Card className="gap-2">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-sm [&_svg]:size-4 [&_svg]:text-muted-foreground">
          {icon}
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent className="text-sm text-muted-foreground">
        {children}
      </CardContent>
    </Card>
  )
}

// Tools lists the tools that the MCP endpoint offers the account, as
// assistants get them.
function Tools() {
  const { t } = useI18n()
  const tools = useMcpTools()
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t.mcp.toolsTitle}</CardTitle>
        <CardDescription>{t.mcp.toolsText}</CardDescription>
      </CardHeader>
      <CardContent>
        {tools.data === null ? (
          tools.error ? (
            <Alert variant="destructive">
              <CircleAlert />
              <AlertTitle>{t.mcp.toolsFailed}</AlertTitle>
              <AlertDescription className="gap-2">
                <p>{tools.error.message}</p>
                <Button variant="outline" size="sm" onClick={tools.reload}>
                  <RotateCw />
                  {t.app.tryAgain}
                </Button>
              </AlertDescription>
            </Alert>
          ) : (
            <div className="flex justify-center py-6">
              <Spinner />
            </div>
          )
        ) : (
          <ul className="divide-y">
            {[...tools.data]
              .sort((a, b) => a.name.localeCompare(b.name))
              .map((tool) => (
                <ToolItem key={tool.name} tool={tool} />
              ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function ToolItem({ tool }: { tool: McpTool }) {
  const { t } = useI18n()
  const a = tool.annotations
  const kind = a?.readOnlyHint
    ? { label: t.mcp.readOnly, variant: "secondary" as const }
    : a?.destructiveHint
      ? { label: t.mcp.destructive, variant: "destructive" as const }
      : { label: t.mcp.changes, variant: "outline" as const }
  return (
    <li className="flex flex-col gap-1 py-3 first:pt-0 last:pb-0 sm:flex-row sm:gap-4">
      <div className="flex shrink-0 items-start gap-2 sm:w-64">
        <code className="font-mono text-sm font-medium">{tool.name}</code>
        <Badge variant={kind.variant}>{kind.label}</Badge>
      </div>
      <div className="min-w-0 text-sm">
        {a?.title && <div className="font-medium">{a.title}</div>}
        <p className="text-muted-foreground">{tool.description}</p>
      </div>
    </li>
  )
}
