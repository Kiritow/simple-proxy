import { BadgePlus } from "lucide-react"

import { TextField, useDescribe } from "@/components/form"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldError,
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
import type { CertificateList, FieldProblem } from "@/lib/api"
import type { Resource } from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"
import {
  certificateOf,
  domainsOf,
  siteKey,
  uncovered,
  type Site,
} from "@/lib/model"

// Radix Select does not allow an empty value, which means "automatic" here.
const AUTO = "auto"

// CertificateField chooses the certificate of an HTTPS site.
export function CertificateField({
  site,
  certs,
  error,
  onChange,
  onRequest,
}: {
  site: Site
  certs: Resource<CertificateList>
  error?: FieldProblem
  onChange: (certName: string) => void
  onRequest?: () => void // offers to request a certificate
}) {
  const { t } = useI18n()
  const describe = useDescribe()
  const id = `${siteKey(site)}.cert_name`
  const domains = domainsOf(site)
  const request = onRequest && (
    <Button variant="outline" size="sm" className="w-fit" onClick={onRequest}>
      <BadgePlus />
      {t.site.requestCert}
    </Button>
  )

  if (certs.data === null) {
    return (
      <div className="flex flex-col gap-3">
        <TextField
          id={id}
          label={t.site.certificate}
          placeholder={domains[0] ?? "example.com"}
          value={site.certName}
          onChange={(e) => onChange(e.target.value)}
          error={error}
          description={
            certs.error
              ? t.site.certUnavailable(certs.error.message)
              : t.site.certLoading
          }
        />
        {certs.error === null && request}
      </div>
    )
  }

  const list = certs.data.certificates
  const known = new Set(list.map((c) => c.name))
  // The names a certificate is valid for; just its name if it is unknown.
  const namesOf = (cert: string) =>
    list.find((c) => c.name === cert)?.domains ?? [cert]
  const sourceOf = (cert: string) => list.find((c) => c.name === cert)?.source
  const covers = (cert: string) =>
    domains.length > 0 && uncovered(domains, namesOf(cert)).length === 0
  const names = [...known]
  if (site.certName && !known.has(site.certName)) names.unshift(site.certName)
  names.sort((a, b) => Number(covers(b)) - Number(covers(a)))

  const cert = certificateOf(site)
  const missing = uncovered(domains, namesOf(cert))
  let problem = describe(error)
  if (!problem && cert && !known.has(cert)) problem = t.site.certMissing(cert)

  return (
    <Field data-invalid={problem ? true : undefined}>
      <FieldLabel htmlFor={id}>{t.site.certificate}</FieldLabel>
      <div className="flex flex-wrap gap-2">
        <Select
          value={site.certName || AUTO}
          onValueChange={(v) => onChange(v === AUTO ? "" : v)}
        >
          <SelectTrigger
            id={id}
            className="min-w-0 flex-1"
            aria-invalid={problem ? true : undefined}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={AUTO}>
              {t.site.certAuto}
              {domains[0] && <span className="font-mono">({domains[0]})</span>}
            </SelectItem>
            {names.length > 0 && <SelectSeparator />}
            {names.map((name) => (
              <SelectItem key={name} value={name}>
                <span>{name}</span>
                {known.has(name) && (
                  <span className="text-xs text-muted-foreground">
                    {t.certs.sources[sourceOf(name) ?? "acme"]}
                  </span>
                )}
                {!known.has(name) ? (
                  <Badge variant="destructive">{t.site.certNotFound}</Badge>
                ) : (
                  covers(name) && (
                    <Badge variant="secondary">{t.site.certMatches}</Badge>
                  )
                )}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {request}
      </div>
      {problem ? (
        <FieldError>{problem}</FieldError>
      ) : (
        <FieldDescription>
          {missing.length > 0
            ? t.site.certUncovered(cert, missing.join(", "))
            : t.site.certHint}
        </FieldDescription>
      )}
    </Field>
  )
}
