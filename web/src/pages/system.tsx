import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { systemApi } from "@/api/client-endpoints";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { SystemTabs } from "@/components/admin/tabs";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { enumLabel } from "@/lib/labels";

function FieldRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 py-2">
      <span className="shrink-0 text-sm text-muted-foreground">{label}</span>
      <span className="min-w-0 break-words text-right text-sm text-foreground/85">
        {value}
      </span>
    </div>
  );
}

export default function SystemStatusPage() {
  const { t } = useTranslation();
  const query = useQuery({ queryKey: ["system"], queryFn: systemApi.get });

  if (query.isPending) return <PageSkeleton />;
  if (query.isError || !query.data) {
    return (
      <p className="text-sm text-muted-foreground">{t("common.loadFailed")}</p>
    );
  }
  const data = query.data;
  const compactor = data.context.compactor;

  const personaFields = [
    {
      label: t("system.status.presetName"),
      value: data.persona.preset_name || t("common.none"),
    },
    {
      label: t("system.status.cardChars"),
      value: t("system.status.chars", { count: data.persona.card_chars }),
    },
    {
      label: t("system.status.simChars"),
      value: t("system.status.chars", { count: data.persona.sim_chars }),
    },
  ];

  const contextFields = [
    {
      label: t("system.status.enabledGroups"),
      value: data.context.enabled_group_count,
    },
    {
      label: t("system.status.enabledGroupList"),
      value:
        data.context.enabled_group_ids.length > 0
          ? data.context.enabled_group_ids.join(", ")
          : t("system.status.noGroups"),
    },
    {
      label: t("system.status.thinkInterval"),
      value: t("system.status.seconds", {
        count: data.context.think_interval_sec,
      }),
    },
    {
      label: t("system.status.maxStep"),
      value: t("system.status.steps", { count: data.context.max_step }),
    },
    ...(data.context.think_debounce_ms > 0
      ? [
          {
            label: t("system.status.debounce"),
            value: t("system.status.milliseconds", {
              count: data.context.think_debounce_ms,
            }),
          },
        ]
      : []),
    {
      label: t("system.status.compaction"),
      value: t("system.status.compactionValue", {
        percent: Math.round(compactor.trigger_ratio * 100),
      }),
    },
    {
      label: t("system.status.digestBudget"),
      value: t("system.status.budgetValue", {
        percent: Math.round(compactor.digest_budget_ratio * 100),
      }),
    },
    {
      label: t("system.status.recentTail"),
      value: t("system.status.recentValue", {
        percent: Math.round(compactor.recent_tail_ratio * 100),
      }),
    },
    {
      label: t("system.status.compactorSteps"),
      value: t("system.status.steps", { count: compactor.max_steps }),
    },
    {
      label: t("system.status.idleCompaction"),
      value:
        compactor.idle_minutes > 0
          ? t("system.status.idleValue", {
              minutes: compactor.idle_minutes,
              percent: Math.round(compactor.idle_min_ratio * 100),
            })
          : t("system.status.idleOff"),
    },
  ];

  const modelFields = [
    {
      label: t("system.status.mainModel"),
      value:
        data.model.active?.model_name || t("system.status.notConfigured"),
    },
    ...(data.model.active
      ? [
          {
            label: t("models.fields.baseUrl"),
            value: data.model.active.base_url,
          },
          ...(data.model.active.thinking_type
            ? [
                {
                  label: t("models.fields.thinkingType"),
                  value: enumLabel(t, "thinkingType", data.model.active.thinking_type),
                },
              ]
            : []),
          ...(data.model.active.reasoning_effort
            ? [
                {
                  label: t("models.fields.reasoningEffort"),
                  value: enumLabel(t, "reasoningEffort", data.model.active.reasoning_effort),
                },
              ]
            : []),
        ]
      : []),
    {
      label: t("system.status.retrieval"),
      value: t("system.status.retrievalValue"),
    },
    ...(data.model.mcp_tool_count > 0
      ? [
          {
            label: t("system.status.mcpTools"),
            value: t("system.status.toolsValue", {
              count: data.model.mcp_tool_count,
            }),
          },
        ]
      : []),
    ...(data.model.context_window > 0
      ? [
          {
            label: t("system.status.contextWindow"),
            value: t("system.status.tokens", {
              count: data.model.context_window,
            }),
          },
        ]
      : []),
  ];

  const runtimeFields = [
    {
      label: t("system.status.connection"),
      value: (
        <Badge variant={data.runtime.connected ? "success" : "destructive"}>
          {enumLabel(t, "connection", data.runtime.connected ? "connected" : "disconnected")}
        </Badge>
      ),
    },
    {
      label: t("system.status.reconnect"),
      value: t("system.status.seconds", { count: data.runtime.reconnect_sec }),
    },
    {
      label: t("system.status.schemaVersion"),
      value: t("system.status.schema", {
        current: data.runtime.schema_version,
        latest: data.runtime.latest_schema_version,
      }),
    },
    ...(data.runtime.self_id > 0
      ? [
          {
            label: t("system.status.selfId"),
            value: data.runtime.self_id,
          },
        ]
      : []),
    ...(data.runtime.media_max_bytes_mb > 0
      ? [
          {
            label: t("system.status.mediaLimit"),
            value: t("system.status.mb", {
              count: data.runtime.media_max_bytes_mb,
            }),
          },
        ]
      : []),
  ];

  return (
    <div className="space-y-6">
      <div className="flex items-end justify-between gap-6">
        <PageHeader
          title={t("system.title")}
          subtitle={t("system.subtitle")}
        />
        <SystemTabs />
      </div>

      <div className="grid grid-cols-[repeat(auto-fit,minmax(430px,1fr))] items-start gap-5">
        {[
          { title: t("system.status.sections.persona"), fields: personaFields },
          { title: t("system.status.sections.context"), fields: contextFields },
          { title: t("system.status.sections.model"), fields: modelFields },
          { title: t("system.status.sections.runtime"), fields: runtimeFields },
        ].map((section) => (
          <Card key={section.title}>
            <div className="p-5">
              <h2 className="mb-2 text-lg font-semibold text-foreground">
                {section.title}
              </h2>
              <div className="divide-y divide-border">
                {section.fields.map((field) => (
                  <FieldRow
                    key={field.label}
                    label={field.label}
                    value={field.value}
                  />
                ))}
              </div>
            </div>
          </Card>
        ))}
      </div>
    </div>
  );
}
