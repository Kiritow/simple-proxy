import { useMemo } from "react"
import { FileDiff } from "lucide-react"
import { cn } from "cn"

import { Spinner } from "@/components/ui/spinner"
import type { Preview } from "@/lib/api"
import { diffLines, hunks, type DiffLine } from "@/lib/diff"
import type { PreviewState } from "@/lib/hooks"
import { useI18n } from "@/lib/i18n"

// ChangesPanel shows what saving a site would change in the nginx
// configuration.
export function ChangesPanel({
  state,
  touched,
  className,
}: {
  state: PreviewState
  touched: boolean // whether problems with the site are shown
  className?: string
}) {
  const { t } = useI18n()
  const { error, loading } = state

  const changes = useMemo(() => summarize(state.preview), [state.preview])

  let body
  if (error && (error.status === 400 || error.status === 422)) {
    body = (
      <Message>
        {touched ? t.site.previewProblems : t.site.previewEmpty}
      </Message>
    )
  } else if (error?.status === 404) {
    body = <Message>{t.site.deletedText}</Message> // deleted elsewhere
  } else if (error) {
    body = <Message>{error.message}</Message>
  } else if (!changes) {
    body = (
      <div className="flex flex-1 items-center justify-center">
        <Spinner />
      </div>
    )
  } else if (changes.hunks.length === 0) {
    body = <Message>{t.site.previewNoChanges}</Message>
  } else {
    body = (
      <div className="w-max min-w-full py-1 font-mono text-xs leading-5">
        {changes.hunks.map((hunk, i) => (
          <div key={i}>
            {i > 0 && (
              <div className="px-3 text-muted-foreground select-none">⋯</div>
            )}
            {hunk.map((line, j) => (
              <Line key={j} line={line} />
            ))}
          </div>
        ))}
      </div>
    )
  }

  const showCounts = changes && !error && changes.hunks.length > 0
  return (
    <section
      aria-label={t.site.preview}
      className={cn(
        "flex min-h-0 flex-col overflow-hidden rounded-lg border bg-muted/30",
        className
      )}
    >
      <header className="flex items-center gap-2 border-b px-3 py-2 text-sm font-medium">
        <FileDiff className="size-4 text-muted-foreground" />
        <span className="mr-auto">{t.site.preview}</span>
        {loading && <Spinner className="size-3.5 text-muted-foreground" />}
        {showCounts && (
          <span className="font-mono text-xs">
            <span className="text-emerald-700 dark:text-emerald-400">
              +{changes.added}
            </span>{" "}
            <span className="text-red-700 dark:text-red-400">
              −{changes.removed}
            </span>
          </span>
        )}
      </header>
      <div className="flex min-h-0 flex-1 flex-col overflow-auto">{body}</div>
    </section>
  )
}

// summarize returns the hunks of a preview, and the numbers of added and
// removed lines.
function summarize(preview: Preview | null) {
  if (!preview) return null
  const lines = diffLines(
    preview.current.split("\n"),
    preview.proposed.split("\n")
  )
  return {
    hunks: hunks(lines),
    added: lines.filter((l) => l.kind === "add").length,
    removed: lines.filter((l) => l.kind === "del").length,
  }
}

function Message({ children }: { children: string }) {
  return (
    <p className="m-auto max-w-64 p-6 text-center text-sm text-muted-foreground">
      {children}
    </p>
  )
}

function Line({ line }: { line: DiffLine }) {
  return (
    <div
      className={cn(
        "flex pr-3",
        line.kind === "add" &&
          "bg-emerald-500/15 text-emerald-950 dark:text-emerald-100",
        line.kind === "del" && "bg-red-500/15 text-red-950 dark:text-red-100"
      )}
    >
      <span className="w-9 shrink-0 pr-2 text-right text-muted-foreground/60 select-none">
        {line.oldNo}
      </span>
      <span className="w-9 shrink-0 pr-2 text-right text-muted-foreground/60 select-none">
        {line.newNo}
      </span>
      <span className="w-4 shrink-0 select-none">
        {{ same: " ", add: "+", del: "−" }[line.kind]}
      </span>
      <span className="whitespace-pre">{line.text}</span>
    </div>
  )
}
