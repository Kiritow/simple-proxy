import { useCallback, useEffect, useState } from "react"
import {
  Check,
  Copy,
  Download,
  FileCode2,
  LoaderCircle,
  Play,
  RotateCcw,
  ServerCog,
  Waypoints,
} from "lucide-react"

import { OutputView } from "@/components/output-view"
import { ThemeToggle } from "@/components/theme-toggle"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Kbd, KbdGroup } from "@/components/ui/kbd"
import { Textarea } from "@/components/ui/textarea"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  copyText,
  fetchExample,
  generateConfig,
  type GenerateResult,
} from "@/lib/api"

const draftKey = "simple-proxy:draft"
const initialDraft = localStorage.getItem(draftKey) ?? ""
const isMac = /Mac|iPhone|iPad/.test(navigator.userAgent)

export function App() {
  const [source, setSource] = useState(initialDraft)
  const [result, setResult] = useState<GenerateResult | null>(null)
  const [pending, setPending] = useState(false)
  const [copied, setCopied] = useState(false)

  useEffect(() => localStorage.setItem(draftKey, source), [source])

  const showError = (err: unknown) =>
    setResult({ ok: false, status: 0, message: String(err) })

  const loadExample = () => fetchExample().then(setSource, showError)

  // Greet first-time visitors with the example instead of an empty editor.
  useEffect(() => {
    if (initialDraft) return
    let active = true
    fetchExample().then(
      (example) => {
        if (active) setSource(example)
      },
      (err) => {
        if (active) setResult({ ok: false, status: 0, message: String(err) })
      }
    )
    return () => {
      active = false
    }
  }, [])

  const generate = useCallback(async () => {
    setPending(true)
    setResult(await generateConfig(source))
    setPending(false)
  }, [source])

  const config = result?.ok ? result.config : null

  const copy = async () => {
    if (config === null) return
    try {
      await copyText(config)
    } catch (err) {
      console.error(err) // e.g. clipboard permission denied
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  const download = () => {
    if (config === null) return
    const url = URL.createObjectURL(new Blob([config], { type: "text/plain" }))
    Object.assign(document.createElement("a"), {
      href: url,
      download: "proxy.conf",
    }).click()
    setTimeout(() => URL.revokeObjectURL(url))
  }

  return (
    <div className="flex h-svh flex-col">
      <header className="flex items-center gap-3 border-b px-4 py-3 sm:px-6">
        <div className="flex size-9 items-center justify-center rounded-lg bg-primary text-primary-foreground">
          <Waypoints className="size-5" />
        </div>
        <div className="mr-auto">
          <h1 className="font-heading text-base leading-tight font-semibold">
            Simple Proxy
          </h1>
          <p className="text-xs text-muted-foreground">
            Generate nginx reverse proxy configuration from YAML
          </p>
        </div>
        <ThemeToggle />
      </header>

      <main className="grid flex-1 gap-4 overflow-y-auto p-4 sm:p-6 lg:min-h-0 lg:grid-cols-2 lg:overflow-hidden">
        <Card className="min-h-[32rem] lg:min-h-0">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <FileCode2 className="size-4 text-muted-foreground" />
              config.yaml
            </CardTitle>
            <CardDescription>
              Virtual hosts and the upstreams they proxy to.
            </CardDescription>
            <CardAction className="flex gap-2">
              <Button variant="outline" size="sm" onClick={loadExample}>
                <RotateCcw />
                Example
              </Button>
              <Button size="sm" onClick={generate} disabled={pending}>
                {pending ? <LoaderCircle className="animate-spin" /> : <Play />}
                Generate
                <KbdGroup className="ml-1 hidden sm:inline-flex">
                  <Kbd>{isMac ? "⌘" : "Ctrl"}</Kbd>
                  <Kbd>↵</Kbd>
                </KbdGroup>
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent className="flex min-h-0 flex-1">
            <Textarea
              value={source}
              onChange={(e) => setSource(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                  e.preventDefault()
                  void generate()
                }
              }}
              spellCheck={false}
              wrap="off"
              autoCapitalize="off"
              autoCorrect="off"
              aria-label="Proxy configuration (YAML)"
              className="field-sizing-fixed flex-1 resize-none bg-muted/40 font-mono text-[13px] leading-relaxed md:text-[13px] dark:bg-muted/20"
            />
          </CardContent>
        </Card>

        <Card className="min-h-[32rem] lg:min-h-0">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <ServerCog className="size-4 text-muted-foreground" />
              proxy.conf
              {result && (
                <Badge
                  key={String(result.ok)}
                  variant={result.ok ? "secondary" : "destructive"}
                >
                  {result.ok
                    ? `${result.config.trimEnd().split("\n").length} lines`
                    : "Error"}
                </Badge>
              )}
            </CardTitle>
            <CardDescription>
              Include it in nginx's <code>http</code> context, e.g. in{" "}
              <code>conf.d/</code>.
            </CardDescription>
            <CardAction className="flex gap-2">
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="outline"
                    size="icon-sm"
                    disabled={config === null}
                    onClick={copy}
                    aria-label="Copy to clipboard"
                  >
                    {copied ? <Check /> : <Copy />}
                  </Button>
                </TooltipTrigger>
                <TooltipContent>
                  {copied ? "Copied" : "Copy to clipboard"}
                </TooltipContent>
              </Tooltip>
              <Button
                variant="outline"
                size="sm"
                disabled={config === null}
                onClick={download}
              >
                <Download />
                Download
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent className="flex min-h-0 flex-1 flex-col">
            <OutputView result={result} />
          </CardContent>
        </Card>
      </main>
    </div>
  )
}

export default App
