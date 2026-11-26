import { useState, type ReactNode } from "react"
import { CircleAlert, History, RotateCw, Trash2 } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton, PageHeader } from "@/components/form"
import { VersionsChart } from "@/components/versions-chart"
import { Button } from "@/components/ui/button"
import {
  Card,
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
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { api, errorOf, type VersionStats } from "@/lib/api"
import { formatAgo, formatDate, formatDateTime } from "@/lib/format"
import {
  bucketsOf,
  daysAgo,
  defaultGrouping,
  formatBucket,
  formatBytes,
  maxDays,
  purgeableBefore,
  spanDays,
  startOfDay,
  type Grouping,
} from "@/lib/history"
import { useVersionStats } from "@/lib/hooks"
import { describeError, useI18n } from "@/lib/i18n"

// HistoryPage shows super administrators how many versions of the sites are
// kept, by the day they were created, and clears old ones.
export function HistoryPage() {
  const { t } = useI18n()
  const stats = useVersionStats()

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title={t.history.title} description={t.history.description} />
      {stats.data === null ? (
        stats.error ? (
          <Empty className="border border-dashed">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <CircleAlert />
              </EmptyMedia>
              <EmptyTitle>{t.history.loadFailed}</EmptyTitle>
              <EmptyDescription>
                {describeError(t, stats.error)}
              </EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button variant="outline" onClick={stats.reload}>
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
      ) : stats.data.versions === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <History />
            </EmptyMedia>
            <EmptyTitle>{t.history.emptyTitle}</EmptyTitle>
            <EmptyDescription>{t.history.emptyText}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Versions stats={stats.data} onCleared={stats.reload} />
      )}
    </div>
  )
}

function Versions({
  stats,
  onCleared,
}: {
  stats: VersionStats
  onCleared: () => void
}) {
  const { t, lang } = useI18n()
  const [grouping, setGrouping] = useState<Grouping>(() =>
    defaultGrouping(stats.days)
  )
  const tooLong = spanDays(stats.days) > maxDays
  const shownGrouping = tooLong ? "month" : grouping
  const buckets = bucketsOf(stats.days, shownGrouping)

  return (
    <>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatTile
          label={t.history.versions}
          value={stats.versions.toLocaleString(lang)}
          detail={t.history.sitesCount(stats.sites)}
        />
        <StatTile
          label={t.history.purgeable}
          value={stats.purgeable.toLocaleString(lang)}
          detail={t.history.purgeableText}
        />
        <StatTile
          label={t.history.size}
          value={formatBytes(stats.bytes, lang)}
          detail={t.history.sizeText}
        />
        <StatTile
          label={t.history.since}
          value={stats.oldest ? formatDate(stats.oldest, lang) : "—"}
          detail={
            stats.newest && (
              <time
                dateTime={stats.newest}
                title={formatDateTime(stats.newest, lang)}
              >
                {t.history.newest(formatAgo(stats.newest, lang))}
              </time>
            )
          }
        />
      </div>

      <ClearCard stats={stats} onCleared={onCleared} />

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm text-muted-foreground">
          {t.history.grouping}
        </span>
        <div
          role="group"
          aria-label={t.history.grouping}
          className="inline-flex gap-0.5 rounded-lg border p-0.5"
        >
          {(["day", "month"] as const).map((g) => (
            <Button
              key={g}
              size="sm"
              variant={shownGrouping === g ? "secondary" : "ghost"}
              aria-pressed={shownGrouping === g}
              disabled={g === "day" && tooLong}
              title={g === "day" && tooLong ? t.history.tooManyDays : undefined}
              onClick={() => setGrouping(g)}
            >
              {g === "day" ? t.history.byDay : t.history.byMonth}
            </Button>
          ))}
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>
            {shownGrouping === "day" ? t.history.perDay : t.history.perMonth}
          </CardTitle>
          <CardDescription>{t.history.chartText}</CardDescription>
        </CardHeader>
        <CardContent>
          <VersionsChart buckets={buckets} grouping={shownGrouping} />
        </CardContent>
      </Card>

      <Card className="gap-0 py-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="pl-4">
                {shownGrouping === "day" ? t.history.date : t.history.month}
              </TableHead>
              <TableHead className="text-right">{t.history.versions}</TableHead>
              <TableHead className="text-right max-md:hidden">
                {t.history.created}
              </TableHead>
              <TableHead className="text-right max-md:hidden">
                {t.history.updated}
              </TableHead>
              <TableHead className="text-right max-md:hidden">
                {t.history.deleted}
              </TableHead>
              <TableHead
                className="text-right max-md:hidden"
                title={t.history.initialText}
              >
                {t.history.initial}
              </TableHead>
              <TableHead className="text-right max-sm:hidden">
                {t.history.size}
              </TableHead>
              <TableHead className="pr-4 text-right">
                {t.history.purgeable}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody className="tabular-nums">
            {buckets
              .filter((b) => b.versions > 0)
              .reverse()
              .map((b) => (
                <TableRow key={b.key}>
                  <TableCell className="pl-4 font-medium">
                    {formatBucket(b.key, shownGrouping, lang)}
                  </TableCell>
                  <TableCell className="text-right">{b.versions}</TableCell>
                  <TableCell className="text-right max-md:hidden">
                    {b.created}
                  </TableCell>
                  <TableCell className="text-right max-md:hidden">
                    {b.updated}
                  </TableCell>
                  <TableCell className="text-right max-md:hidden">
                    {b.deleted}
                  </TableCell>
                  <TableCell className="text-right max-md:hidden">
                    {b.initial}
                  </TableCell>
                  <TableCell className="text-right max-sm:hidden">
                    {formatBytes(b.bytes, lang)}
                  </TableCell>
                  <TableCell className="pr-4 text-right">
                    {b.purgeable}
                  </TableCell>
                </TableRow>
              ))}
          </TableBody>
        </Table>
      </Card>
    </>
  )
}

// StatTile shows one figure of the statistics.
function StatTile({
  label,
  value,
  detail,
}: {
  label: string
  value: string
  detail?: ReactNode
}) {
  return (
    <Card className="gap-1 px-4 py-4">
      <div className="text-sm text-muted-foreground">{label}</div>
      <div className="font-heading text-2xl font-semibold">{value}</div>
      {detail && <div className="text-xs text-muted-foreground">{detail}</div>}
    </Card>
  )
}

// ClearCard clears the versions created before a day, but the current one
// of each site.
function ClearCard({
  stats,
  onCleared,
}: {
  stats: VersionStats
  onCleared: () => void
}) {
  const { t, lang } = useI18n()
  const [before, setBefore] = useState(() => daysAgo(30))
  const [busy, setBusy] = useState(false)
  const count = before ? purgeableBefore(stats.days, before) : 0
  const day = before ? formatDate(startOfDay(before), lang) : ""

  const clear = async () => {
    setBusy(true)
    try {
      const deleted = await api.purgeVersions(startOfDay(before))
      toast.success(t.history.cleared(deleted))
    } catch (err) {
      toast.error(t.history.clearFailed, {
        description: describeError(t, errorOf(err)),
      })
    }
    setBusy(false)
    onCleared()
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t.history.clearTitle}</CardTitle>
        <CardDescription>{t.history.clearText}</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="flex flex-wrap items-end gap-4">
          <Field className="w-fit">
            <FieldLabel htmlFor="history-before">
              {t.history.clearBefore}
            </FieldLabel>
            <Input
              id="history-before"
              type="date"
              value={before}
              onChange={(e) => setBefore(e.target.value)}
              className="w-44"
              disabled={busy}
            />
          </Field>
          <ConfirmButton
            title={t.history.clearConfirmTitle(count)}
            description={t.history.clearConfirmText(day)}
            action={t.history.clear}
            onConfirm={() => void clear()}
          >
            <Button variant="destructive" disabled={busy || count === 0}>
              {busy ? <Spinner /> : <Trash2 />}
              {t.history.clear}
            </Button>
          </ConfirmButton>
        </div>
        <FieldDescription className="mt-2" aria-live="polite">
          {before ? t.history.clearCount(count, day) : t.history.pickDay}
        </FieldDescription>
      </CardContent>
    </Card>
  )
}
