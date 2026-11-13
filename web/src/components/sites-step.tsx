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
import type { Certificates, Problems } from "@/lib/hooks"
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
        title="Sites"
        description="Each site is an nginx server block answering for one or more domains."
        actions={
          <>
            {sites.length > 0 && (
              <ConfirmButton
                title="Remove all sites?"
                description="This removes every site and its routes from the builder."
                action="Remove all"
                onConfirm={() => setSites([])}
              >
                <Button variant="ghost">Clear all</Button>
              </ConfirmButton>
            )}
            <Button onClick={add}>
              <Plus />
              Add site
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
            <EmptyTitle>No sites yet</EmptyTitle>
            <EmptyDescription>
              Add a site for each domain nginx should proxy, or import an
              existing config.yaml.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <div className="flex gap-2">
              <Button onClick={add}>
                <Plus />
                Add site
              </Button>
              <Button variant="outline" onClick={onImport}>
                <FileUp />
                Import YAML
              </Button>
            </div>
            <Button variant="link" size="sm" onClick={onLoadExample}>
              or start from the example
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
          <span className="truncate">{domains[0] ?? "New site"}</span>
          {domains.length > 1 && (
            <Badge variant="secondary">+{domains.length - 1}</Badge>
          )}
          <Badge variant="outline">
            {site.https ? "HTTPS" : "HTTP"} · {port}
          </Badge>
          {!site.enabled && <Badge variant="secondary">Disabled</Badge>}
        </CardTitle>
        <CardDescription>
          {routes} {routes === 1 ? "route" : "routes"}
        </CardDescription>
        <CardAction className="flex items-center gap-1">
          <Switch
            checked={site.enabled}
            onCheckedChange={(enabled) => onChange({ enabled })}
            aria-label="Generate this site"
            className="mr-2"
          />
          <IconButton label="Duplicate site" onClick={onDuplicate}>
            <CopyPlus />
          </IconButton>
          <ConfirmButton
            title={`Remove ${domains[0] ?? "this site"}?`}
            description="The site and all of its routes are removed from the builder."
            action="Remove"
            onConfirm={onRemove}
          >
            <IconButton label="Remove site">
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
              label="Domains"
              placeholder="example.com www.example.com"
              description="Separate several domains with spaces."
              value={site.serverName}
              onChange={(e) => onChange({ serverName: e.target.value })}
              error={error("server_name")}
            />
            <TextField
              id={`${key}.listen_port`}
              label="Listen port"
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
              title="HTTPS"
              description="Serve over TLS and redirect plain HTTP to it."
              checked={site.https}
              onCheckedChange={(https) => onChange({ https })}
            />
            <ToggleCard
              id={`${key}.gzip`}
              title="Gzip"
              description="Compress text responses such as HTML, CSS and JSON."
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
  error?: string
  onChange: (certName: string) => void
}) {
  const id = `${siteKey(site)}.cert_name`
  const domains = domainsOf(site)

  if (certs.status !== "ready") {
    return (
      <TextField
        id={id}
        label="Certificate"
        placeholder={domains[0] ?? "example.com"}
        value={site.certName}
        onChange={(e) => onChange(e.target.value)}
        error={error}
        description={
          certs.status === "loading"
            ? "Loading certificates from acme.sh…"
            : `The server could not list certificates (${certs.message}). Enter the name of an acme.sh certificate, or leave empty to use the first domain.`
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
  let problem = error
  if (!problem && cert && !known.has(cert)) {
    problem = `acme.sh has no certificate named ${cert}.`
  }

  return (
    <Field data-invalid={problem ? true : undefined}>
      <FieldLabel htmlFor={id}>Certificate</FieldLabel>
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
            Named after the first domain
            {domains[0] && <span className="font-mono">({domains[0]})</span>}
          </SelectItem>
          {names.length > 0 && <SelectSeparator />}
          {names.map((name) => (
            <SelectItem key={name} value={name}>
              <span className="font-mono">{name}</span>
              {!known.has(name) ? (
                <Badge variant="destructive">not found</Badge>
              ) : (
                covers(name) && <Badge variant="secondary">matches</Badge>
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
            ? `Warning: ${cert} does not cover ${uncovered.join(", ")}.`
            : "Certificates issued by acme.sh on the server."}
        </FieldDescription>
      )}
    </Field>
  )
}
