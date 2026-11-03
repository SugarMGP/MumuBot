import { Languages, Monitor, Moon, Sun } from "lucide-react";
import { useTranslation } from "react-i18next";
import { setAppLocale, type AppLocale } from "@/app/i18n";
import { useTheme, type ThemeMode } from "@/app/theme-provider";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/cn";

const themeItems: Array<{ value: ThemeMode; icon: typeof Sun }> = [
  { value: "light", icon: Sun },
  { value: "dark", icon: Moon },
  { value: "system", icon: Monitor },
];

export function ThemeSwitcher() {
  const { mode, setMode } = useTheme();
  const { t } = useTranslation();
  const Icon = mode === "dark" ? Moon : mode === "light" ? Sun : Monitor;

  return (
    <DropdownMenu>
      <Tooltip content={t("common.theme.title")}>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t("common.theme.title")}
          >
            <Icon className="size-4" />
          </Button>
        </DropdownMenuTrigger>
      </Tooltip>
      <DropdownMenuContent align="start">
        <DropdownMenuLabel>{t("common.theme.title")}</DropdownMenuLabel>
        {themeItems.map((item) => {
          const ItemIcon = item.icon;
          return (
            <DropdownMenuItem
              key={item.value}
              onSelect={() => setMode(item.value)}
              className={cn(mode === item.value && "bg-muted text-foreground")}
            >
              <ItemIcon className="size-4" />
              <span>{t(`common.theme.${item.value}`)}</span>
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

const locales: AppLocale[] = ["zh-CN", "en-US"];

export function LocaleSwitcher() {
  const { i18n, t } = useTranslation();
  const current = i18n.language.startsWith("zh") ? "zh-CN" : "en-US";

  return (
    <DropdownMenu>
      <Tooltip content={t("common.language.title")}>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t("common.language.title")}
          >
            <Languages className="size-4" />
          </Button>
        </DropdownMenuTrigger>
      </Tooltip>
      <DropdownMenuContent align="start">
        <DropdownMenuLabel>{t("common.language.title")}</DropdownMenuLabel>
        {locales.map((locale) => (
          <DropdownMenuItem
            key={locale}
            onSelect={() => setAppLocale(locale)}
            className={cn(current === locale && "bg-muted text-foreground")}
          >
            <span>{t(`common.language.${locale === "zh-CN" ? "zh" : "en"}`)}</span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function PreferenceSwitchers({ className }: { className?: string }) {
  return (
    <div className={cn("flex items-center gap-1", className)}>
      <ThemeSwitcher />
      <LocaleSwitcher />
    </div>
  );
}
