import { useState, type MouseEvent } from "react"
import {
  ArrowDown,
  ArrowUp,
  ArrowUpDown,
  CircleAlert,
  ChevronDown,
  Download,
  FileCode,
  FileText,
  FileUp,
  Globe,
  LayoutGrid,
  List,
  Lock,
  Pencil,
  Plus,
  RotateCw,
  Search,
  Trash2,
} from "lucide-react"
import { toast } from "sonner"

import {
  ConfirmButton,
  IconButton,
  MenuItem,
  PageHeader,
} from "@/components/form"
import { ImportDialog } from "@/components/import-dialog"
import { SiteDialog } from "@/components/site-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from "@/components/ui/input-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  api,
  downloadText,
  errorOf,
  type CertificateList,
  type Status,
  type StoredSite,
} from "@/lib/api"
import { useAuth } from "@/lib/auth"
import { formatAgo, formatDateTime } from "@/lib/format"
import { useNow, useStoredState, type Resource } from "@/lib/hooks"
import { describeProblem, useI18n } from "@/lib/i18n"
import {
  certificatesOf,
  domainsOf,
  fromStored,
  matchesSearch,
  parseYaml,
  toServer,
  toYaml,
  upstreamOf,
  type Site,
} from "@/lib/model"
import {
  defaultSort,
  descending,
  sortBy,
  sortKey,
  sorts,
  toggleSort,
  type Sort,
  type SortKey,
} from "@/lib/sort"

type Editing = { key: number; stored: StoredSite | null; initial?: Site }

// The sites are listed in a table, or as cards.
const views = ["list", "cards"] as const
type View = (typeof views)[number]

// Row is a stored site as listed.
type Row = {
  stored: StoredSite
  site: Site
  name: string // the first domain, or what the site is without one
  domains: string[]
  certs: string[] // the certificates it serves
}

// stop keeps clicks on controls from opening the site, as clicking it
// elsewhere does. It also stops clicks in dialogs that the controls open,
// which React passes to the controls' parents.
const stop = (e: MouseEvent) => e.stopPropagation()

export function SitesPage({
  sites,
  status,
  certs,
  onChanged,
}: {
  sites: Resource<StoredSite[]>
  status: Status | null
  certs: Resource<CertificateList>
  onChanged: () => void // after sites changed
}) {
  const { t } = useI18n()
  // Site operators change only the sites given to them.
  const all = useAuth().me.permissions.all_sites
  const [query, setQuery] = useState("")
  const [view, setView] = useStoredState<View>(
    "simple-proxy:sites-view",
    "list",
    views
  )
  const [sort, setSort] = useStoredState<Sort>(
    "simple-proxy:sites-sort",
    defaultSort,
    sorts
  )
  const now = useNow(30_000) // for the times since sites changed
  const [editing, setEditing] = useState<Editing | null>(null)
  const [importing, setImporting] = useState<{ key: number } | null>(null)
  const [pending, setPending] = useState<Set<string>>(new Set())

  const edit = (stored: StoredSite | null, initial?: Site) =>
    setEditing({ key: Date.now(), stored, initial })

  const rows = (sites.data ?? []).map((stored): Row => {
    const site = fromStored(stored)
    return {
      stored,
      site,
      name: nameOf(site, site.raw !== null ? t.sites.raw : t.sites.newSite),
      domains: domainsOf(site),
      certs: certificatesOf(site),
    }
  })
  const matching = rows.filter(({ site }) => matchesSearch(site, query))
  const shown = sortBy(matching, sort, ({ stored, domains }) => ({
    name: domains[0],
    created: stored.created_at,
    updated: stored.updated_at,
  }))

  const busy = (id: string, on: boolean) =>
    setPending((p) => {
      const next = new Set(p)
      if (on) next.add(id)
      else next.delete(id)
      return next
    })

  const change = async (
    stored: StoredSite,
    action: () => Promise<unknown>,
    done: string
  ) => {
    busy(stored.id, true)
    try {
      await action()
      toast.success(done)
    } catch (err) {
      const e = errorOf(err)
      if (e.status === 409) {
        toast.error(t.sites.conflict)
        busy(stored.id, false)
        onChanged()
        return
      }
      toast.error(t.sites.toggleFailed, {
        description:
          e.fields.length > 0
            ? e.fields.map((f) => describeProblem(t, f)).join(" ")
            : e.message,
      })
    }
    busy(stored.id, false)
    onChanged()
  }

  const toggle = (stored: StoredSite, site: Site, enabled: boolean) =>
    change(
      stored,
      () =>
        api.updateSite(
          stored.id,
          toServer({ ...site, enabled }),
          stored.generation
        ),
      status?.applies
        ? t.sites.saved(nameOf(site, t.sites.newSite))
        : t.sites.savedOnly(nameOf(site, t.sites.newSite))
    )

  const remove = (stored: StoredSite, site: Site) =>
    change(
      stored,
      () => api.deleteSite(stored.id, stored.generation),
      t.sites.removed(nameOf(site, t.sites.newSite))
    )

  const fromExample = async () => {
    const [site] = parseYaml(await api.example())
    edit(null, site)
  }

  // exportNginx downloads the configuration generated from the stored
  // sites, as nginx gets it.
  const exportNginx = async () => {
    try {
      downloadText("proxy.conf", await api.nginxConf())
    } catch (err) {
      toast.error(t.sites.exportFailed, {
        description: errorOf(err).message,
      })
    }
  }

  const noMatches = (
    <span className="text-muted-foreground">{t.sites.noMatches}</span>
  )

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t.sites.title}
        description={t.sites.description}
        actions={
          <>
            {all && (
              <Button
                variant="outline"
                onClick={() => setImporting({ key: Date.now() })}
              >
                <FileUp />
                {t.sites.import}
              </Button>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" disabled={rows.length === 0}>
                  <Download />
                  {t.sites.export}
                  <ChevronDown />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-72">
                <MenuItem
                  icon={<FileText />}
                  title="config.yaml"
                  text={t.sites.exportYamlText}
                  onSelect={() =>
                    downloadText(
                      "config.yaml",
                      toYaml(rows.map(({ site }) => site))
                    )
                  }
                />
                {all && (
                  <MenuItem
                    icon={<FileCode />}
                    title={t.sites.exportNginx}
                    text={t.sites.exportNginxText}
                    onSelect={exportNginx}
                  />
                )}
              </DropdownMenuContent>
            </DropdownMenu>
            {all && (
              <Button onClick={() => edit(null)}>
                <Plus />
                {t.sites.add}
              </Button>
            )}
          </>
        }
      />

      {sites.data === null ? (
        sites.error ? (
          <Empty className="border border-dashed">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <CircleAlert />
              </EmptyMedia>
              <EmptyTitle>{t.sites.loadFailed}</EmptyTitle>
              <EmptyDescription>{sites.error.message}</EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button variant="outline" onClick={sites.reload}>
                <RotateCw />
                {t.app.tryAgain}
              </Button>
            </EmptyContent>
          </Empty>
        ) : (
          <div className="flex justify-center py-24">
            <Spinner />
          </div>
        )
      ) : rows.length === 0 && !all ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Globe />
            </EmptyMedia>
            <EmptyTitle>{t.sites.noneGivenTitle}</EmptyTitle>
            <EmptyDescription>{t.sites.noneGivenText}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : rows.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Globe />
            </EmptyMedia>
            <EmptyTitle>{t.sites.emptyTitle}</EmptyTitle>
            <EmptyDescription>{t.sites.emptyText}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <div className="flex gap-2">
              <Button onClick={() => edit(null)}>
                <Plus />
                {t.sites.add}
              </Button>
              <Button
                variant="outline"
                onClick={() => setImporting({ key: Date.now() })}
              >
                <FileUp />
                {t.sites.import}
              </Button>
            </div>
            <Button variant="link" size="sm" onClick={fromExample}>
              {t.sites.fromExample}
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <div className="flex flex-col gap-4">
          <div className="flex flex-wrap items-center gap-2">
            <InputGroup className="max-w-sm min-w-48 flex-1">
              <InputGroupAddon>
                <Search />
              </InputGroupAddon>
              <InputGroupInput
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t.sites.search}
                aria-label={t.sites.search}
              />
            </InputGroup>
            <div className="ml-auto flex items-center gap-2">
              <Select value={sort} onValueChange={(v) => setSort(v as Sort)}>
                <SelectTrigger aria-label={t.sites.sort}>
                  <ArrowUpDown className="text-muted-foreground" />
                  <SelectValue />
                </SelectTrigger>
                <SelectContent position="popper" align="end">
                  {sorts.map((s) => (
                    <SelectItem key={s} value={s}>
                      {t.sites.sorts[s]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <div
                role="group"
                aria-label={t.sites.view}
                className="inline-flex rounded-lg border p-px"
              >
                <IconButton
                  label={t.sites.viewList}
                  variant={view === "list" ? "secondary" : "ghost"}
                  aria-pressed={view === "list"}
                  onClick={() => setView("list")}
                >
                  <List />
                </IconButton>
                <IconButton
                  label={t.sites.viewCards}
                  variant={view === "cards" ? "secondary" : "ghost"}
                  aria-pressed={view === "cards"}
                  onClick={() => setView("cards")}
                >
                  <LayoutGrid />
                </IconButton>
              </div>
            </div>
          </div>

          {view === "list" ? (
            <Card className="gap-0 py-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <SortableHead
                      label={t.sites.domains}
                      by="name"
                      sort={sort}
                      onSort={setSort}
                      className="pl-4"
                    />
                    <TableHead className="max-md:hidden">
                      {t.sites.upstream}
                    </TableHead>
                    <TableHead className="max-md:hidden">
                      {t.sites.https}
                    </TableHead>
                    <TableHead>{t.sites.status}</TableHead>
                    <SortableHead
                      label={t.sites.modified}
                      by="updated"
                      sort={sort}
                      onSort={setSort}
                      className="max-sm:hidden"
                    />
                    <TableHead className="pr-4 text-right">
                      <span className="sr-only">{t.sites.actions}</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.map((row) => (
                    <TableRow
                      key={row.stored.id}
                      className="cursor-pointer"
                      onClick={() => edit(row.stored)}
                    >
                      <TableCell className="pl-4">
                        <div className="flex items-center gap-2 font-medium">
                          {row.certs.length > 0 && (
                            <Lock
                              className="size-3.5 text-emerald-600 md:hidden dark:text-emerald-400"
                              aria-label={t.sites.https}
                            />
                          )}
                          {row.name}
                          {row.stored.problems && <InvalidBadge />}
                        </div>
                        {row.domains.length > 1 && (
                          <div className="text-xs text-muted-foreground">
                            {row.domains.slice(1).join(" ")}
                          </div>
                        )}
                      </TableCell>
                      <TableCell className="max-md:hidden">
                        <Upstream site={row.site} />
                      </TableCell>
                      <TableCell className="max-md:hidden">
                        <Certificates certs={row.certs} />
                      </TableCell>
                      <TableCell>
                        <EnabledSwitch
                          enabled={row.site.enabled}
                          busy={pending.has(row.stored.id)}
                          onChange={(on) => toggle(row.stored, row.site, on)}
                        />
                      </TableCell>
                      <TableCell className="max-sm:hidden">
                        <Modified stored={row.stored} now={now} />
                      </TableCell>
                      <TableCell className="pr-4">
                        <SiteActions
                          name={row.name}
                          busy={pending.has(row.stored.id)}
                          onEdit={() => edit(row.stored)}
                          onRemove={
                            all ? () => remove(row.stored, row.site) : undefined
                          }
                        />
                      </TableCell>
                    </TableRow>
                  ))}
                  {shown.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={6} className="py-10 text-center">
                        {noMatches}
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </Card>
          ) : shown.length === 0 ? (
            <p className="rounded-xl border border-dashed py-10 text-center">
              {noMatches}
            </p>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {shown.map((row) => (
                <Card
                  key={row.stored.id}
                  className="cursor-pointer gap-3 transition-shadow hover:ring-foreground/25"
                  onClick={() => edit(row.stored)}
                >
                  <CardHeader>
                    <CardTitle className="flex min-w-0 items-center gap-2">
                      {row.certs.length > 0 ? (
                        <Lock
                          className="size-4 shrink-0 text-emerald-600 dark:text-emerald-400"
                          aria-label={t.sites.https}
                        />
                      ) : (
                        <Globe
                          className="size-4 shrink-0 text-muted-foreground"
                          aria-label={t.sites.http}
                        />
                      )}
                      <span className="truncate">{row.name}</span>
                      {row.stored.problems && <InvalidBadge />}
                    </CardTitle>
                    {row.domains.length > 1 && (
                      <CardDescription className="truncate text-xs">
                        {row.domains.slice(1).join(" ")}
                      </CardDescription>
                    )}
                    <CardAction className="-mt-1 -mr-2">
                      <SiteActions
                        name={row.name}
                        busy={pending.has(row.stored.id)}
                        onEdit={() => edit(row.stored)}
                        onRemove={
                          all ? () => remove(row.stored, row.site) : undefined
                        }
                      />
                    </CardAction>
                  </CardHeader>
                  <CardContent className="flex flex-1 flex-col gap-1.5">
                    <Upstream site={row.site} />
                    <Certificates certs={row.certs} />
                  </CardContent>
                  <CardFooter className="justify-between gap-2 py-2.5">
                    <EnabledSwitch
                      enabled={row.site.enabled}
                      busy={pending.has(row.stored.id)}
                      onChange={(on) => toggle(row.stored, row.site, on)}
                    />
                    <Modified stored={row.stored} now={now} labeled />
                  </CardFooter>
                </Card>
              ))}
            </div>
          )}
        </div>
      )}

      {editing && (
        <SiteDialog
          key={editing.key}
          open
          onOpenChange={(o) => !o && setEditing(null)}
          stored={editing.stored}
          initial={editing.initial}
          certs={certs}
          status={status}
          onSaved={onChanged}
        />
      )}
      {importing && (
        <ImportDialog
          key={importing.key}
          open
          onOpenChange={(o) => !o && setImporting(null)}
          onImport={async (imported) => {
            await api.replaceSites(imported.map(toServer))
            toast.success(t.sites.imported(imported.length))
            onChanged()
          }}
        />
      )}
    </div>
  )
}

// SortableHead is the header of a column that sorts the sites by clicking
// it, and shows whether they are.
function SortableHead({
  label,
  by,
  sort,
  onSort,
  className,
}: {
  label: string
  by: SortKey
  sort: Sort
  onSort: (sort: Sort) => void
  className?: string
}) {
  const active = sortKey(sort) === by
  const desc = descending(sort)
  const Icon = !active ? ArrowUpDown : desc ? ArrowDown : ArrowUp
  return (
    <TableHead
      className={className}
      aria-sort={active ? (desc ? "descending" : "ascending") : undefined}
    >
      <button
        type="button"
        className="-mx-1 inline-flex items-center gap-1 rounded-md px-1 py-0.5 outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        onClick={() => onSort(toggleSort(sort, by))}
      >
        {label}
        <Icon
          className={active ? "size-3.5" : "size-3.5 text-muted-foreground/60"}
        />
      </button>
    </TableHead>
  )
}

// EnabledSwitch turns a site on or off.
function EnabledSwitch({
  enabled,
  busy,
  onChange,
}: {
  enabled: boolean
  busy: boolean // while the site changes
  onChange: (enabled: boolean) => void
}) {
  const { t } = useI18n()
  return (
    <label className="flex w-fit items-center gap-2 text-xs" onClick={stop}>
      <Switch checked={enabled} disabled={busy} onCheckedChange={onChange} />
      {enabled ? t.sites.enabled : t.sites.disabled}
    </label>
  )
}

// SiteActions edits or deletes a site, once confirmed, if onRemove is
// given.
function SiteActions({
  name,
  busy,
  onEdit,
  onRemove,
}: {
  name: string
  busy: boolean // while the site changes
  onEdit: () => void
  onRemove?: () => void
}) {
  const { t } = useI18n()
  return (
    <div className="flex justify-end" onClick={stop}>
      <IconButton label={t.sites.edit} onClick={onEdit}>
        <Pencil />
      </IconButton>
      {onRemove && (
        <ConfirmButton
          title={t.sites.removeTitle(name)}
          description={t.sites.removeText}
          action={t.sites.removeAction}
          onConfirm={onRemove}
        >
          <IconButton label={t.sites.remove} disabled={busy}>
            <Trash2 />
          </IconButton>
        </ConfirmButton>
      )}
    </div>
  )
}

// InvalidBadge marks a site stored by an earlier version that is invalid.
function InvalidBadge() {
  const { t } = useI18n()
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge variant="destructive">{t.sites.invalid}</Badge>
      </TooltipTrigger>
      <TooltipContent className="max-w-64">
        {t.sites.invalidText}
      </TooltipContent>
    </Tooltip>
  )
}

// Upstream describes where a site forwards requests.
function Upstream({ site }: { site: Site }) {
  const { t } = useI18n()
  return (
    <div>
      {site.raw !== null ? (
        <Badge variant="outline">{t.sites.raw}</Badge>
      ) : site.root.enabled ? (
        <div className="font-mono text-xs">{upstreamOf(site)}</div>
      ) : (
        <div className="text-xs text-muted-foreground">
          {t.sites.noMainRoute}
        </div>
      )}
      {site.raw === null && site.routes.length > 0 && (
        <div className="text-xs text-muted-foreground">
          {t.sites.routeCount(site.routes.length)}
        </div>
      )}
    </div>
  )
}

// Certificates lists the certificates a site serves, if any.
function Certificates({ certs }: { certs: string[] }) {
  const { t } = useI18n()
  return certs.length > 0 ? (
    <span className="flex items-center gap-1.5 text-xs">
      <Lock className="size-3.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
      <span className="truncate">{certs.join(", ")}</span>
    </span>
  ) : (
    <span className="text-xs text-muted-foreground">{t.sites.http}</span>
  )
}

// Modified tells how long ago a site last changed, and when exactly, and
// when it was added, on hover.
function Modified({
  stored,
  now,
  labeled,
}: {
  stored: StoredSite
  now: number
  labeled?: boolean // whether to say what the time is
}) {
  const { t, lang } = useI18n()
  const updated = stored.updated_at
  if (!updated) return null
  const ago = formatAgo(updated, lang, now)
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <time
          dateTime={updated}
          className="text-xs whitespace-nowrap text-muted-foreground"
        >
          {labeled ? t.sites.modifiedAt(ago) : ago}
        </time>
      </TooltipTrigger>
      <TooltipContent className="flex-col items-start gap-0.5 whitespace-nowrap">
        <div>{t.sites.modifiedAt(formatDateTime(updated, lang))}</div>
        {stored.created_at && (
          <div>{t.sites.addedAt(formatDateTime(stored.created_at, lang))}</div>
        )}
      </TooltipContent>
    </Tooltip>
  )
}

function nameOf(site: Site, fallback: string): string {
  return domainsOf(site)[0] ?? fallback
}
