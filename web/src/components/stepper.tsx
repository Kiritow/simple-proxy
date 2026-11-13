import { Check } from "lucide-react"
import { cn } from "cn"

import { useI18n } from "@/lib/i18n"
import type { StepId } from "@/lib/model"

export type StepInfo = { id: StepId; title: string; description: string }

export function Stepper({
  steps,
  current,
  problems,
  onSelect,
}: {
  steps: StepInfo[]
  current: StepId
  problems: Record<StepId, number>
  onSelect: (step: StepId) => void
}) {
  const { t } = useI18n()
  const currentIndex = steps.findIndex((s) => s.id === current)
  return (
    <nav aria-label={t.app.steps}>
      <ol className="flex items-center gap-3">
        {steps.map((step, i) => {
          const isCurrent = i === currentIndex
          const done = i < currentIndex
          const count = problems[step.id]
          return (
            <li
              key={step.id}
              className="flex flex-1 items-center gap-3 last:flex-none"
            >
              <button
                type="button"
                onClick={() => onSelect(step.id)}
                aria-current={isCurrent ? "step" : undefined}
                className="flex items-center gap-2.5 rounded-lg text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <span
                  className={cn(
                    "flex size-7 shrink-0 items-center justify-center rounded-full border text-xs font-semibold transition-colors",
                    isCurrent &&
                      "border-primary bg-primary text-primary-foreground",
                    done && "border-primary text-primary",
                    !isCurrent && !done && "text-muted-foreground",
                    count > 0 &&
                      !isCurrent &&
                      "border-destructive text-destructive"
                  )}
                >
                  {done && count === 0 ? <Check className="size-3.5" /> : i + 1}
                </span>
                <span
                  // On small screens, only the current step is labeled visibly.
                  className={cn(
                    "leading-tight",
                    !isCurrent && "max-sm:sr-only"
                  )}
                >
                  <span className="block text-sm font-medium">
                    {step.title}
                  </span>
                  <span
                    className={cn(
                      "block text-xs text-muted-foreground",
                      count > 0 && "text-destructive"
                    )}
                  >
                    {count > 0 ? t.steps.problems(count) : step.description}
                  </span>
                </span>
              </button>
              {i < steps.length - 1 && (
                <div
                  className={cn(
                    "h-px flex-1 bg-border",
                    done && "bg-primary/50"
                  )}
                />
              )}
            </li>
          )
        })}
      </ol>
    </nav>
  )
}
