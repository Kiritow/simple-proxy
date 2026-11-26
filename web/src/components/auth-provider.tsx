import { useEffect, useMemo, useState, type ReactNode } from "react"
import { CircleAlert, RotateCw } from "lucide-react"

import { LoginPage } from "@/components/login-page"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { api, errorOf, onUnauthorized, type ApiError, type Me } from "@/lib/api"
import {
  AuthContext,
  loginErrorOf,
  withoutLoginError,
  type Auth,
} from "@/lib/auth"
import { useI18n } from "@/lib/i18n"

type State =
  | { kind: "loading" }
  | { kind: "signedOut" }
  | { kind: "failed"; error: ApiError } // the server could not tell
  | { kind: "signedIn"; me: Me }

// AuthProvider renders children once signed in, and the login page until
// then. It finds out again whether the session lasts, and what it may do,
// whenever the window is shown again, and drops back to the login page as
// soon as a request finds the session ended.
export function AuthProvider({ children }: { children: ReactNode }) {
  const { t } = useI18n()
  const [state, setState] = useState<State>({ kind: "loading" })
  const [loads, setLoads] = useState(0)
  // A failed sign-in with the OpenID Connect provider returns to the
  // console with its error in the URL, which is shown on the login page
  // until signing in.
  const [loginError, setLoginError] = useState(() =>
    loginErrorOf(location.href)
  )

  useEffect(() => {
    if (loginErrorOf(location.href) !== null) {
      history.replaceState(history.state, "", withoutLoginError(location.href))
    }
  }, [])

  useEffect(() => {
    let active = true
    api.me().then(
      (me) => {
        if (active) setState({ kind: "signedIn", me })
      },
      (err: unknown) => {
        if (!active) return
        const error = errorOf(err)
        if (error.status === 401) setState({ kind: "signedOut" })
        // While signed in, keep going: requests will tell.
        else
          setState((s) =>
            s.kind === "signedIn" ? s : { kind: "failed", error }
          )
      }
    )
    return () => {
      active = false
    }
  }, [loads])

  useEffect(() => {
    const check = () => {
      if (document.visibilityState === "visible") setLoads((n) => n + 1)
    }
    document.addEventListener("visibilitychange", check)
    return () => document.removeEventListener("visibilitychange", check)
  }, [])

  useEffect(() => onUnauthorized(() => setState({ kind: "signedOut" })), [])

  const auth = useMemo<Auth | null>(
    () =>
      state.kind === "signedIn"
        ? {
            me: state.me,
            reload: () => setLoads((n) => n + 1),
            signOut: () => {
              api.logout().then(
                () => setState({ kind: "signedOut" }),
                () => setState({ kind: "signedOut" })
              )
            },
          }
        : null,
    [state]
  )

  switch (state.kind) {
    case "loading":
      return (
        <div className="flex min-h-svh items-center justify-center">
          <Spinner />
        </div>
      )
    case "failed":
      return (
        <div className="flex min-h-svh items-center justify-center p-4">
          <Empty>
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <CircleAlert />
              </EmptyMedia>
              <EmptyTitle>{t.auth.unreachable}</EmptyTitle>
              <EmptyDescription>{state.error.message}</EmptyDescription>
            </EmptyHeader>
            <EmptyContent>
              <Button variant="outline" onClick={() => setLoads((n) => n + 1)}>
                <RotateCw />
                {t.app.tryAgain}
              </Button>
            </EmptyContent>
          </Empty>
        </div>
      )
    case "signedOut":
      return (
        <LoginPage
          error={loginError}
          onSignedIn={(me) => {
            setLoginError(null)
            setState({ kind: "signedIn", me })
          }}
        />
      )
    case "signedIn":
      return (
        <AuthContext.Provider value={auth}>{children}</AuthContext.Provider>
      )
  }
}
