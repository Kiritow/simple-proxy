import { useEffect, useMemo, useState } from "react"
import { CircleAlert, Plus, Trash2, TriangleAlert } from "lucide-react"
import { toast } from "sonner"

import { CertificateField } from "@/components/certificate-field"
import { ChangesPanel } from "@/components/changes-panel"
import { IssueDialog } from "@/components/issue-dialog"
import {
  DirectiveList,
  IconButton,
  Output,
  TextField,
  ToggleCard,
} from "@/components/form"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { FieldDescription, FieldGroup } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  api,
  errorOf,
  type ApiError,
  type Certificate,
  type Status,
  type StoredSite,
} from "@/lib/api"
import {
  noProblems,
  toProblems,
  usePreview,
  type Problems,
  type Resource,
} from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"
import {
  DEFAULT_HOST,
  domainsOf,
  fromStored,
  newRoute,
  newSite,
  routeKey,
  serialize,
  siteKey,
  toServer,
  type Route,
  type Site,
} from "@/lib/model"

type Tab = "details" | "routes" | "https" | "advanced"
const tabs: Tab[] = ["details", "routes", "https", "advanced"]

// tabOf returns the tab showing the input with the given key.
function tabOf(site: Site, key: string): Tab {
  const route = /\/r:([^/.]+)/.exec(key)?.[1]
  const field = /\.(\w+)$/.exec(key)?.[1]
  const directive = key.includes("/d:")
  if (route && route !== site.root.id) return "routes"
  if (route)
    return !directive && (field === "ip" || field === "port")
      ? "details"
      : "advanced"
  if (field === "server_name") return "details"
  if (field === "cert_name") return "https"
  return "advanced"
}

// SiteDialog edits a site, or creates one if stored is null. Render it with a
// new key every time it opens, so that it starts from stored or initial.
export function SiteDialog({
  open,
  onOpenChange,
  stored,
  initial,
  certs,
  status,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  stored: StoredSite | null
  initial?: Site // for a new site
  certs: Resource<Certificate[]>
  status: Status | null
  onSaved: () => void
}) {
  const { t } = useI18n()
  const [site, setSite] = useState<Site>(
    () => (stored ? fromStored(stored) : initial) ?? newSite()
  )
  const [tab, setTab] = useState<Tab>("details")
  const [saving, setSaving] = useState(false)
  // Problems are shown once the site was changed, or saving was attempted.
  const [touched, setTouched] = useState(false)
  const [failure, setFailure] = useState<{
    error: ApiError
    problems: Problems
  } | null>(null)
  const [issuing, setIssuing] = useState<{ key: number } | null>(null)
  const [focusRequest, setFocusRequest] = useState<{ key: string } | null>(null)
  // The generation of the stored site that the edits are based on. Saving
  // fails if the site has changed since, i.e. in another window.
  const [base, setBase] = useState(stored?.generation ?? 0)

  const preview = usePreview(site, stored?.id ?? null)
  const problems = useMemo<Problems>(() => {
    if (!touched) return noProblems
    // Problems found when saving are kept until the site changes.
    return {
      byKey: new Map([
        ...preview.problems.byKey,
        ...(failure?.problems.byKey ?? []),
      ]),
    }
  }, [touched, preview.problems, failure])

  const latest = preview.preview?.generation ?? 0
  const deletedElsewhere = stored !== null && preview.error?.status === 404
  const changedElsewhere =
    stored !== null &&
    !deletedElsewhere &&
    ((latest !== 0 && latest !== base) || failure?.error.status === 409)

  // resolve settles a conflict with the site as stored now: by loading it,
  // or by basing the edits on it, so that saving overwrites it.
  const resolve = async (load: boolean) => {
    if (!stored) return
    let current: StoredSite | undefined
    try {
      current = (await api.sites()).find((s) => s.id === stored.id)
    } catch (err) {
      toast.error(errorOf(err).message)
      return
    }
    if (!current) return // deleted; the preview will say so
    if (load) {
      setSite(fromStored(current))
      setTouched(false)
    }
    setBase(current.generation)
    setFailure(null)
  }

  useEffect(() => {
    if (!focusRequest) return
    const timer = setTimeout(() => {
      document.getElementById(focusRequest.key)?.focus()
    }, 50) // after the tab is shown
    return () => clearTimeout(timer)
  }, [focusRequest])

  const change = (update: (s: Site) => Site) => {
    setSite(update)
    setTouched(true)
    setFailure(null)
  }
  const patch = (p: Partial<Site>) => change((s) => ({ ...s, ...p }))
  const patchRoot = (p: Partial<Route>) =>
    change((s) => ({ ...s, root: { ...s.root, ...p } }))
  const patchRoute = (id: string, p: Partial<Route>) =>
    change((s) => ({
      ...s,
      routes: s.routes.map((r) => (r.id === id ? { ...r, ...p } : r)),
    }))

  const key = siteKey(site)
  const root = routeKey(site, site.root)
  const name = domainsOf(site)[0] ?? t.sites.newSite
  const counts = Object.fromEntries(tabs.map((tab) => [tab, 0])) as Record<
    Tab,
    number
  >
  for (const k of problems.byKey.keys()) counts[tabOf(site, k)]++

  const save = async () => {
    setSaving(true)
    setTouched(true)
    try {
      const config = toServer(site)
      if (stored) await api.updateSite(stored.id, config, base)
      else await api.createSite(config)
    } catch (err) {
      const error = errorOf(err)
      const placed = toProblems(error, serialize([site]).owners)
      setFailure({ error, problems: placed })
      const first = [...placed.byKey.keys()][0]
      if (first) {
        setTab(tabOf(site, first))
        setFocusRequest({ key: first })
      }
      setSaving(false)
      return
    }
    setSaving(false)
    toast.success(
      status?.applies ? t.sites.saved(name) : t.sites.savedOnly(name)
    )
    onSaved()
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !saving && onOpenChange(o)}>
      <DialogContent className="flex h-[92svh] flex-col sm:max-w-6xl lg:h-[86svh]">
        <DialogHeader>
          <DialogTitle>
            {stored ? t.site.editTitle(name) : t.site.newTitle}
          </DialogTitle>
          <DialogDescription>{t.site.description}</DialogDescription>
        </DialogHeader>

        <div className="-mx-4 flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-1 lg:grid lg:grid-cols-2 lg:overflow-hidden">
          <div className="flex flex-col gap-4 lg:min-h-0 lg:overflow-y-auto lg:pr-1">
            {deletedElsewhere ? (
              <Alert variant="destructive">
                <CircleAlert />
                <AlertTitle>{t.site.deletedTitle}</AlertTitle>
                <AlertDescription>{t.site.deletedText}</AlertDescription>
              </Alert>
            ) : (
              changedElsewhere && (
                <Alert className="border-amber-500/40 bg-amber-500/10 text-amber-900 *:data-[slot=alert-description]:text-amber-900/90 dark:text-amber-200 dark:*:data-[slot=alert-description]:text-amber-200/90">
                  <TriangleAlert />
                  <AlertTitle>{t.site.changedTitle}</AlertTitle>
                  <AlertDescription className="gap-2">
                    <p>{t.site.changedText}</p>
                    <div className="flex flex-wrap gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => resolve(true)}
                      >
                        {t.site.loadSaved}
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => resolve(false)}
                      >
                        {t.site.keepMine}
                      </Button>
                    </div>
                  </AlertDescription>
                </Alert>
              )
            )}
            {failure && <SaveFailure error={failure.error} />}
            <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)}>
              <TabsList className="w-full">
                {tabs.map((tab) => (
                  <TabsTrigger key={tab} value={tab} className="gap-1.5">
                    {t.site[tab]}
                    {counts[tab] > 0 && (
                      <span className="size-1.5 rounded-full bg-destructive" />
                    )}
                  </TabsTrigger>
                ))}
              </TabsList>

              <TabsContent value="details" className="pt-3">
                <FieldGroup>
                  <TextField
                    id={`${key}.server_name`}
                    label={t.site.domains}
                    placeholder="example.com www.example.com"
                    description={t.site.domainsHint}
                    value={site.serverName}
                    onChange={(e) => patch({ serverName: e.target.value })}
                    error={problems.byKey.get(`${key}.server_name`)}
                    autoFocus={!stored}
                  />
                  <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_9rem]">
                    <TextField
                      id={`${root}.ip`}
                      label={t.site.host}
                      placeholder={DEFAULT_HOST}
                      value={site.root.host}
                      onChange={(e) => patchRoot({ host: e.target.value })}
                      error={problems.byKey.get(`${root}.ip`)}
                    />
                    <TextField
                      id={`${root}.port`}
                      label={t.site.port}
                      type="number"
                      min={1}
                      max={65535}
                      placeholder="3000"
                      value={site.root.port}
                      onChange={(e) => patchRoot({ port: e.target.value })}
                      error={problems.byKey.get(`${root}.port`)}
                    />
                  </div>
                  <RouteToggles
                    id={root}
                    route={site.root}
                    onChange={patchRoot}
                  />
                  <ToggleCard
                    id={`${key}.enabled`}
                    title={t.site.enabled}
                    description={t.site.enabledText}
                    checked={site.enabled}
                    onCheckedChange={(enabled) => patch({ enabled })}
                  />
                </FieldGroup>
              </TabsContent>

              <TabsContent value="routes" className="flex flex-col gap-3 pt-3">
                <FieldDescription>{t.site.routesText}</FieldDescription>
                {site.routes.map((route) => (
                  <RouteCard
                    key={route.id}
                    site={site}
                    route={route}
                    problems={problems}
                    onChange={(p) => patchRoute(route.id, p)}
                    onRemove={() =>
                      change((s) => ({
                        ...s,
                        routes: s.routes.filter((r) => r.id !== route.id),
                      }))
                    }
                  />
                ))}
                {site.routes.length === 0 && (
                  <p className="py-4 text-center text-sm text-muted-foreground">
                    {t.site.noRoutes}
                  </p>
                )}
                <Button
                  variant="outline"
                  className="w-fit"
                  onClick={() => {
                    const route = newRoute()
                    change((s) => ({ ...s, routes: [...s.routes, route] }))
                    setFocusRequest({ key: `${routeKey(site, route)}.path` })
                  }}
                >
                  <Plus />
                  {t.site.addRoute}
                </Button>
              </TabsContent>

              <TabsContent value="https" className="pt-3">
                <FieldGroup>
                  <ToggleCard
                    id={`${key}.https`}
                    title={t.site.httpsTitle}
                    description={t.site.httpsText}
                    checked={site.https}
                    onCheckedChange={(https) => patch({ https })}
                  />
                  {site.https && (
                    <CertificateField
                      site={site}
                      certs={certs}
                      error={problems.byKey.get(`${key}.cert_name`)}
                      onChange={(certName) => patch({ certName })}
                      onRequest={
                        status?.certificates
                          ? () => setIssuing({ key: Date.now() })
                          : undefined
                      }
                    />
                  )}
                </FieldGroup>
              </TabsContent>

              <TabsContent value="advanced" className="pt-3">
                <FieldGroup>
                  <div className="grid gap-4 sm:grid-cols-2">
                    <TextField
                      id={`${key}.listen_port`}
                      label={t.site.listenPort}
                      type="number"
                      min={1}
                      max={65535}
                      placeholder={site.https ? "443" : "80"}
                      value={site.listenPort}
                      onChange={(e) => patch({ listenPort: e.target.value })}
                      error={problems.byKey.get(`${key}.listen_port`)}
                    />
                    <TextField
                      id={`${root}.path`}
                      label={t.site.defaultPath}
                      description={t.site.defaultPathHint}
                      placeholder="/"
                      className="[&_input]:font-mono"
                      value={site.root.path}
                      onChange={(e) => patchRoot({ path: e.target.value })}
                      error={problems.byKey.get(`${root}.path`)}
                    />
                  </div>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <ToggleCard
                      id={`${key}.gzip`}
                      title={t.site.gzip}
                      description={t.site.gzipText}
                      checked={site.gzip}
                      onCheckedChange={(gzip) => patch({ gzip })}
                    />
                    <ToggleCard
                      id={`${root}.enabled`}
                      title={t.site.mainRoute}
                      description={t.site.mainRouteText}
                      checked={site.root.enabled}
                      onCheckedChange={(enabled) => patchRoot({ enabled })}
                    />
                  </div>
                  <DirectiveList
                    owner={key}
                    title={t.site.serverDirectives}
                    description={t.site.serverDirectivesText}
                    items={site.options}
                    onChange={(options) => patch({ options })}
                    problems={problems}
                    placeholder="client_max_body_size 64m"
                  />
                  <DirectiveList
                    owner={root}
                    title={t.site.routeDirectives}
                    description={t.site.routeDirectivesText}
                    items={site.root.options}
                    onChange={(options) => patchRoot({ options })}
                    problems={problems}
                    placeholder="proxy_read_timeout 300s"
                  />
                </FieldGroup>
              </TabsContent>
            </Tabs>
          </div>
          <ChangesPanel
            state={preview}
            touched={touched}
            className="min-h-80 shrink-0 lg:min-h-0"
          />
        </div>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={saving}>
              {t.app.cancel}
            </Button>
          </DialogClose>
          <Button onClick={save} disabled={saving || deletedElsewhere}>
            {saving && <Spinner />}
            {saving ? t.site.saving : t.site.save}
          </Button>
        </DialogFooter>
      </DialogContent>

      {issuing && (
        <IssueDialog
          key={issuing.key}
          open
          onOpenChange={(o) => !o && setIssuing(null)}
          initialDomains={domainsOf(site)}
          status={status}
          onIssued={(certName) => {
            certs.reload()
            patch({ certName })
          }}
        />
      )}
    </Dialog>
  )
}

function SaveFailure({ error }: { error: ApiError }) {
  const { t } = useI18n()
  if (error.status === 409 || error.status === 404) {
    return null // explained by the banner about other windows
  }
  if (error.status === 502) {
    return (
      <Alert variant="destructive">
        <CircleAlert />
        <AlertTitle>{t.site.applyFailed}</AlertTitle>
        <AlertDescription className="gap-2">
          <p>{t.site.applyFailedText}</p>
          <Output>{error.output || error.message}</Output>
        </AlertDescription>
      </Alert>
    )
  }
  return (
    <Alert variant="destructive">
      <CircleAlert />
      <AlertTitle>
        {error.fields.length > 0 ? t.site.fixFields : t.site.saveFailed}
      </AlertTitle>
      {error.fields.length === 0 && (
        <AlertDescription>{error.message}</AlertDescription>
      )}
    </Alert>
  )
}

function RouteToggles({
  id,
  route,
  onChange,
}: {
  id: string
  route: Route
  onChange: (patch: Partial<Route>) => void
}) {
  const { t } = useI18n()
  return (
    <div className="grid gap-3 sm:grid-cols-3">
      <ToggleCard
        id={`${id}.websocket`}
        title={t.site.websocket}
        description={t.site.websocketText}
        checked={route.websocket}
        onCheckedChange={(websocket) => onChange({ websocket })}
      />
      <ToggleCard
        id={`${id}.realtime`}
        title={t.site.realtime}
        description={t.site.realtimeText}
        checked={route.realtime}
        onCheckedChange={(realtime) => onChange({ realtime })}
      />
      <ToggleCard
        id={`${id}.disable_cache`}
        title={t.site.noCache}
        description={t.site.noCacheText}
        checked={route.disableCache}
        onCheckedChange={(disableCache) => onChange({ disableCache })}
      />
    </div>
  )
}

function RouteCard({
  site,
  route,
  problems,
  onChange,
  onRemove,
}: {
  site: Site
  route: Route
  problems: Problems
  onChange: (patch: Partial<Route>) => void
  onRemove: () => void
}) {
  const { t } = useI18n()
  const key = routeKey(site, route)
  const error = (field: string) => problems.byKey.get(`${key}.${field}`)
  const host = route.host || site.root.host || DEFAULT_HOST
  const port = route.port || site.root.port || "?"

  return (
    <Card size="sm" className={route.enabled ? undefined : "bg-muted/30"}>
      <CardHeader>
        <CardTitle className="flex min-w-0 flex-wrap items-center gap-x-2 font-mono">
          {route.path || t.site.newRoute}
          <span className="text-muted-foreground">
            → http://{host.includes(":") ? `[${host}]` : host}:{port}
          </span>
        </CardTitle>
        <CardDescription>{t.site.inheritHint}</CardDescription>
        <CardAction className="flex items-center gap-1">
          <Switch
            checked={route.enabled}
            onCheckedChange={(enabled) => onChange({ enabled })}
            aria-label={t.site.routeEnabled}
            className="mr-2"
          />
          <IconButton label={t.site.removeRoute} onClick={onRemove}>
            <Trash2 />
          </IconButton>
        </CardAction>
      </CardHeader>
      <CardContent>
        <FieldGroup>
          <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_7rem]">
            <TextField
              id={`${key}.path`}
              label={t.site.path}
              placeholder="/api"
              className="[&_input]:font-mono"
              value={route.path}
              onChange={(e) => onChange({ path: e.target.value })}
              error={error("path")}
            />
            <TextField
              id={`${key}.ip`}
              label={t.site.host}
              placeholder={site.root.host || DEFAULT_HOST}
              value={route.host}
              onChange={(e) => onChange({ host: e.target.value })}
              error={error("ip")}
            />
            <TextField
              id={`${key}.port`}
              label={t.site.port}
              type="number"
              min={1}
              max={65535}
              placeholder={site.root.port || "3000"}
              value={route.port}
              onChange={(e) => onChange({ port: e.target.value })}
              error={error("port")}
            />
          </div>
          <RouteToggles id={key} route={route} onChange={onChange} />
          <DirectiveList
            owner={key}
            title={t.site.directives}
            description={t.site.directivesText}
            items={route.options}
            onChange={(options) => onChange({ options })}
            problems={problems}
            placeholder="proxy_read_timeout 300s"
          />
        </FieldGroup>
      </CardContent>
    </Card>
  )
}
