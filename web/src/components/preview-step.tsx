import { useEffect, useMemo, useState } from "react"
import {
  ArrowRight,
  Check,
  CircleAlert,
  Copy,
  Download,
  RefreshCw,
} from "lucide-react"

import { CodeBlock, NginxCode, YamlCode } from "@/components/code-view"
import { StepHeader } from "@/components/form"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardHeader } from "@/components/ui/card"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  copyText,
  downloadText,
  generateConfig,
  type ApiError,
  type GenerateResult,
} from "@/lib/api"
import { describeProblem, useI18n, type Messages } from "@/lib/i18n"
import {
  describeKey,
  resolveField,
  serialize,
  toYaml,
  type Site,
} from "@/lib/model"

type Generated = { result: GenerateResult; owners: Map<string, string> }

export function PreviewStep({
  sites,
  onFix,
}: {
  sites: Site[]
  onFix: (key: string) => void
}) {
  const { t } = useI18n()
  const [generated, setGenerated] = useState<Generated | null>(null)
  const [attempt, setAttempt] = useState(0)
  const [tab, setTab] = useState("nginx")
  const [copied, setCopied] = useState(false)
  const yaml = useMemo(() => toYaml(sites), [sites])

  useEffect(() => {
    let active = true
    const { config, owners } = serialize(sites)
    generateConfig(config).then((result) => {
      if (active) setGenerated({ result, owners })
    })
    return () => {
      active = false
    }
  }, [sites, attempt])

  const regenerate = () => {
    setGenerated(null)
    setAttempt((n) => n + 1)
  }

  const result = generated?.result
  const files: Record<string, { name: string; text: string | null }> = {
    nginx: { name: "proxy.conf", text: result?.ok ? result.config : null },
    yaml: { name: "config.yaml", text: yaml },
  }
  const file = files[tab]

  const copy = async () => {
    if (file.text === null) return
    try {
      await copyText(file.text)
    } catch (err) {
      console.error(err) // e.g. clipboard permission denied
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  const enabled = sites.filter((s) => s.enabled)
  const routes = enabled.reduce(
    (n, s) => n + [s.root, ...s.routes].filter((r) => r.enabled).length,
    0
  )
  const https = enabled.filter((s) => s.https).length

  return (
    <div className="space-y-6">
      <StepHeader
        title={t.preview.title}
        description={t.preview.description}
        actions={
          <Button variant="outline" onClick={regenerate} disabled={!generated}>
            <RefreshCw />
            {t.preview.regenerate}
          </Button>
        }
      />

      <div className="grid grid-cols-3 gap-3">
        <Stat label={t.preview.sites} value={enabled.length} />
        <Stat label={t.preview.routes} value={routes} />
        <Stat label={t.preview.httpsSites} value={https} />
      </div>

      <Tabs value={tab} onValueChange={setTab}>
        <Card>
          <CardHeader className="items-center">
            <TabsList>
              <TabsTrigger value="nginx">proxy.conf</TabsTrigger>
              <TabsTrigger value="yaml">config.yaml</TabsTrigger>
            </TabsList>
            <CardAction className="flex gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={file.text === null}
                onClick={copy}
              >
                {copied ? <Check /> : <Copy />}
                {copied ? t.preview.copied : t.preview.copy}
              </Button>
              <Button
                size="sm"
                disabled={file.text === null}
                onClick={() =>
                  file.text !== null && downloadText(file.name, file.text)
                }
              >
                <Download />
                {t.preview.download}
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            <TabsContent value="nginx" className="space-y-2">
              {!result ? (
                <div className="flex items-center justify-center gap-2 py-16 text-muted-foreground">
                  <Spinner />
                  {t.preview.generating}
                </div>
              ) : result.ok ? (
                <>
                  <CodeBlock>
                    <NginxCode text={result.config} />
                  </CodeBlock>
                  <p className="text-xs text-muted-foreground">
                    {t.preview.includeHint}
                  </p>
                </>
              ) : (
                <ProblemList
                  error={result.error}
                  owners={generated.owners}
                  sites={sites}
                  onFix={onFix}
                />
              )}
            </TabsContent>
            <TabsContent value="yaml" className="space-y-2">
              <CodeBlock>
                <YamlCode text={yaml} />
              </CodeBlock>
              <p className="text-xs text-muted-foreground">
                {t.preview.yamlHint}
              </p>
            </TabsContent>
          </CardContent>
        </Card>
      </Tabs>
    </div>
  )
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-xl border bg-card px-4 py-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="font-heading text-2xl font-semibold tabular-nums">
        {value}
      </div>
    </div>
  )
}

function ProblemList({
  error,
  owners,
  sites,
  onFix,
}: {
  error: ApiError
  owners: Map<string, string>
  sites: Site[]
  onFix: (key: string) => void
}) {
  const { t } = useI18n()
  const items =
    error.fields.length > 0
      ? error.fields.map((f) => ({ ...f, key: resolveField(owners, f.field) }))
      : [{ field: "", message: error.message, key: undefined }]

  return (
    <Alert variant="destructive">
      <CircleAlert />
      <AlertTitle>{title(t, error.status)}</AlertTitle>
      <AlertDescription>
        <ul className="mt-1 w-full divide-y divide-destructive/15">
          {items.map((p, i) => (
            <li key={i} className="flex items-center gap-3 py-2">
              <div className="min-w-0 flex-1">
                {(p.key || p.field) && (
                  <p className="font-medium">
                    {p.key ? describeKey(sites, p.key, t.describe) : p.field}
                  </p>
                )}
                <p className="break-words">{describeProblem(t, p)}</p>
              </div>
              {p.key && (
                <Button
                  variant="outline"
                  size="xs"
                  onClick={() => onFix(p.key!)}
                >
                  {t.preview.fix}
                  <ArrowRight />
                </Button>
              )}
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  )
}

function title(t: Messages, status: number): string {
  switch (status) {
    case 0:
      return t.preview.unreachable
    case 400:
      return t.preview.invalid
    case 422:
      return t.preview.certMissing
    default:
      return t.preview.failed(status)
  }
}
