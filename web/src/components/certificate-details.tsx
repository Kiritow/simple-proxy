import { useState, type ReactNode } from "react"
import { CircleCheck, KeyRound, TriangleAlert } from "lucide-react"
import { cn } from "cn"

import { Badge } from "@/components/ui/badge"
import type { CertificatePreview } from "@/lib/api"
import { formatDate } from "@/lib/format"
import { useI18n } from "@/lib/i18n"

// Certificates expiring within this many days are highlighted.
const SOON_DAYS = 30

// CertificateDetails shows what an upload or a signing request would store,
// as previewed by the server: for a certificate, a CA, or a certificate to
// sign.
export function CertificateDetails({
  preview,
  kind,
}: {
  preview: CertificatePreview | null
  kind: "certificate" | "authority" | "sign"
}) {
  const { t, lang } = useI18n()
  const empty =
    !preview ||
    (kind === "sign" ? preview.domains.length === 0 : !preview.subject)

  return (
    <section className="flex flex-col gap-3 rounded-lg border bg-muted/30 p-4 text-sm">
      <h3 className="font-medium">{t.details.title}</h3>
      {empty ? (
        <p className="text-muted-foreground">
          {kind === "sign" ? t.details.emptySign : t.details.emptyUpload}
        </p>
      ) : (
        <dl className="grid grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-4 gap-y-2.5">
          <Row term={t.details.name}>
            {preview.name || <span className="text-muted-foreground">—</span>}
          </Row>
          {kind === "authority" ? (
            <Row term={t.details.subject}>{preview.subject}</Row>
          ) : (
            <Row term={t.details.domains}>
              <div className="flex flex-wrap gap-1">
                {preview.domains.map((d) => (
                  <Badge key={d} variant="secondary" className="font-mono">
                    {d}
                  </Badge>
                ))}
              </div>
            </Row>
          )}
          <Row term={t.details.issuer}>
            {preview.issuer === "" ? (
              <span className="text-muted-foreground">—</span>
            ) : preview.issuer === preview.subject ? (
              t.details.selfSigned
            ) : (
              preview.issuer
            )}
          </Row>
          {preview.not_before && preview.not_after && (
            <Row term={t.details.validity}>
              <span className="flex flex-wrap items-center gap-x-1.5">
                {formatDate(preview.not_before, lang)} –
                <Expiry date={preview.not_after} className="text-sm" />
              </span>
            </Row>
          )}
          {preview.key && (
            <Row term={t.details.key}>
              <div className="flex flex-col gap-1">
                <span>{preview.key}</span>
                <KeyStatus kind={kind} hasKey={preview.has_key} />
              </div>
            </Row>
          )}
          <Row term={t.details.chain}>
            {preview.chain.length === 0 ? (
              <span className="text-muted-foreground">
                {t.details.chainEmpty}
              </span>
            ) : (
              <ol className="flex flex-col gap-1">
                {preview.chain.map((c, i) => (
                  <li key={i} className="flex flex-col">
                    <span>{c.subject}</span>
                    <span className="text-xs text-muted-foreground">
                      {t.details.until(formatDate(c.not_after, lang))}
                      {c.root &&
                        ` · ${kind === "authority" ? t.details.root : t.details.rootNotServed}`}
                    </span>
                  </li>
                ))}
              </ol>
            )}
          </Row>
        </dl>
      )}
    </section>
  )
}

function Row({ term, children }: { term: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{term}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </>
  )
}

function KeyStatus({
  kind,
  hasKey,
}: {
  kind: "certificate" | "authority" | "sign"
  hasKey: boolean
}) {
  const { t } = useI18n()
  if (kind === "sign") {
    return (
      <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <KeyRound className="size-3.5" />
        {t.details.keyNew}
      </span>
    )
  }
  return hasKey ? (
    <span className="flex items-center gap-1.5 text-xs text-emerald-700 dark:text-emerald-400">
      <CircleCheck className="size-3.5" />
      {t.details.keyFound}
    </span>
  ) : (
    <span className="flex items-center gap-1.5 text-xs text-amber-700 dark:text-amber-400">
      <TriangleAlert className="size-3.5" />
      {t.details.keyMissing}
    </span>
  )
}

// Expiry shows when a certificate expires, highlighting it if that is soon
// or past.
export function Expiry({
  date,
  className,
}: {
  date: string
  className?: string
}) {
  const { t, lang } = useI18n()
  const [now] = useState(Date.now) // when shown; pages are not left open for days
  const days = (new Date(date).getTime() - now) / 86_400_000
  if (days < 0) {
    return (
      <span
        className={cn("inline-flex items-center gap-1.5 text-xs", className)}
      >
        {formatDate(date, lang)}
        <Badge variant="destructive">{t.certs.expired}</Badge>
      </span>
    )
  }
  return (
    <span
      className={cn(
        "text-xs",
        days < SOON_DAYS && "font-medium text-amber-700 dark:text-amber-400",
        className
      )}
    >
      {formatDate(date, lang)}
    </span>
  )
}
