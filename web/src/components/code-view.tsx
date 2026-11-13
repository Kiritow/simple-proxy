import type { ReactNode } from "react"

export function CodeBlock({ children }: { children: ReactNode }) {
  return (
    <pre className="overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-[13px] leading-relaxed dark:bg-muted/20">
      <code>{children}</code>
    </pre>
  )
}

const blockNames = new Set(["server", "location", "map"])

// NginxCode renders generated configuration with minimal syntax highlighting.
// The generator emits one directive per line, so a line-based approach is
// enough.
export function NginxCode({ text }: { text: string }) {
  return text.split("\n").map((line, i) => {
    const comment = /^(\s*)(#.*)$/.exec(line)
    if (comment) {
      return (
        <Line key={i} indent={comment[1]}>
          <span className="text-muted-foreground italic">{comment[2]}</span>
        </Line>
      )
    }
    const directive = /^(\s*)([\w-]+)(.*)$/.exec(line)
    if (!directive) return <Line key={i}>{line}</Line>
    const [, indent, name, rest] = directive
    return (
      <Line key={i} indent={indent}>
        <span
          className={
            blockNames.has(name)
              ? "font-semibold text-violet-600 dark:text-violet-400"
              : "text-sky-700 dark:text-sky-400"
          }
        >
          {name}
        </span>
        {rest.split(/(\$\w+)/).map((part, j) =>
          part.startsWith("$") ? (
            <span key={j} className="text-amber-700 dark:text-amber-300">
              {part}
            </span>
          ) : (
            part
          )
        )}
      </Line>
    )
  })
}

// YamlCode highlights the keys and comments of a YAML document.
export function YamlCode({ text }: { text: string }) {
  return text.split("\n").map((line, i) => {
    const m = /^(\s*(?:- )?)([\w-]+)(:)(.*)$/.exec(line)
    if (!m) return <Line key={i}>{line}</Line>
    const [, indent, key, colon, rest] = m
    return (
      <Line key={i} indent={indent}>
        <span className="text-sky-700 dark:text-sky-400">{key}</span>
        {colon}
        {rest}
      </Line>
    )
  })
}

function Line({ indent, children }: { indent?: string; children: ReactNode }) {
  return (
    <>
      {indent}
      {children}
      {"\n"}
    </>
  )
}
