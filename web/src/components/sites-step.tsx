import type { Dispatch, SetStateAction } from "react"
import { CopyPlus, FileUp, Globe, Plus, Trash2 } from "lucide-react"

import {
  ConfirmButton,
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
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import type { FieldProblem } from "@/lib/api"
import type { Certificates, Problems } from "@/lib/hooks"
import { describeProblem, useI18n } from "@/lib/i18n"
import {
  certCovers,
  certificateOf,
  cloneSite,
  domainsOf,
  newSite,
  siteKey,
  type Site,
} from "@/lib/model"

export function SitesStep({
  sites,
  setSites,
  problems,
  certs,
  focus,
  onImport,
  onLoadExample,
}: {
  sites: Site[]
  setSites: Dispatch<SetStateAction<Site[]>>
  problems: Problems
  certs: Certificates
  focus: (key: string) => void
  onImport: () => void
  onLoadExample: () => void
}) {
  const { t } = useI18n()
  const add = () => {
    const site = newSite()
    setSites((ss) => [...ss, site])
    focus(`${siteKey(site)}.server_name`)
  }
  const update = (id: string, patch: Partial<Site>) =>
    setSites((ss) => ss.map((s) => (s.id === id ? { ...s, ...patch } : s)))

  return (
    <div className="space-y-6">
      <StepHeader
        title={t.sites.title}
        description={t.sites.description}
        actions={
          <>
            {sites.length > 0 && (
              <ConfirmButton
                title={t.sites.clearAllTitle}
                description={t.sites.clearAllText}
                action={t.sites.clearAllAction}
                onConfirm={() => setSites([])}
              >
                <Button variant="ghost">{t.sites.clearAll}</Button>
              </ConfirmButton>
            )}
            <Button onClick={add}>
              <Plus />
              {t.sites.add}
            </Button>
          </>
        }
      />

      {sites.length === 0 ? (
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
              <Button onClick={add}>
                <Plus />
                {t.sites.add}
              </Button>
              <Button variant="outline" onClick={onImport}>
                <FileUp />
                {t.sites.importYaml}
              </Button>
            </div>
            <Button variant="link" size="sm" onClick={onLoadExample}>
              {t.sites.fromExample}
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        sites.map((site) => (
          <SiteCard
            key={site.id}
            site={site}
            problems={problems}
            certs={certs}
            onChange={(patch) => update(site.id, patch)}
            onDuplicate={() => {
              const copy = cloneSite(site)
              setSites((ss) =>
                ss.flatMap((s) => (s.id === site.id ? [s, copy] : [s]))
              )
              focus(`${siteKey(copy)}.server_name`)
            }}
            onRemove={() =>
              setSites((ss) => ss.filter((s) => s.id !== site.id))
            }
          />
        ))
      )}
    </div>
  )
}

function SiteCard({
  site,
  problems,
  certs,
  onChange,
  onDuplicate,
  onRemove,
}: {
  site: Site
  problems: Problems
  certs: Certificates
  onChange: (patch: Partial<Site>) => void
  onDuplicate: () => void
  onRemove: () => void
}) {
  const { t } = useI18n()
  const key = siteKey(site)
  const error = (field: string) => problems.byKey.get(`${key}.${field}`)
  const domains = domainsOf(site)
  const port = site.listenPort || (site.https ? "443" : "80")
  const routes = site.routes.length + 1

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex min-w-0 items-center gap-2">
          <Globe className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate">{domains[0] ?? t.sites.newSite}</span>
          {domains.length > 1 && (
            <Badge variant="secondary">+{domains.length - 1}</Badge>
          )}
          <Badge variant="outline">
            {site.https ? "HTTPS" : "HTTP"} · {port}
          </Badge>
          {!site.enabled && (
            <Badge variant="secondary">{t.sites.disabled}</Badge>
          )}
        </CardTitle>
        <CardDescription>{t.sites.routeCount(routes)}</CardDescription>
        <CardAction className="flex items-center gap-1">
          <Switch
            checked={site.enabled}
            onCheckedChange={(enabled) => onChange({ enabled })}
            aria-label={t.sites.enabled}
            className="mr-2"
          />
          <IconButton label={t.sites.duplicate} onClick={onDuplicate}>
            <CopyPlus />
          </IconButton>
          <ConfirmButton
            title={t.sites.removeTitle(domains[0] ?? t.sites.removeThis)}
            description={t.sites.removeText}
            action={t.sites.removeAction}
            onConfirm={onRemove}
          >
            <IconButton label={t.sites.remove}>
              <Trash2 />
            </IconButton>
          </ConfirmButton>
        </CardAction>
      </CardHeader>
      <CardContent>
        <FieldGroup>
          <div className="grid gap-4 sm:grid-cols-[1fr_9rem]">
            <TextField
              id={`${key}.server_name`}
              label={t.sites.domains}
              placeholder="example.com www.example.com"
              description={t.sites.domainsHint}
              value={site.serverName}
              onChange={(e) => onChange({ serverName: e.target.value })}
              error={error("server_name")}
            />
            <TextField
              id={`${key}.listen_port`}
              label={t.sites.listenPort}
              type="number"
              min={1}
              max={65535}
              placeholder={site.https ? "443" : "80"}
              value={site.listenPort}
              onChange={(e) => onChange({ listenPort: e.target.value })}
              error={error("listen_port")}
            />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <ToggleCard
              id={`${key}.https`}
              title={t.sites.https}
              description={t.sites.httpsText}
              checked={site.https}
              onCheckedChange={(https) => onChange({ https })}
            />
            <ToggleCard
              id={`${key}.gzip`}
              title={t.sites.gzip}
              description={t.sites.gzipText}
              checked={site.gzip}
              onCheckedChange={(gzip) => onChange({ gzip })}
            />
          </div>
          {site.https && (
            <CertificateField
              site={site}
              certs={certs}
              error={error("cert_name")}
              onChange={(certName) => onChange({ certName })}
            />
          )}
          <DirectiveList
            owner={key}
            items={site.options}
            onChange={(options) => onChange({ options })}
            problems={problems}
            placeholder="client_max_body_size 64m"
          />
        </FieldGroup>
      </CardContent>
    </Card>
  )
}

// Radix Select does not allow an empty value, which means "automatic" here.
const AUTO = "auto"

function CertificateField({
  site,
  certs,
  error,
  onChange,
}: {
  site: Site
  certs: Certificates
  error?: FieldProblem
  onChange: (certName: string) => void
}) {
  const { t } = useI18n()
  const id = `${siteKey(site)}.cert_name`
  const domains = domainsOf(site)

  if (certs.status !== "ready") {
    return (
      <TextField
        id={id}
        label={t.sites.certificate}
        placeholder={domains[0] ?? "example.com"}
        value={site.certName}
        onChange={(e) => onChange(e.target.value)}
        error={error}
        description={
          certs.status === "loading"
            ? t.sites.certLoading
            : t.sites.certUnavailable(certs.message)
        }
      />
    )
  }

  const known = new Set(certs.names)
  const covers = (cert: string) =>
    domains.length > 0 && domains.every((d) => certCovers(cert, d))
  const names = [...certs.names]
  if (site.certName && !known.has(site.certName)) names.unshift(site.certName)
  names.sort((a, b) => Number(covers(b)) - Number(covers(a)))

  const cert = certificateOf(site)
  const uncovered = domains.filter((d) => !certCovers(cert, d))
  let problem = error && describeProblem(t, error)
  if (!problem && cert && !known.has(cert)) {
    problem = t.sites.certMissing(cert)
  }

  return (
    <Field data-invalid={problem ? true : undefined}>
      <FieldLabel htmlFor={id}>{t.sites.certificate}</FieldLabel>
      <Select
        value={site.certName || AUTO}
        onValueChange={(v) => onChange(v === AUTO ? "" : v)}
      >
        <SelectTrigger
          id={id}
          className="w-full"
          aria-invalid={problem ? true : undefined}
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={AUTO}>
            {t.sites.certAuto}
            {domains[0] && <span className="font-mono">({domains[0]})</span>}
          </SelectItem>
          {names.length > 0 && <SelectSeparator />}
          {names.map((name) => (
            <SelectItem key={name} value={name}>
              <span className="font-mono">{name}</span>
              {!known.has(name) ? (
                <Badge variant="destructive">{t.sites.certNotFound}</Badge>
              ) : (
                covers(name) && (
                  <Badge variant="secondary">{t.sites.certMatches}</Badge>
                )
              )}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {problem ? (
        <FieldError>{problem}</FieldError>
      ) : (
        <FieldDescription>
          {uncovered.length > 0
            ? t.sites.certUncovered(cert, uncovered.join(", "))
            : t.sites.certHint}
        </FieldDescription>
      )}
    </Field>
  )
}
