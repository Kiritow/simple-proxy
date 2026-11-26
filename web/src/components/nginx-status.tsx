import { useEffect, useState, type ReactNode } from "react"
import { Check, CircleAlert, Copy, Download, RefreshCw } from "lucide-react"
import { cn } from "cn"
import { toast } from "sonner"

import { CodeBlock, NginxCode } from "@/components/code-view"
import { Output } from "@/components/form"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  api,
  copyText,
  downloadText,
  errorOf,
  type ApiError,
  type Status,
} from "@/lib/api"
import { formatAgo } from "@/lib/format"
import type { Resource } from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"

type State = "loading" | "idle" | "ok" | "error"

function stateOf(status: Resource<Status>): State {
  if (!status.data) return "loading"
  if (!status.data.applies) return "idle"
  return status.data.error ? "error" : "ok"
}

// StatusButton shows whether nginx runs the stored sites.
export function StatusButton({
  status,
  onClick,
}: {
  status: Resource<Status>
  onClick: () => void
}) {
  const { t } = useI18n()
  const state = stateOf(status)
  const label = {
    loading: t.status.loading,
    idle: t.status.notApplying,
    ok: t.status.upToDate,
    error: t.status.outOfDate,
  }[state]
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={onClick}
      title={status.data?.nginx?.version}
      className={cn(state === "error" && "text-destructive")}
    >
      <span
        className={cn(
          "size-2 rounded-full",
          state === "ok" && "bg-emerald-500",
          state === "error" && "bg-destructive",
          (state === "idle" || state === "loading") && "bg-muted-foreground/40"
        )}
      />
      <span className="max-sm:sr-only">{label}</span>
    </Button>
  )
}

export function StatusDialog({
  open,
  onOpenChange,
  status,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  status: Resource<Status>
}) {
  const { t, lang } = useI18n()
  const [tab, setTab] = useState("status")
  const [applying, setApplying] = useState(false)
  const s = status.data

  const apply = async () => {
    setApplying(true)
    try {
      await api.apply()
      toast.success(t.status.applied)
    } catch (err) {
      toast.error(t.status.applyFailed, { description: errorOf(err).message })
    }
    setApplying(false)
    status.reload()
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[90svh] flex-col sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {t.status.title}
            {s?.applies && s.nginx && (
              <Badge variant="secondary" className="font-mono">
                {s.nginx.version}
              </Badge>
            )}
          </DialogTitle>
          <DialogDescription>
            {s?.applies ? t.status.description : t.status.notApplyingText}
          </DialogDescription>
        </DialogHeader>
        <Tabs value={tab} onValueChange={setTab} className="min-h-0 flex-1">
          <TabsList>
            <TabsTrigger value="status">{t.status.statusTab}</TabsTrigger>
            <TabsTrigger value="config">{t.status.configTab}</TabsTrigger>
          </TabsList>
          <TabsContent value="status" className="flex flex-col gap-4 pt-2">
            {s?.error && (
              <Alert variant="destructive">
                <CircleAlert />
                <AlertTitle>{t.status.outOfDate}</AlertTitle>
                <AlertDescription className="gap-2">
                  <p>{t.status.outOfDateText}</p>
                  <Output>
                    {[s.error, s.output].filter(Boolean).join("\n")}
                  </Output>
                </AlertDescription>
              </Alert>
            )}
            {s && (
              <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[auto_1fr]">
                {s.applies && (
                  <>
                    <Item term={t.status.version}>
                      <NginxVersion status={s} />
                    </Item>
                    <Item term={t.status.configFile} mono>
                      {s.config_path}
                    </Item>
                    <Item term={t.status.testCommand} mono>
                      {s.test_command || "—"}
                    </Item>
                    <Item term={t.status.reloadCommand} mono>
                      {s.reload_command || "—"}
                    </Item>
                    <Item term={t.status.lastApplied}>
                      {s.applied_at ? (
                        <time dateTime={s.applied_at} title={s.applied_at}>
                          {formatAgo(s.applied_at, lang)}
                        </time>
                      ) : (
                        t.status.never
                      )}
                    </Item>
                  </>
                )}
                <Item term={t.status.acme}>
                  {s.acme ? t.status.acmeAvailable : t.status.acmeUnavailable}
                </Item>
                {s.acme && (
                  <Item term={t.status.webroot} mono={Boolean(s.acme_webroot)}>
                    {s.acme_webroot || t.status.noWebroot}
                  </Item>
                )}
                <Item term={t.status.certDir} mono>
                  {s.cert_dir}
                </Item>
              </dl>
            )}
          </TabsContent>
          <TabsContent value="config" className="min-h-0 pt-2">
            {tab === "config" && <GeneratedConfig />}
          </TabsContent>
        </Tabs>
        {s?.applies && (
          <DialogFooter>
            <Button onClick={apply} disabled={applying}>
              {applying ? <Spinner /> : <RefreshCw />}
              {t.status.applyAgain}
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  )
}

// NginxVersion shows the version of nginx, with how it was built, or why it
// is not known.
function NginxVersion({ status: s }: { status: Status }) {
  const { t } = useI18n()
  if (!s.nginx) {
    return (
      <div className="flex flex-col gap-1">
        <span>{t.status.versionUnknown}</span>
        {s.nginx_error ? (
          <Output>{s.nginx_error}</Output>
        ) : (
          !s.version_command && (
            <span className="text-xs text-muted-foreground">
              {t.status.noVersionCommand}
            </span>
          )
        )}
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-1">
      <span className="font-mono text-xs leading-5">{s.nginx.version}</span>
      {s.nginx.build && (
        <details>
          <summary className="cursor-pointer text-xs text-muted-foreground select-none hover:text-foreground">
            {t.status.buildDetails}
          </summary>
          <Output>{s.nginx.build}</Output>
        </details>
      )}
    </div>
  )
}

function Item({
  term,
  mono,
  children,
}: {
  term: string
  mono?: boolean
  children: ReactNode
}) {
  return (
    <>
      <dt className="text-muted-foreground">{term}</dt>
      <dd
        className={cn(
          "min-w-0 break-all",
          mono && "font-mono text-xs leading-5"
        )}
      >
        {children}
      </dd>
    </>
  )
}

function GeneratedConfig() {
  const { t } = useI18n()
  const [config, setConfig] = useState<string | null>(null)
  const [error, setError] = useState<ApiError | null>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    let active = true
    api.nginxConf().then(
      (text) => active && setConfig(text),
      (err: unknown) => active && setError(errorOf(err))
    )
    return () => {
      active = false
    }
  }, [])

  if (error) {
    return (
      <Alert variant="destructive">
        <CircleAlert />
        <AlertTitle>{error.message}</AlertTitle>
      </Alert>
    )
  }
  if (config === null) {
    return (
      <div className="flex justify-center py-10">
        <Spinner />
      </div>
    )
  }
  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      <div className="flex items-center gap-2">
        <p className="mr-auto text-xs text-muted-foreground">
          {t.status.configHint}
        </p>
        <Button
          variant="outline"
          size="sm"
          onClick={async () => {
            await copyText(config)
            setCopied(true)
            setTimeout(() => setCopied(false), 1500)
          }}
        >
          {copied ? <Check /> : <Copy />}
          {copied ? t.app.copied : t.app.copy}
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={() => downloadText("proxy.conf", config)}
        >
          <Download />
          {t.app.download}
        </Button>
      </div>
      <div className="max-h-[55svh] min-h-0 overflow-auto">
        <CodeBlock>
          <NginxCode text={config} />
        </CodeBlock>
      </div>
    </div>
  )
}
