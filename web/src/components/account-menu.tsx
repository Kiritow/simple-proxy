import { useState, type FormEvent } from "react"
import { Bot, CircleAlert, CircleUser, KeyRound, LogOut } from "lucide-react"
import { toast } from "sonner"

import { TextField } from "@/components/form"
import { TokensDialog } from "@/components/tokens-dialog"
import { Alert, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { FieldGroup } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import { api, errorOf, type ApiError } from "@/lib/api"
import { displayName, useAuth } from "@/lib/auth"
import { describeError, useI18n } from "@/lib/i18n"

// AccountMenu shows who is signed in, changes their password, manages their
// API tokens and signs out.
export function AccountMenu() {
  const { t } = useI18n()
  const { me, signOut } = useAuth()
  const [changing, setChanging] = useState<{ key: number } | null>(null)
  const [tokens, setTokens] = useState<{ key: number } | null>(null)
  const user = me.user
  const name = displayName(user)
  const canChangePassword = user.method === "password" && !user.password_managed

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="sm" aria-label={t.account.menu}>
            <CircleUser />
            <span className="max-w-32 truncate max-sm:sr-only">{name}</span>
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-60">
          <DropdownMenuLabel className="flex flex-col gap-0.5 py-1.5">
            <span className="truncate text-sm text-foreground">{name}</span>
            {name !== user.username && (
              <span className="truncate font-normal">@{user.username}</span>
            )}
            <span className="font-normal">{t.roles.names[user.role]}</span>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          {canChangePassword && (
            <DropdownMenuItem onSelect={() => setChanging({ key: Date.now() })}>
              <KeyRound />
              {t.account.changePassword}
            </DropdownMenuItem>
          )}
          <DropdownMenuItem onSelect={() => setTokens({ key: Date.now() })}>
            <Bot />
            {t.tokens.menu}
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={signOut}>
            <LogOut />
            {t.auth.signOut}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {changing && (
        <PasswordDialog
          key={changing.key}
          open
          onOpenChange={(o) => !o && setChanging(null)}
        />
      )}
      {tokens && (
        <TokensDialog
          key={tokens.key}
          open
          onOpenChange={(o) => !o && setTokens(null)}
        />
      )}
    </>
  )
}

// PasswordDialog changes the password of the signed-in account.
function PasswordDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useI18n()
  const [current, setCurrent] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [busy, setBusy] = useState(false)
  const [submitted, setSubmitted] = useState(false)
  const [failure, setFailure] = useState<ApiError | null>(null)

  const problem = (field: string) =>
    failure?.fields.find((f) => f.field === field)
  const differ = submitted && confirm !== password
  const unplaced =
    failure &&
    (failure.fields.length === 0 ||
      failure.fields.some((f) => !["current", "password"].includes(f.field)))

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setSubmitted(true)
    if (confirm !== password) return
    setBusy(true)
    setFailure(null)
    try {
      await api.changePassword(current, password)
    } catch (err) {
      setFailure(errorOf(err))
      setBusy(false)
      return
    }
    setBusy(false)
    toast.success(t.account.changed)
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="flex flex-col gap-6">
          <DialogHeader>
            <DialogTitle>{t.account.changePassword}</DialogTitle>
            <DialogDescription>
              {t.account.changePasswordText}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            {failure && unplaced && (
              <Alert variant="destructive">
                <CircleAlert />
                <AlertTitle>{describeError(t, failure)}</AlertTitle>
              </Alert>
            )}
            <TextField
              id="account-current-password"
              label={t.account.currentPassword}
              type="password"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
              error={problem("current")}
              autoComplete="current-password"
              autoFocus
              required
              disabled={busy}
            />
            <TextField
              id="account-new-password"
              label={t.account.newPassword}
              description={t.auth.passwordHint}
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              error={problem("password")}
              autoComplete="new-password"
              required
              disabled={busy}
            />
            <TextField
              id="account-confirm-password"
              label={t.account.confirmPassword}
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              error={differ ? t.auth.passwordsDiffer : undefined}
              autoComplete="new-password"
              required
              disabled={busy}
            />
          </FieldGroup>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline" disabled={busy}>
                {t.app.cancel}
              </Button>
            </DialogClose>
            <Button type="submit" disabled={busy}>
              {busy && <Spinner />}
              {busy ? t.account.changing : t.account.change}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
