import { CircleAlert, FileText } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import type { GenerateResult } from "@/lib/api"

export function OutputView({ result }: { result: GenerateResult | null }) {
  if (result === null) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-2 rounded-lg border border-dashed text-center text-muted-foreground">
        <FileText className="size-8 opacity-50" />
        <p className="font-medium text-foreground">Nothing generated yet</p>
        <p className="max-w-64 text-xs">
          Edit the configuration on the left, then press Generate.
        </p>
      </div>
    )
  }

  if (!result.ok) {
    return (
      <Alert variant="destructive">
        <CircleAlert />
        <AlertTitle>{errorTitle(result.status)}</AlertTitle>
        <AlertDescription>
          <ul className="mt-1 space-y-1 font-mono text-xs break-all">
            {result.message.split("\n").map((line, i) => (
              <li key={i}>{line.trim()}</li>
            ))}
          </ul>
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <pre className="min-h-0 flex-1 overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-[13px] leading-relaxed dark:bg-muted/20">
      <code>
        {result.config.split("\n").map((line, i) => (
          <NginxLine key={i} line={line} />
        ))}
      </code>
    </pre>
  )
}

function errorTitle(status: number): string {
  switch (status) {
    case 0:
      return "Server unreachable"
    case 400:
      return "Invalid configuration"
    case 413:
      return "Configuration too large"
    case 422:
      return "Certificate not found"
    default:
      return `Generation failed (HTTP ${status})`
  }
}

const blockNames = new Set(["server", "location", "map"])

// NginxLine renders one line of generated configuration with minimal syntax
// highlighting. The generator emits one directive per line, so a line-based
// approach is enough.
function NginxLine({ line }: { line: string }) {
  const comment = /^(\s*)(#.*)$/.exec(line)
  if (comment) {
    return (
      <>
        {comment[1]}
        <span className="text-muted-foreground italic">{comment[2]}</span>
        {"\n"}
      </>
    )
  }

  const directive = /^(\s*)([\w-]+)(.*)$/.exec(line)
  if (!directive) {
    return line + "\n"
  }
  const [, indent, name, rest] = directive
  return (
    <>
      {indent}
      <span
        className={
          blockNames.has(name)
            ? "font-semibold text-violet-600 dark:text-violet-400"
            : "text-sky-700 dark:text-sky-400"
        }
      >
        {name}
      </span>
      {rest.split(/(\$\w+)/).map((part, i) =>
        part.startsWith("$") ? (
          <span key={i} className="text-amber-700 dark:text-amber-300">
            {part}
          </span>
        ) : (
          part
        )
      )}
      {"\n"}
    </>
  )
}
