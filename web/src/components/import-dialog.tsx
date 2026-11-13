import { useRef, useState } from "react"
import { CircleAlert, FileUp } from "lucide-react"

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
import { Textarea } from "@/components/ui/textarea"
import { fetchExample } from "@/lib/api"
import { ImportError, parseYaml, type Site } from "@/lib/model"

export function ImportDialog({
  open,
  onOpenChange,
  onImport,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onImport: (sites: Site[]) => void
}) {
  const [text, setText] = useState("")
  const [problems, setProblems] = useState<string[]>([])
  const fileInput = useRef<HTMLInputElement>(null)

  const load = (promise: Promise<string>) =>
    promise.then(
      (t) => {
        setText(t)
        setProblems([])
      },
      (err: unknown) => setProblems([String(err)])
    )

  const submit = () => {
    try {
      onImport(parseYaml(text))
    } catch (err) {
      setProblems(err instanceof ImportError ? err.problems : [String(err)])
      return
    }
    setText("")
    setProblems([])
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Import configuration</DialogTitle>
          <DialogDescription>
            Paste a config.yaml (or JSON), e.g. one used with
            legacy-proxy-generator. Importing replaces the current sites.
          </DialogDescription>
        </DialogHeader>
        <Textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={
            "servers:\n- server_name: example.com\n  default:\n    port: 3000"
          }
          spellCheck={false}
          wrap="off"
          aria-label="Configuration"
          aria-invalid={problems.length > 0 ? true : undefined}
          className="field-sizing-fixed h-72 resize-none font-mono text-xs md:text-xs"
        />
        {problems.length > 0 && (
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>The configuration cannot be imported</AlertTitle>
            <AlertDescription>
              <ul className="list-disc pl-4">
                {problems.map((p, i) => (
                  <li key={i} className="whitespace-pre-wrap">
                    {p}
                  </li>
                ))}
              </ul>
            </AlertDescription>
          </Alert>
        )}
        <DialogFooter className="sm:justify-between">
          <div className="flex gap-2">
            <Button
              variant="outline"
              onClick={() => fileInput.current?.click()}
            >
              <FileUp />
              Open file
            </Button>
            <Button variant="ghost" onClick={() => load(fetchExample())}>
              Load example
            </Button>
          </div>
          <div className="flex gap-2">
            <DialogClose asChild>
              <Button variant="outline">Cancel</Button>
            </DialogClose>
            <Button onClick={submit} disabled={!text.trim()}>
              Import
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
