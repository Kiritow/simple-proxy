import type { Dispatch, SetStateAction } from "react"
import {
  ArrowLeft,
  ArrowRight,
  House,
  Plus,
  Signpost,
  Trash2,
} from "lucide-react"
import { cn } from "cn"

import {
  DirectiveList,
  IconButton,
  StepHeader,
  TextField,
  ToggleCard,
} from "@/components/form"
import { Badge } from "@/components/ui/badge"
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
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { FieldGroup } from "@/components/ui/field"
import { Switch } from "@/components/ui/switch"
import type { Problems } from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"
import {
  DEFAULT_HOST,
  domainsOf,
  newRoute,
  routeKey,
  siteKey,
  type Route,
  type Site,
} from "@/lib/model"

export function RoutesStep({
  sites,
  setSites,
  selectedId,
  onSelect,
  problems,
  focus,
  onBack,
}: {
  sites: Site[]
  setSites: Dispatch<SetStateAction<Site[]>>
  selectedId: string
  onSelect: (id: string) => void
  problems: Problems
  focus: (key: string) => void
  onBack: () => void
}) {
  const { t } = useI18n()
  if (sites.length === 0) {
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <Signpost />
          </EmptyMedia>
          <EmptyTitle>{t.routes.noSitesTitle}</EmptyTitle>
          <EmptyDescription>{t.routes.noSitesText}</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button variant="outline" onClick={onBack}>
            <ArrowLeft />
            {t.routes.backToSites}
          </Button>
        </EmptyContent>
      </Empty>
    )
  }

  const site = sites.find((s) => s.id === selectedId) ?? sites[0]
  const updateSite = (f: (s: Site) => Site) =>
    setSites((ss) => ss.map((s) => (s.id === site.id ? f(s) : s)))
  const updateRoute = (id: string, patch: Partial<Route>) =>
    updateSite((s) =>
      s.root.id === id
        ? { ...s, root: { ...s.root, ...patch } }
        : {
            ...s,
            routes: s.routes.map((r) => (r.id === id ? { ...r, ...patch } : r)),
          }
    )
  const addRoute = () => {
    const route = newRoute()
    updateSite((s) => ({ ...s, routes: [...s.routes, route] }))
    focus(`${routeKey(site, route)}.path`)
  }

  return (
    <div className="grid gap-6 md:grid-cols-[13rem_minmax(0,1fr)]">
      <nav aria-label={t.routes.sitesNav} className="flex flex-col gap-1">
        <p className="px-2 pb-1 text-xs font-medium text-muted-foreground">
          {t.routes.sitesNav}
        </p>
        {sites.map((s) => {
          const prefix = `${siteKey(s)}/r:`
          const count = [...problems.byKey.keys()].filter((k) =>
            k.startsWith(prefix)
          ).length
          return (
            <button
              key={s.id}
              type="button"
              onClick={() => onSelect(s.id)}
              aria-current={s.id === site.id ? "page" : undefined}
              className={cn(
                "flex items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50",
                s.id === site.id && "bg-muted font-medium",
                !s.enabled && "text-muted-foreground"
              )}
            >
              <span className="truncate">
                {domainsOf(s)[0] || t.sites.newSite}
              </span>
              {count > 0 && (
                <span className="size-1.5 shrink-0 rounded-full bg-destructive" />
              )}
              <span className="ml-auto text-xs text-muted-foreground tabular-nums">
                {s.routes.length + 1}
              </span>
            </button>
          )
        })}
      </nav>

      <div className="flex min-w-0 flex-col gap-4">
        <StepHeader
          title={domainsOf(site).join(" ") || t.sites.newSite}
          description={t.routes.description}
          actions={
            <Button onClick={addRoute}>
              <Plus />
              {t.routes.add}
            </Button>
          }
        />
        <RouteCard
          site={site}
          route={site.root}
          problems={problems}
          onChange={(patch) => updateRoute(site.root.id, patch)}
        />
        {site.routes.map((route) => (
          <RouteCard
            key={route.id}
            site={site}
            route={route}
            problems={problems}
            onChange={(patch) => updateRoute(route.id, patch)}
            onRemove={() =>
              updateSite((s) => ({
                ...s,
                routes: s.routes.filter((r) => r.id !== route.id),
              }))
            }
          />
        ))}
        {site.routes.length === 0 && (
          <p className="text-center text-sm text-muted-foreground">
            {t.routes.emptyHint}
          </p>
        )}
      </div>
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
  onRemove?: () => void // undefined for the default route
}) {
  const { t } = useI18n()
  const isDefault = route === site.root
  const key = routeKey(site, route)
  const error = (field: string) => problems.byKey.get(`${key}.${field}`)
  const inheritedHost = isDefault
    ? DEFAULT_HOST
    : site.root.host || DEFAULT_HOST
  const inheritedPort = isDefault ? "" : site.root.port
  const host = route.host || inheritedHost
  const port = route.port || inheritedPort

  return (
    <Card className={cn(!route.enabled && "bg-muted/30")}>
      <CardHeader>
        <CardTitle className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          {isDefault ? (
            <House className="size-4 text-muted-foreground" />
          ) : (
            <Signpost className="size-4 text-muted-foreground" />
          )}
          <span className="font-mono">
            {route.path || (isDefault ? "/" : t.routes.newRoute)}
          </span>
          <ArrowRight className="size-3.5 text-muted-foreground" />
          <span className="font-mono text-muted-foreground">
            http://{host.includes(":") ? `[${host}]` : host}:{port || "?"}
          </span>
          {isDefault && (
            <Badge variant="secondary">{t.routes.defaultBadge}</Badge>
          )}
          {!route.enabled && (
            <Badge variant="outline">{t.routes.disabledBadge}</Badge>
          )}
        </CardTitle>
        <CardDescription>
          {isDefault ? t.routes.defaultText : t.routes.routeText}
        </CardDescription>
        <CardAction className="flex items-center gap-1">
          <Switch
            checked={route.enabled}
            onCheckedChange={(enabled) => onChange({ enabled })}
            aria-label={t.routes.enabled}
            className="mr-2"
          />
          {onRemove && (
            <IconButton label={t.routes.remove} onClick={onRemove}>
              <Trash2 />
            </IconButton>
          )}
        </CardAction>
      </CardHeader>
      <CardContent>
        <FieldGroup>
          <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_8rem]">
            <TextField
              id={`${key}.path`}
              label={t.routes.path}
              placeholder={isDefault ? "/" : "/api"}
              className="[&_input]:font-mono"
              value={route.path}
              onChange={(e) => onChange({ path: e.target.value })}
              error={error("path")}
            />
            <TextField
              id={`${key}.ip`}
              label={t.routes.host}
              placeholder={inheritedHost}
              value={route.host}
              onChange={(e) => onChange({ host: e.target.value })}
              error={error("ip")}
            />
            <TextField
              id={`${key}.port`}
              label={t.routes.port}
              type="number"
              min={1}
              max={65535}
              placeholder={inheritedPort || "3000"}
              value={route.port}
              onChange={(e) => onChange({ port: e.target.value })}
              error={error("port")}
            />
          </div>
          <div className="grid gap-3 lg:grid-cols-3">
            <ToggleCard
              id={`${key}.websocket`}
              title={t.routes.websocket}
              description={t.routes.websocketText}
              checked={route.websocket}
              onCheckedChange={(websocket) => onChange({ websocket })}
            />
            <ToggleCard
              id={`${key}.realtime`}
              title={t.routes.realtime}
              description={t.routes.realtimeText}
              checked={route.realtime}
              onCheckedChange={(realtime) => onChange({ realtime })}
            />
            <ToggleCard
              id={`${key}.disable_cache`}
              title={t.routes.noCache}
              description={t.routes.noCacheText}
              checked={route.disableCache}
              onCheckedChange={(disableCache) => onChange({ disableCache })}
            />
          </div>
          <DirectiveList
            owner={key}
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
