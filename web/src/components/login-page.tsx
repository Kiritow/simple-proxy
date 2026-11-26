import { useEffect, useState, type FormEvent } from "react"
import { CircleAlert, KeyRound, RotateCw, Waypoints } from "lucide-react"

import { TextField } from "@/components/form"
import { LanguageMenu } from "@/components/language-menu"
import { ThemeToggle } from "@/components/theme-toggle"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { FieldGroup, FieldSeparator } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import {
  api,
  errorOf,
  type ApiError,
  type AuthConfig,
  type Me,
} from "@/lib/api"
import { describeError, useI18n } from "@/lib/i18n"

type Mode = "signIn" | "signUp"

// LoginPage signs in with a password or the OpenID Connect provider, or
// signs up, as the server allows. error is the code of a failed sign-in
// with the provider, if any.
export function LoginPage({
  error,
  onSignedIn,
}: {
  error: string | null
  onSignedIn: (me: Me) => void
}) {
  const { t } = useI18n()
  const [config, setConfig] = useState<AuthConfig | null>(null)
  const [configError, setConfigError] = useState<ApiError | null>(null)
  const [loads, setLoads] = useState(0)
  const [mode, setMode] = useState<Mode>("signIn")

  useEffect(() => {
    let active = true
    api.authConfig().then(
      (c) => {
        if (!active) return
        setConfig(c)
        setConfigError(null)
      },
      (err: unknown) => {
        if (active) setConfigError(errorOf(err))
      }
    )
    return () => {
      active = false
    }
  }, [loads])

  const canSignUp = Boolean(config?.registration && config.password)
  const signingUp = mode === "signUp" && canSignUp

  let body
  if (config === null) {
    body = configError ? (
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <CircleAlert />
          </EmptyMedia>
          <EmptyTitle>{t.auth.unreachable}</EmptyTitle>
          <EmptyDescription>{configError.message}</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button variant="outline" onClick={() => setLoads((n) => n + 1)}>
            <RotateCw />
            {t.app.tryAgain}
          </Button>
        </EmptyContent>
      </Empty>
    ) : (
      <div className="flex justify-center py-12">
        <Spinner />
      </div>
    )
  } else {
    body = (
      <>
        {error && !signingUp && (
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>{t.auth.failed}</AlertTitle>
            <AlertDescription>
              {describeError(t, {
                status: 0,
                message: error,
                code: error,
                fields: [],
              })}
            </AlertDescription>
          </Alert>
        )}
        <Card>
          <CardContent>
            {signingUp ? (
              <SignUpForm onSignedIn={onSignedIn} />
            ) : (
              <FieldGroup>
                {config.password && <SignInForm onSignedIn={onSignedIn} />}
                {config.password && config.oidc && (
                  <FieldSeparator>{t.auth.or}</FieldSeparator>
                )}
                {config.oidc && (
                  <Button
                    variant={config.password ? "outline" : "default"}
                    className="w-full"
                    asChild
                  >
                    <a href={api.oidcLoginUrl}>
                      <KeyRound />
                      {t.auth.signInWith(config.oidc.name)}
                    </a>
                  </Button>
                )}
                {!config.password && !config.oidc && (
                  <p className="text-sm text-muted-foreground">
                    {t.auth.noMethods}
                  </p>
                )}
              </FieldGroup>
            )}
          </CardContent>
        </Card>
        {canSignUp && (
          <p className="text-center text-sm text-muted-foreground">
            {signingUp ? t.auth.haveAccount : t.auth.noAccount}{" "}
            <button
              type="button"
              className="font-medium text-foreground underline-offset-4 outline-none hover:underline focus-visible:underline"
              onClick={() => setMode(signingUp ? "signIn" : "signUp")}
            >
              {signingUp ? t.auth.toSignIn : t.auth.createAccount}
            </button>
          </p>
        )}
      </>
    )
  }

  return (
    <div className="flex min-h-svh flex-col">
      <div className="flex justify-end gap-1 p-3">
        <LanguageMenu />
        <ThemeToggle />
      </div>
      <main className="flex flex-1 justify-center px-4 pt-[8svh] pb-16">
        <div className="flex w-full max-w-sm flex-col gap-6">
          <div className="flex flex-col items-center gap-3 text-center">
            <span className="flex size-10 items-center justify-center rounded-xl bg-primary text-primary-foreground">
              <Waypoints className="size-5" />
            </span>
            <div className="flex flex-col gap-1">
              <h1 className="font-heading text-xl font-semibold">
                {signingUp ? t.auth.signUpTitle : t.auth.signInTitle}
              </h1>
              <p className="text-sm text-muted-foreground">
                {signingUp ? t.auth.signUpText : t.auth.signInText}
              </p>
            </div>
          </div>
          {body}
        </div>
      </main>
    </div>
  )
}

function SignInForm({ onSignedIn }: { onSignedIn: (me: Me) => void }) {
  const { t } = useI18n()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<ApiError | null>(null)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setFailure(null)
    let me: Me
    try {
      me = await api.login(username.trim(), password)
    } catch (err) {
      setFailure(errorOf(err))
      setBusy(false)
      return
    }
    onSignedIn(me)
  }

  return (
    <form onSubmit={submit}>
      <FieldGroup>
        {failure && <Failure error={failure} />}
        <TextField
          id="login-username"
          label={t.auth.username}
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoComplete="username"
          autoCapitalize="none"
          autoFocus
          required
          disabled={busy}
        />
        <TextField
          id="login-password"
          label={t.auth.password}
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="current-password"
          required
          disabled={busy}
        />
        <Button type="submit" className="w-full" disabled={busy}>
          {busy && <Spinner />}
          {busy ? t.auth.signingIn : t.auth.signIn}
        </Button>
      </FieldGroup>
    </form>
  )
}

function SignUpForm({ onSignedIn }: { onSignedIn: (me: Me) => void }) {
  const { t } = useI18n()
  const [username, setUsername] = useState("")
  const [displayName, setDisplayName] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [busy, setBusy] = useState(false)
  const [submitted, setSubmitted] = useState(false)
  const [failure, setFailure] = useState<ApiError | null>(null)

  const problem = (field: string) =>
    failure?.fields.find((f) => f.field === field)
  const differ = submitted && confirm !== password
  // Problems with other fields, or with the request as a whole.
  const unplaced =
    failure &&
    (failure.fields.length === 0 ||
      failure.fields.some(
        (f) => !["username", "password", "display_name"].includes(f.field)
      ))

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setSubmitted(true)
    if (confirm !== password) return
    setBusy(true)
    setFailure(null)
    let me: Me
    try {
      me = await api.register(username.trim(), password, displayName.trim())
    } catch (err) {
      setFailure(errorOf(err))
      setBusy(false)
      return
    }
    onSignedIn(me)
  }

  return (
    <form onSubmit={submit}>
      <FieldGroup>
        {failure && unplaced && (
          <Failure error={failure} title={t.auth.signUpFailed} />
        )}
        <TextField
          id="signup-username"
          label={t.auth.username}
          description={t.auth.usernameHint}
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          error={problem("username")}
          autoComplete="username"
          autoCapitalize="none"
          autoFocus
          required
          disabled={busy}
        />
        <TextField
          id="signup-display-name"
          label={t.auth.displayName}
          description={t.auth.displayNameHint}
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          error={problem("display_name")}
          autoComplete="name"
          disabled={busy}
        />
        <TextField
          id="signup-password"
          label={t.auth.password}
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
          id="signup-confirm"
          label={t.auth.confirmPassword}
          type="password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          error={differ ? t.auth.passwordsDiffer : undefined}
          autoComplete="new-password"
          required
          disabled={busy}
        />
        <Button type="submit" className="w-full" disabled={busy}>
          {busy && <Spinner />}
          {busy ? t.auth.signingUp : t.auth.signUp}
        </Button>
      </FieldGroup>
    </form>
  )
}

// Failure tells why signing in or up failed.
function Failure({ error, title }: { error: ApiError; title?: string }) {
  const { t } = useI18n()
  const text = describeError(t, error)
  return (
    <Alert variant="destructive">
      <CircleAlert />
      {title ? (
        <>
          <AlertTitle>{title}</AlertTitle>
          <AlertDescription>{text}</AlertDescription>
        </>
      ) : (
        <AlertTitle>{text}</AlertTitle>
      )}
    </Alert>
  )
}
