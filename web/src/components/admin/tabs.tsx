import { NavLink } from "react-router";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/cn";

function TabLink({
  to,
  label,
  end,
}: {
  to: string;
  label: string;
  end?: boolean;
}) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        cn(
          "px-3 py-1.5 text-sm font-medium transition-colors",
          isActive
            ? "bg-primary text-primary-foreground"
            : "bg-card text-muted-foreground hover:bg-muted hover:text-foreground",
        )
      }
    >
      {label}
    </NavLink>
  );
}

export function SettingsTabs() {
  const { t } = useTranslation();
  return (
    <nav
      className="inline-flex shrink-0 overflow-hidden rounded-lg border border-border"
      aria-label={t("settings.title")}
    >
      <TabLink to="/admin/settings" label={t("settings.tabs.general")} end />
      <TabLink to="/admin/settings/models" label={t("settings.tabs.models")} />
      <TabLink to="/admin/settings/mcp" label={t("settings.tabs.mcp")} />
    </nav>
  );
}

export function SystemTabs() {
  const { t } = useTranslation();
  return (
    <nav
      className="inline-flex shrink-0 overflow-hidden rounded-lg border border-border"
      aria-label={t("system.title")}
    >
      <TabLink to="/admin/system" label={t("system.tabs.status")} end />
      <TabLink to="/admin/system/logs" label={t("system.tabs.logs")} />
      <TabLink to="/admin/system/models" label={t("system.tabs.models")} />
    </nav>
  );
}
