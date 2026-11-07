import { Suspense, lazy } from "react";
import { FullScreenLoader } from "@/components/admin/loading";
import { useTranslation } from "react-i18next";
import { createBrowserRouter, Navigate, useRouteError } from "react-router";
import { Button } from "@/components/ui/button";
import { AdminLayout } from "./layout";

const LoginPage = lazy(() => import("@/pages/login"));
const DashboardPage = lazy(() => import("@/pages/dashboard"));
const LifePage = lazy(() => import("@/pages/life"));
const DiariesPage = lazy(() => import("@/pages/diaries"));
const MemoriesPage = lazy(() => import("@/pages/memories"));
const MemoryDetailPage = lazy(() => import("@/pages/memory-detail"));
const TopicsPage = lazy(() => import("@/pages/topics"));
const TopicDetailPage = lazy(() => import("@/pages/topic-detail"));
const MembersPage = lazy(() => import("@/pages/members"));
const ConversationsPage = lazy(() => import("@/pages/conversations"));
const StickersPage = lazy(() => import("@/pages/stickers"));
const PersonasPage = lazy(() => import("@/pages/personas"));
const SettingsPage = lazy(() => import("@/pages/settings"));
const ModelsPage = lazy(() => import("@/pages/models"));
const McpPage = lazy(() => import("@/pages/mcp"));
const SystemStatusPage = lazy(() => import("@/pages/system"));
const SystemLogsPage = lazy(() => import("@/pages/system-logs"));
const SystemModelsPage = lazy(() => import("@/pages/system-models"));

function RouteError() {
  const { t } = useTranslation();
  const error = useRouteError();
  console.error("route error", error);
  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center gap-3 text-center">
      <h1 className="text-xl font-semibold text-foreground">
        {t("common.pageError")}
      </h1>
      <p className="text-sm text-muted-foreground">
        {t("common.pageErrorHint")}
      </p>
      <Button onClick={() => window.location.reload()}>
        {t("common.retry")}
      </Button>
    </div>
  );
}

function withSuspense(element: React.ReactNode) {
  return <Suspense fallback={<FullScreenLoader />}>{element}</Suspense>;
}

export const router = createBrowserRouter([
  {
    path: "/login",
    element: withSuspense(<LoginPage />),
    handle: { titleKey: "login.title" },
    errorElement: <RouteError />,
  },
  {
    path: "/",
    element: <AdminLayout />,
    errorElement: <RouteError />,
    children: [
      { index: true, element: <Navigate to="/admin" replace /> },
      {
        path: "admin",
        element: withSuspense(<DashboardPage />),
        handle: { titleKey: "dashboard.title" },
      },
      {
        path: "admin/life",
        element: withSuspense(<LifePage />),
        handle: { titleKey: "life.title" },
      },
      {
        path: "admin/diaries",
        element: withSuspense(<DiariesPage />),
        handle: { titleKey: "diaries.title" },
      },
      {
        path: "admin/memories",
        element: withSuspense(<MemoriesPage />),
        handle: { titleKey: "memories.title" },
      },
      {
        path: "admin/memories/:id",
        element: withSuspense(<MemoryDetailPage />),
        handle: { titleKey: "memories.title" },
      },
      {
        path: "admin/topics",
        element: withSuspense(<TopicsPage />),
        handle: { titleKey: "topics.title" },
      },
      {
        path: "admin/topics/:id",
        element: withSuspense(<TopicDetailPage />),
        handle: { titleKey: "topics.title" },
      },
      {
        path: "admin/members",
        element: withSuspense(<MembersPage />),
        handle: { titleKey: "members.title" },
      },
      {
        path: "admin/conversations",
        element: withSuspense(<ConversationsPage />),
        handle: { titleKey: "contacts.title" },
      },
      {
        path: "admin/stickers",
        element: withSuspense(<StickersPage />),
        handle: { titleKey: "stickers.title" },
      },
      {
        path: "admin/personas",
        element: withSuspense(<PersonasPage />),
        handle: { titleKey: "personas.title" },
      },
      {
        path: "admin/settings",
        element: withSuspense(<SettingsPage />),
        handle: { titleKey: "settings.title" },
      },
      {
        path: "admin/settings/models",
        element: withSuspense(<ModelsPage />),
        handle: { titleKey: "models.title" },
      },
      {
        path: "admin/settings/mcp",
        element: withSuspense(<McpPage />),
        handle: { titleKey: "mcp.title" },
      },
      {
        path: "admin/system",
        element: withSuspense(<SystemStatusPage />),
        handle: { titleKey: "system.title" },
      },
      {
        path: "admin/system/logs",
        element: withSuspense(<SystemLogsPage />),
        handle: { titleKey: "system.title" },
      },
      {
        path: "admin/system/models",
        element: withSuspense(<SystemModelsPage />),
        handle: { titleKey: "system.title" },
      },
    ],
  },
  { path: "*", element: <Navigate to="/admin" replace /> },
]);
