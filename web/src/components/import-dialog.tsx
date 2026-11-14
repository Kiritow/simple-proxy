import { useRef, useState } from "react"
import { CircleAlert, FileUp } from "lucide-react"

import { Output } from "@/components/form"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
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
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { api, errorOf, type ApiError } from "@/lib/api"
import { describeProblem, useI18n } from "@/lib/i18n"
import {
  ImportError,
  parseYaml,
  type ImportProblem,
  type Site,
} from "@/lib/model"

// ImportDialog replaces the sites with those of a configuration file. Render
// it with a new key every time it opens.
export function ImportDialog({
  open,
  onOpenChange,
  onImport,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  // onImport stores the sites; the dialog reports its errors.
  onImport: (sites: Site[]) => Promise<void>
}) {
  const { t } = useI18n()
  const [text, setText] = useState("")
  const [busy, setBusy] = useState(false)
  // Problems are kept untranslated, so that they follow language changes.
  const [problems, setProblems] = useState<ImportProblem[]>([])
  const [failure, setFailure] = useState<ApiError | null>(null)
  const fileInput = useRef<HTMLInputElement>(null)

  const load = (promise: Promise<string>) =>
    promise.then(
      (loaded) => {
        setText(loaded)
        setProblems([])
        setFailure(null)
      },
      (err: unknown) => setFailure(errorOf(err))
    )

  const submit = async () => {
    setProblems([])
    setFailure(null)
    let sites: Site[]
    try {
      sites = parseYaml(text)
    } catch (err) {
      if (err instanceof ImportError) setProblems(err.problems)
      else setFailure(errorOf(err))
      return
    }
    setBusy(true)
    try {
      await onImport(sites)
    } catch (err) {
      setFailure(errorOf(err))
      setBusy(false)
      return
    }
    setBusy(false)
    onOpenChange(false)
  }

  const messages = [
    ...problems.map((p) => t.import.problems[p.code](p.path, p.detail ?? "")),
    ...(failure?.fields.map((f) => `${f.field}: ${describeProblem(t, f)}`) ??
      []),
  ]
  if (failure && failure.fields.length === 0) messages.push(failure.message)

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t.import.title}</DialogTitle>
          <DialogDescription>{t.import.description}</DialogDescription>
        </DialogHeader>
        <Textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={
            "servers:\n- server_name: example.com\n  default:\n    port: 3000"
          }
          spellCheck={false}
          wrap="off"
          aria-label={t.import.label}
          aria-invalid={messages.length > 0 ? true : undefined}
          disabled={busy}
          className="field-sizing-fixed h-72 resize-none font-mono text-xs md:text-xs"
        />
        {messages.length > 0 && (
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>{t.import.failed}</AlertTitle>
            <AlertDescription className="gap-2">
              <ul className="list-disc pl-4">
                {messages.map((m, i) => (
                  <li key={i} className="whitespace-pre-wrap">
                    {m}
                  </li>
                ))}
              </ul>
              {failure?.output && <Output>{failure.output}</Output>}
            </AlertDescription>
          </Alert>
        )}
        <DialogFooter className="sm:justify-between">
          <div className="flex gap-2">
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => fileInput.current?.click()}
            >
              <FileUp />
              {t.import.openFile}
            </Button>
            <Button
              variant="ghost"
              disabled={busy}
              onClick={() => load(api.example())}
            >
              {t.import.loadExample}
            </Button>
          </div>
          <div className="flex gap-2">
            <DialogClose asChild>
              <Button variant="outline" disabled={busy}>
                {t.app.cancel}
              </Button>
            </DialogClose>
            <Button onClick={submit} disabled={busy || !text.trim()}>
              {busy && <Spinner />}
              {busy ? t.import.importing : t.import.import}
            </Button>
          </div>
        </DialogFooter>
        <input
          ref={fileInput}
          type="file"
          accept=".yaml,.yml,.json"
          hidden
          onChange={(e) => {
            const file = e.target.files?.[0]
            if (file) void load(file.text())
            e.target.value = ""
          }}
        />
      </DialogContent>
    </Dialog>
  )
}
