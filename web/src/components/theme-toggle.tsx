import { Moon, Sun } from "lucide-react"

import { useTheme } from "@/components/theme-provider"
import { useI18n } from "@/lib/i18n"
import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"

export function ThemeToggle() {
  const { theme, setTheme } = useTheme()
  const { t } = useI18n()
  const dark =
    theme === "dark" ||
    (theme === "system" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches)

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          onClick={() => setTheme(dark ? "light" : "dark")}
          aria-label={t.app.toggleTheme}
        >
          {dark ? <Sun /> : <Moon />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        {t.app.toggleTheme} <kbd className="ml-1 opacity-70">D</kbd>
      </TooltipContent>
    </Tooltip>
  )
}
