import { useState } from "react"
import {
  CircleAlert,
  Download,
  FileUp,
  Globe,
  Lock,
  Pencil,
  Plus,
  RotateCw,
  Search,
  Trash2,
} from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton, IconButton, PageHeader } from "@/components/form"
import { ImportDialog } from "@/components/import-dialog"
import { SiteDialog } from "@/components/site-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
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
import type { Resource } from "@/lib/hooks"
import { describeProblem, useI18n } from "@/lib/i18n"
import {
  certificateOf,
  DEFAULT_HOST,
  domainsOf,
  fromStored,
  parseYaml,
  toServer,
  toYaml,
  type Site,
} from "@/lib/model"

type Editing = { key: number; stored: StoredSite | null; initial?: Site }

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
  const [query, setQuery] = useState("")
  const [editing, setEditing] = useState<Editing | null>(null)
  const [importing, setImporting] = useState<{ key: number } | null>(null)
  const [pending, setPending] = useState<Set<string>>(new Set())

  const edit = (stored: StoredSite | null, initial?: Site) =>
    setEditing({ key: Date.now(), stored, initial })

  const rows = (sites.data ?? []).map((stored) => ({
    stored,
    site: fromStored(stored),
  }))
  const q = query.trim().toLowerCase()
  const shown = q
    ? rows.filter(({ site }) =>
        [
          site.serverName,
          upstreamOf(site) ?? "",
          ...site.routes.map((r) => r.path),
        ]
          .join(" ")
          .toLowerCase()
          .includes(q)
      )
    : rows

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

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t.sites.title}
        description={t.sites.description}
        actions={
          <>
            <Button
              variant="outline"
              onClick={() => setImporting({ key: Date.now() })}
            >
              <FileUp />
              {t.sites.import}
            </Button>
            <Button
              variant="outline"
              disabled={rows.length === 0}
              onClick={() =>
                downloadText(
                  "config.yaml",
                  toYaml(rows.map(({ site }) => site))
                )
              }
            >
              <Download />
              {t.sites.export}
            </Button>
            <Button onClick={() => edit(null)}>
              <Plus />
              {t.sites.add}
            </Button>
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
        <Card className="gap-0 py-0">
          <div className="border-b p-3">
            <InputGroup className="max-w-sm">
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
          </div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">{t.sites.domains}</TableHead>
                <TableHead className="max-md:hidden">
                  {t.sites.upstream}
                </TableHead>
                <TableHead className="max-md:hidden">{t.sites.https}</TableHead>
                <TableHead>{t.sites.status}</TableHead>
                <TableHead className="pr-4 text-right">
                  <span className="sr-only">{t.sites.actions}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map(({ stored, site }) => {
                const domains = domainsOf(site)
                const name = nameOf(site, t.sites.newSite)
                return (
                  <TableRow
                    key={stored.id}
                    className="cursor-pointer"
                    onClick={() => edit(stored)}
                  >
                    <TableCell className="pl-4">
                      <div className="flex items-center gap-2 font-medium">
                        {site.https && (
                          <Lock
                            className="size-3.5 text-emerald-600 md:hidden dark:text-emerald-400"
                            aria-label={t.sites.https}
                          />
                        )}
                        {name}
                        {stored.problems && (
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <Badge variant="destructive">
                                {t.sites.invalid}
                              </Badge>
                            </TooltipTrigger>
                            <TooltipContent className="max-w-64">
                              {t.sites.invalidText}
                            </TooltipContent>
                          </Tooltip>
                        )}
                      </div>
                      {domains.length > 1 && (
                        <div className="text-xs text-muted-foreground">
                          {domains.slice(1).join(" ")}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="max-md:hidden">
                      {site.root.enabled ? (
                        <div className="font-mono text-xs">
                          {upstreamOf(site)}
                        </div>
                      ) : (
                        <div className="text-xs text-muted-foreground">
                          {t.sites.noMainRoute}
                        </div>
                      )}
                      {site.routes.length > 0 && (
                        <div className="text-xs text-muted-foreground">
                          {t.sites.routeCount(site.routes.length)}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="max-md:hidden">
                      {site.https ? (
                        <span className="flex items-center gap-1.5 font-mono text-xs">
                          <Lock className="size-3.5 text-emerald-600 dark:text-emerald-400" />
                          {certificateOf(site)}
                        </span>
                      ) : (
                        <span className="text-xs text-muted-foreground">
                          {t.sites.http}
                        </span>
                      )}
                    </TableCell>
                    <TableCell onClick={(e) => e.stopPropagation()}>
                      <label className="flex w-fit items-center gap-2 text-xs">
                        <Switch
                          checked={site.enabled}
                          disabled={pending.has(stored.id)}
                          onCheckedChange={(on) => toggle(stored, site, on)}
                        />
                        {site.enabled ? t.sites.enabled : t.sites.disabled}
                      </label>
                    </TableCell>
                    <TableCell
                      className="pr-4 text-right"
                      onClick={(e) => e.stopPropagation()}
                    >
                      <IconButton
                        label={t.sites.edit}
                        onClick={() => edit(stored)}
                      >
                        <Pencil />
                      </IconButton>
                      <ConfirmButton
                        title={t.sites.removeTitle(name)}
                        description={t.sites.removeText}
                        action={t.sites.removeAction}
                        onConfirm={() => remove(stored, site)}
                      >
                        <IconButton
                          label={t.sites.remove}
                          disabled={pending.has(stored.id)}
                        >
                          <Trash2 />
                        </IconButton>
                      </ConfirmButton>
                    </TableCell>
                  </TableRow>
                )
              })}
              {shown.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={5}
                    className="py-10 text-center text-muted-foreground"
                  >
                    {t.sites.noMatches}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </Card>
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

function nameOf(site: Site, fallback: string): string {
  return domainsOf(site)[0] ?? fallback
}

// upstreamOf describes where the main route of site goes, if it is on.
function upstreamOf(site: Site): string | undefined {
  if (!site.root.enabled) return undefined
  const host = site.root.host || DEFAULT_HOST
  const port = site.root.port || "?"
  return `http://${host.includes(":") ? `[${host}]` : host}:${port}`
}
