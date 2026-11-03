import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { dashboardApi } from "@/api/client-endpoints";
import { AdminIcon } from "@/components/admin/icons";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { SectionCard } from "@/components/admin/section-card";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { clampPercent, formatDateTime } from "@/lib/format";

const metrics = [
  { key: "groups", icon: "groups", tone: "bg-info/10" },
  { key: "memories", icon: "memories", tone: "bg-primary/10" },
  { key: "members", icon: "members", tone: "bg-warning/10" },
  { key: "activeMemories", icon: "jargons", tone: "bg-secondary/10" },
  { key: "topics", icon: "style-cards", tone: "bg-accent/10" },
  { key: "stickers", icon: "stickers", tone: "bg-warning/10" },
] as const;

export default function DashboardPage() {
  const { t } = useTranslation();
  const query = useQuery({ queryKey: ["dashboard"], queryFn: dashboardApi.get });

  if (query.isPending) return <PageSkeleton />;
  if (query.isError || !query.data) {
    return (
      <p className="text-sm text-muted-foreground">{t("common.loadFailed")}</p>
    );
  }
  const data = query.data;
  const values: Record<string, number> = {
    groups: data.enabled_group_count,
    memories: data.counts.memories,
    members: data.counts.members,
    activeMemories: data.counts.active_memories,
    topics: data.counts.topics,
    stickers: data.counts.stickers,
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("dashboard.title")}
        subtitle={t("dashboard.subtitle")}
      />

      <section className="grid grid-cols-3 gap-3 min-[1360px]:grid-cols-6">
        {metrics.map((metric) => (
          <Card key={metric.key}>
            <div className="flex items-center gap-3 p-4">
              <span
                className={`admin-icon-tile size-10 text-foreground/75 ${metric.tone}`}
              >
                <AdminIcon name={metric.icon} className="size-5" />
              </span>
              <div>
                <div className="text-xs text-muted-foreground">
                  {t(`dashboard.metrics.${metric.key}`)}
                </div>
                <div className="admin-number mt-1 text-xl font-semibold text-foreground">
                  {values[metric.key]}
                </div>
              </div>
            </div>
          </Card>
        ))}
      </section>

      <div className="grid grid-cols-[minmax(0,1fr)_minmax(240px,clamp(240px,22vw,320px))] gap-4 min-[1360px]:grid-cols-[minmax(0,2fr)_minmax(280px,0.8fr)]">
        <SectionCard
          title={t("dashboard.currentEmotions")}
          description={t("dashboard.emotionsHint")}
          actions={<Badge>{t("dashboard.live")}</Badge>}
        >
          {data.mood ? (
            <div className="space-y-4">
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
              <div className="space-y-2 pt-1">
                {data.emotions.length === 0 ? (
                  <p className="text-xs text-muted-foreground/70">
                    {t("dashboard.noEmotions")}
                  </p>
                ) : (
                  data.emotions.map((emotion, index) => (
                    <div
                      key={`${emotion.kind}-${index}`}
                      className="flex items-start justify-between gap-3"
                    >
                      <div className="min-w-0">
                        <Badge>
                          {t(`enums.emotions.${emotion.kind}`, {
                            defaultValue: emotion.kind,
                          })}
                          {" · "}
                          {Math.round(emotion.strength * 100)}%
                        </Badge>
                        <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">
                          {emotion.reason}
                        </p>
                      </div>
                      <time className="shrink-0 text-xs text-muted-foreground/60">
                        {formatDateTime(emotion.occurred_at)}
                      </time>
                    </div>
                  ))
                )}
              </div>
            </div>
          ) : (
            <div className="flex h-[220px] items-center justify-center text-sm text-muted-foreground/70">
              {t("dashboard.noMood")}
            </div>
          )}
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
                className="flex items-center justify-between py-3"
              >
                <span className="text-muted-foreground">{row.label}</span>
                <span>{row.value}</span>
              </div>
            ))}
          </div>
        </SectionCard>
      </div>

      {data.mood ? (
        <Card>
          <div className="flex items-center gap-4 p-4">
            <span className="inline-flex size-10 items-center justify-center rounded-full bg-primary/10 text-primary">
              <AdminIcon name="mood" className="size-5" />
            </span>
            <div>
              <h2 className="text-lg font-semibold text-foreground">
                {t("dashboard.moodSummary")}
              </h2>
              <p className="mt-1 text-sm text-muted-foreground">
                {data.mood.last_reason || t("dashboard.emptyReason")}
              </p>
            </div>
          </div>
        </Card>
      ) : null}
    </div>
  );
}
