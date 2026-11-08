import { useQueryClient } from "@tanstack/react-query";
import { motion, useReducedMotion } from "motion/react";
import { Suspense, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  Navigate,
  NavLink,
  Outlet,
  useLocation,
  useMatches,
  useNavigationType,
} from "react-router";
import { sessionApi } from "@/api/client-endpoints";
import { AdminIcon } from "@/components/admin/icons";
import { FullScreenLoader, PageSkeleton } from "@/components/admin/loading";
import { PreferenceSwitchers } from "@/components/admin/switchers";
import { toastApiError } from "@/components/admin/toast";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { useSession } from "@/hooks/use-session";
import { cn } from "@/lib/cn";
import { AppErrorBoundary } from "./error-boundary";

const navGroups = [
  {
    key: "",
    items: [{ href: "/admin", key: "overview", end: true }],
  },
  {
    key: "lifeMemory",
    items: [
      { href: "/admin/life", key: "life", end: false },
      { href: "/admin/diaries", key: "diaries", end: false },
      { href: "/admin/memories", key: "memories", end: false },
      { href: "/admin/topics", key: "topics", end: false },
    ],
  },
  {
    key: "communication",
    items: [
      { href: "/admin/conversations", key: "conversations", end: false },
      { href: "/admin/members", key: "members", end: false },
      { href: "/admin/stickers", key: "stickers", end: false },
    ],
  },
  {
    key: "operations",
    items: [
      { href: "/admin/personas", key: "personas", end: false },
      { href: "/admin/settings", key: "settings", end: false },
      { href: "/admin/system", key: "system", end: false },
    ],
  },
];

export function AdminLayout() {
  const { t } = useTranslation();
  const location = useLocation();
  const matches = useMatches();
  const navigationType = useNavigationType();
  const reducedMotion = useReducedMotion();
  const queryClient = useQueryClient();
  const session = useSession();
  const [loggingOut, setLoggingOut] = useState(false);

  const titleKey = [...matches]
    .reverse()
    .map((match) => (match.handle as { titleKey?: string } | undefined)?.titleKey)
    .find(Boolean);

  useEffect(() => {
    document.title = titleKey
      ? `${t(titleKey)} - ${t("common.appName")}`
      : t("common.appName");
  }, [titleKey, t]);

  useEffect(() => {
    if (navigationType !== "POP") window.scrollTo({ top: 0 });
  }, [location.pathname, navigationType]);

  if (session.isPending) return <FullScreenLoader />;

  if (session.isError) {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center gap-3 text-center">
        <p className="text-sm text-muted-foreground">{t("common.loadFailed")}</p>
        <Button variant="outline" onClick={() => window.location.reload()}>
          {t("common.retry")}
        </Button>
      </div>
    );
  }

  if (!session.data?.authenticated) {
    const next = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`/login?next=${next}`} replace />;
  }

  const logout = async () => {
    setLoggingOut(true);
    try {
      await sessionApi.logout();
      queryClient.setQueryData(["session"], {
        enabled: true,
        authenticated: false,
        version: session.data?.version ?? "",
      });
    } catch (error) {
      toastApiError(error);
    } finally {
      setLoggingOut(false);
    }
  };

  return (
    <div className="admin-shell">
      <aside className="admin-sidebar">
        <div className="flex h-full flex-col px-3 py-4">
          <div className="min-w-0 px-3 pb-6">
            <div className="text-lg font-semibold leading-5 text-foreground">
              {t("common.appName")}
            </div>
            <div className="mt-1 truncate text-xs text-muted-foreground/70">
              {session.data.version}
            </div>
          </div>
          <nav
            className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto"
            aria-label={t("nav.label")}
          >
            <div className="space-y-3">
              {navGroups.map((group, groupIndex) => (
                <div
                  key={group.key || "home"}
                  className={cn(
                    groupIndex > 0 && "border-t border-border/70 pt-3",
                  )}
                >
                  {group.key ? (
                    <p className="px-3 pb-1.5 text-[11px] font-medium tracking-wide text-muted-foreground/60">
                      {t(`nav.groups.${group.key}`)}
                    </p>
                  ) : null}
                  <ul className="space-y-1">
                    {group.items.map((item) => (
                      <li key={item.href}>
                        <NavLink
                          to={item.href}
                          end={item.end}
                          title={t(`nav.${item.key}`)}
                          className={({ isActive }) =>
                            cn(
                              "group relative flex h-10 w-full items-center gap-3 rounded-xl px-3 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/35",
                              isActive
                                ? "bg-primary/12 text-primary before:absolute before:-left-3 before:h-6 before:w-1 before:rounded-r-full before:bg-primary"
                                : "text-foreground/60 hover:bg-primary/10 hover:text-primary",
                            )
                          }
                        >
                          <AdminIcon
                            name={item.key === "personas" ? "persona" : item.key}
                            className="size-[18px]"
                          />
                          <span>{t(`nav.${item.key}`)}</span>
                        </NavLink>
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          </nav>
          <div className="mt-3 border-t border-border pt-3">
            <div className="flex items-center justify-between px-2 pb-2">
              <PreferenceSwitchers />
            </div>
            <Button
              variant="ghost"
              className="w-full justify-start text-foreground/75"
              onClick={logout}
              disabled={loggingOut}
            >
              {loggingOut ? (
                <Spinner />
              ) : (
                <AdminIcon name="log-out" className="size-[18px]" />
              )}
              <span>{t("common.logout")}</span>
            </Button>
          </div>
        </div>
      </aside>
      <div className="min-w-0">
        <main id="admin-main" className="admin-page">
          <AppErrorBoundary key={location.pathname}>
            <motion.div
              initial={reducedMotion ? false : { opacity: 0, y: 6 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: reducedMotion ? 0 : 0.15, ease: "easeOut" }}
            >
              <Suspense fallback={<PageSkeleton />}>
                <Outlet />
              </Suspense>
            </motion.div>
          </AppErrorBoundary>
        </main>
      </div>
    </div>
  );
}
