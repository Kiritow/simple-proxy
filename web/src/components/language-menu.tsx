import { Languages } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { languages, useI18n, type Lang, type LangSetting } from "@/lib/i18n"

export function LanguageMenu() {
  const { t, setting, setSetting } = useI18n()
  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label={t.app.language}>
              <Languages />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>{t.app.language}</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="w-auto">
        <DropdownMenuRadioGroup
          value={setting}
          onValueChange={(value) => setSetting(value as LangSetting)}
        >
          <DropdownMenuRadioItem value="auto">
            {t.app.followBrowser}
          </DropdownMenuRadioItem>
          <DropdownMenuSeparator />
          {(Object.keys(languages) as Lang[]).map((lang) => (
            <DropdownMenuRadioItem key={lang} value={lang} lang={lang}>
              {languages[lang].langName}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
