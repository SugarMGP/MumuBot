import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { dashboardApi } from "@/api/client-endpoints";
import { AdminIcon } from "@/components/admin/icons";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { SectionCard } from "@/components/admin/section-card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { clampPercent, formatDateTime } from "@/lib/format";

const metrics = [
  { key: "conversations", icon: "conversations", tone: "bg-info/10" },
  { key: "memories", icon: "memories", tone: "bg-primary/10" },
  { key: "topics", icon: "topics", tone: "bg-secondary/10" },
  { key: "workers", icon: "tools", tone: "bg-accent/10" },
] as const;

export default function DashboardPage() {
  const { t } = useTranslation();
  const query = useQuery({ queryKey: ["dashboard"], queryFn: dashboardApi.get, refetchInterval: 15_000 });
  const [showAllEmotions, setShowAllEmotions] = useState(false);

  if (query.isPending) return <PageSkeleton />;
  if (query.isError || !query.data) {
    return (
      <p className="text-sm text-muted-foreground">{t("common.loadFailed")}</p>
    );
  }
  const data = query.data;
  const visibleEmotions = showAllEmotions
    ? data.emotions
    : data.emotions.slice(0, 3);
  const values: Record<string, number | string> = {
    conversations: data.counts.active_conversations,
    memories: data.counts.active_memories,
    topics: data.counts.active_topics,
    workers: `${data.counts.running_workers} / ${data.counts.worker_count}`,
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("dashboard.title")}
        subtitle={t("dashboard.subtitle")}
      />

      <section className="grid grid-cols-4 gap-4">
        {metrics.map((metric) => (
          <Card key={metric.key}>
            <div className="admin-card-pad-compact flex items-center gap-3">
              <span
                className={`admin-icon-tile size-8 shrink-0 text-foreground/75 ${metric.tone}`}
              >
                <AdminIcon name={metric.icon} className="size-4" />
              </span>
              <div>
                <div className="admin-caption-text text-muted-foreground">
                  {t(`dashboard.metrics.${metric.key}`)}
                </div>
                <div className="admin-metric-value mt-1 text-xl text-foreground">
                  {values[metric.key]}
                </div>
              </div>
            </div>
          </Card>
        ))}
      </section>

      <div className="grid items-start grid-cols-[minmax(0,1fr)_minmax(240px,clamp(240px,22vw,320px))] gap-4 min-[1360px]:grid-cols-[minmax(0,2fr)_minmax(280px,0.8fr)]">
        <SectionCard
          title={t("dashboard.currentEmotions")}
          description={t("dashboard.emotionsHint")}
          actions={<Badge>{query.isFetching ? t("common.refreshing") : t("dashboard.live")}</Badge>}
        >
          <div className="space-y-4">
            {data.mood ? (
              <div className="space-y-3">
                {[
                  {
                    label: t("dashboard.energy"),
                    value: data.mood.energy,
                    tone: "bg-secondary",
                  },
                  {
                    label: t("dashboard.sociability"),
                    value: data.mood.sociability,
                    tone: "bg-warning",
                  },
                ].map((row) => (
                  <div key={row.label} className="flex items-center gap-3">
                    <span className="w-16 shrink-0 text-sm text-muted-foreground">
                      {row.label}
                    </span>
                    <div className="h-2 flex-1 overflow-hidden rounded-full bg-muted">
                      <div
                        className={`h-2 rounded-full ${row.tone}`}
                        style={{ width: `${clampPercent(row.value)}%` }}
                      />
                    </div>
                  </div>
                ))}
              </div>
            ) : null}

            {data.emotions_error ? (
              <div className="space-y-2">
                <p className="text-xs text-destructive">
                  {t("dashboard.emotionsError")}
                </p>
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => void query.refetch()}
                >
                  {t("dashboard.retryEmotions")}
                </Button>
              </div>
            ) : data.emotions.length === 0 ? (
              <p className="text-xs text-muted-foreground/60">
                {t("dashboard.noEmotions")}
              </p>
            ) : (
              <div className="space-y-3">
                <p className="admin-caption-text text-muted-foreground/60">
                  {t("dashboard.emotionCount", { count: data.emotions.length })}
                </p>
                {visibleEmotions.map((emotion) => (
                  <div
                    key={emotion.id}
                    className="admin-card-pad-tile space-y-2 rounded-xl border border-border"
                  >
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge className="max-w-full whitespace-normal break-words text-left">
                        {emotion.kind}
                      </Badge>
                      <Badge variant="outline">
                        {t(`enums.emotionStrength.${emotion.strength_band}`, {
                          defaultValue: emotion.strength_band,
                        })}
                      </Badge>
                    </div>
                    <p className="admin-caption-text whitespace-pre-wrap break-words text-foreground/75">
                      <span className="text-muted-foreground/60">
                        {t("dashboard.emotionFact")}
                      </span>
                      {emotion.fact}
                    </p>
                    <p className="admin-caption-text whitespace-pre-wrap break-words text-foreground/75">
                      <span className="text-muted-foreground/60">
                        {t("dashboard.emotionReason")}
                      </span>
                      {emotion.reason}
                    </p>
                    <time className="block text-xs text-muted-foreground/45">
                      {t("dashboard.emotionUpdatedAt", {
                        time: formatDateTime(emotion.updated_at),
                      })}
                    </time>
                  </div>
                ))}
                {data.emotions.length > 3 ? (
                  <Button
                    variant="ghost"
                    size="xs"
                    aria-expanded={showAllEmotions}
                    onClick={() => setShowAllEmotions((value) => !value)}
                  >
                    {showAllEmotions
                      ? t("dashboard.collapseEmotions")
                      : t("dashboard.expandEmotions", {
                          count: data.emotions.length - 3,
                        })}
                  </Button>
                ) : null}
              </div>
            )}
          </div>
        </SectionCard>

        <SectionCard
          title={t("dashboard.runtime")}
          actions={
            <AdminIcon
              name="connection"
              className={
                data.runtime.connected
                  ? "size-5 text-secondary"
                  : "size-5 text-destructive"
              }
            />
          }
        >
          <div className="divide-y divide-border text-sm">
            {[
              {
                label: t("dashboard.onebot"),
                value: (
                  <Badge
                    variant={
                      data.runtime.connected ? "success" : "destructive"
                    }
                  >
                    {data.runtime.connected
                      ? t("enums.connection.connected")
                      : t("enums.connection.disconnected")}
                  </Badge>
                ),
              },
              {
                label: t("dashboard.botAccount"),
                value: <code className="text-xs">{data.runtime.self_id}</code>,
              },
              {
                label: t("dashboard.mcpTools"),
                value: data.runtime.mcp_tool_count,
              },
            ].map((row) => (
              <div
                key={row.label}
                className="flex items-center justify-between py-2.5"
              >
                <span className="admin-label-text text-muted-foreground">
                  {row.label}
                </span>
                <span className="admin-body-text">{row.value}</span>
              </div>
            ))}
          </div>
        </SectionCard>
      </div>

      {data.mood ? (
        <Card>
          <div className="admin-card-pad-compact flex items-center gap-4">
            <span className="inline-flex size-10 items-center justify-center rounded-full bg-primary/10 text-primary">
              <AdminIcon name="mood" className="size-5" />
            </span>
            <div>
              <h2 className="admin-card-title-compact text-foreground">
                {t("dashboard.moodSummary")}
              </h2>
              <p className="admin-body-text mt-1 text-muted-foreground">
                {data.mood.last_reason || t("dashboard.emptyReason")}
              </p>
            </div>
          </div>
        </Card>
      ) : null}
    </div>
  );
}
