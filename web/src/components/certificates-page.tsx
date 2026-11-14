import { useState } from "react"
import { BadgePlus, ShieldCheck, ShieldOff } from "lucide-react"

import { PageHeader } from "@/components/form"
import { IssueDialog } from "@/components/issue-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card } from "@/components/ui/card"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import type { Certificate, Status } from "@/lib/api"
import { formatDate } from "@/lib/format"
import type { Resource } from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"

export function CertificatesPage({
  certs,
  status,
}: {
  certs: Resource<Certificate[]>
  status: Status | null
}) {
  const { t, lang } = useI18n()
  const [issuing, setIssuing] = useState<{ key: number } | null>(null)

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t.certs.title}
        description={t.certs.description}
        actions={
          <Button
            disabled={!status?.certificates}
            onClick={() => setIssuing({ key: Date.now() })}
          >
            <BadgePlus />
            {t.certs.request}
          </Button>
        }
      />

      {certs.error ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ShieldOff />
            </EmptyMedia>
            <EmptyTitle>{t.certs.unavailable}</EmptyTitle>
            <EmptyDescription>{certs.error.message}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : certs.data === null ? (
        <div className="flex justify-center py-24">
          <Spinner />
        </div>
      ) : certs.data.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <ShieldCheck />
            </EmptyMedia>
            <EmptyTitle>{t.certs.emptyTitle}</EmptyTitle>
            <EmptyDescription>{t.certs.emptyText}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Card className="py-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">{t.certs.name}</TableHead>
                <TableHead>{t.certs.domains}</TableHead>
                <TableHead>{t.certs.key}</TableHead>
                <TableHead>{t.certs.ca}</TableHead>
                <TableHead>{t.certs.created}</TableHead>
                <TableHead className="pr-4">{t.certs.renews}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {certs.data.map((cert) => (
                <TableRow key={`${cert.name} ${cert.key_length}`}>
                  <TableCell className="pl-4 font-mono text-xs font-medium">
                    {cert.name}
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {cert.domains.map((d) => (
                        <Badge
                          key={d}
                          variant="secondary"
                          className="font-mono"
                        >
                          {d}
                        </Badge>
                      ))}
                    </div>
                  </TableCell>
                  <TableCell className="text-xs">{cert.key_length}</TableCell>
                  <TableCell className="text-xs">{cert.ca}</TableCell>
                  <TableCell className="text-xs">
                    {formatDate(cert.created, lang)}
                  </TableCell>
                  <TableCell className="pr-4 text-xs">
                    {formatDate(cert.renew, lang)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}

      {issuing && (
        <IssueDialog
          key={issuing.key}
          open
          onOpenChange={(o) => !o && setIssuing(null)}
          initialDomains={[]}
          status={status}
          onIssued={certs.reload}
        />
      )}
    </div>
  )
}
