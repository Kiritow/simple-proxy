// Building blocks shared by the builder's steps.

import { useState, type ComponentProps, type ReactNode } from "react"
import { Plus, X } from "lucide-react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldLabel,
  FieldLegend,
  FieldSet,
  FieldTitle,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import type { FieldProblem } from "@/lib/api"
import type { Problems } from "@/lib/hooks"
import { describeProblem, useI18n } from "@/lib/i18n"
import { directiveKey, newId, type Directive } from "@/lib/model"

// useDescribe returns the message for a problem reported by the server, or
// passes a message through.
function useDescribe() {
  const { t } = useI18n()
  return (error?: FieldProblem | string) =>
    typeof error === "string" || error === undefined
      ? error
      : describeProblem(t, error)
}

export function StepHeader({
  title,
  description,
  actions,
}: {
  title: ReactNode
  description: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className="flex flex-wrap items-end gap-3">
      <div className="mr-auto min-w-0">
        <h2 className="truncate font-heading text-lg font-semibold">{title}</h2>
        <p className="text-sm text-muted-foreground">{description}</p>
      </div>
      {actions && <div className="flex gap-2">{actions}</div>}
    </div>
  )
}

// TextField is a labeled input. Its id is also the key of the input, so
// that problems can be shown on it and it can be focused to fix them.
export function TextField({
  id,
  label,
  description,
  error,
  className,
  ...props
}: {
  id: string
  label: ReactNode
  description?: ReactNode
  error?: FieldProblem | string
} & ComponentProps<typeof Input>) {
  const message = useDescribe()(error)
  return (
    <Field data-invalid={message ? true : undefined} className={className}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Input
        id={id}
        aria-invalid={message ? true : undefined}
        autoComplete="off"
        spellCheck={false}
        {...props}
      />
      {message ? (
        <FieldError>{message}</FieldError>
      ) : (
        description && <FieldDescription>{description}</FieldDescription>
      )}
    </Field>
  )
}

// ToggleCard is a switch presented as a selectable card.
export function ToggleCard({
  id,
  title,
  description,
  checked,
  onCheckedChange,
}: {
  id: string
  title: ReactNode
  description: ReactNode
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <FieldLabel htmlFor={id}>
      <Field orientation="horizontal">
        <FieldContent>
          <FieldTitle>{title}</FieldTitle>
          <FieldDescription>{description}</FieldDescription>
        </FieldContent>
        <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} />
      </Field>
    </FieldLabel>
  )
}

// DirectiveList edits the raw nginx directives of a server or location.
export function DirectiveList({
  owner,
  items,
  onChange,
  problems,
  placeholder,
}: {
  owner: string // key of the site or route
  items: Directive[]
  onChange: (items: Directive[]) => void
  problems: Problems
  placeholder: string
}) {
  const { t } = useI18n()
  const describe = useDescribe()
  const [added, setAdded] = useState("")
  const add = () => {
    const d = { id: newId(), text: "" }
    setAdded(d.id)
    onChange([...items, d])
  }

  return (
    <FieldSet className="gap-3">
      <FieldLegend variant="label" className="mb-0">
        {t.directives.title}
      </FieldLegend>
      <FieldDescription>{t.directives.text}</FieldDescription>
      {items.map((d) => {
        const key = directiveKey(owner, d)
        const error = describe(problems.byKey.get(key))
        return (
          <Field key={d.id} data-invalid={error ? true : undefined}>
            <div className="flex gap-2">
              <Input
                id={key}
                value={d.text}
                onChange={(e) =>
                  onChange(
                    items.map((it) =>
                      it.id === d.id ? { ...it, text: e.target.value } : it
                    )
                  )
                }
                placeholder={placeholder}
                aria-label={t.directives.label}
                aria-invalid={error ? true : undefined}
                autoFocus={d.id === added}
                autoComplete="off"
                spellCheck={false}
                className="font-mono"
              />
              <IconButton
                label={t.directives.remove}
                onClick={() => onChange(items.filter((it) => it.id !== d.id))}
              >
                <X />
              </IconButton>
            </div>
            <FieldError>{error}</FieldError>
          </Field>
        )
      })}
      <Button variant="outline" size="sm" className="w-fit" onClick={add}>
        <Plus />
        {t.directives.add}
      </Button>
    </FieldSet>
  )
}

export function IconButton({
  label,
  ...props
}: { label: string } & ComponentProps<typeof Button>) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label={label} {...props} />
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

// ConfirmButton asks for confirmation before a destructive action.
export function ConfirmButton({
  title,
  description,
  action,
  onConfirm,
  children,
}: {
  title: ReactNode
  description: ReactNode
  action: string
  onConfirm: () => void
  children: ReactNode // the trigger
}) {
  const { t } = useI18n()
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>{children}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t.confirm.cancel}</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={onConfirm}>
            {action}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
