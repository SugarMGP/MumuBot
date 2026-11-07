import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { systemApi } from "@/api/client-endpoints";
import type { AggregateRow, StatsSeriesRow } from "@/api/types";
import { Chart } from "@/components/admin/chart";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { SectionCard } from "@/components/admin/section-card";
import { SystemTabs } from "@/components/admin/tabs";
import { useTheme } from "@/app/theme-provider";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useQueryParams } from "@/hooks/use-query-params";
import { formatNumber } from "@/lib/format";
import { taskLabel } from "@/lib/labels";
import { chartPalette } from "@/lib/theme-tokens";

const ranges = ["24h", "7d", "30d", "all"] as const;

function failureRate(row: AggregateRow): number {
  return row.request_count > 0
    ? (row.failure_count / row.request_count) * 100
    : 0;
}

function coverage(row: AggregateRow): number {
  return row.request_count > 0
    ? (row.usage_reported_count / row.request_count) * 100
    : 0;
}

function cacheRate(row: AggregateRow): number {
  return row.prompt_tokens > 0
    ? (row.cached_tokens / row.prompt_tokens) * 100
    : 0;
}

function bucketLabel(row: StatsSeriesRow): string {
  const date = new Date(row.bucket_start);
  if (Number.isNaN(date.getTime())) return row.bucket_start;
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

const chartFont =
  '"Noto Sans SC", "PingFang SC", "Microsoft YaHei", "Segoe UI", sans-serif';

export default function SystemModelsPage() {
  const { t, i18n } = useTranslation();
  const { resolved } = useTheme();
  const { params, update } = useQueryParams();
  const range = ranges.includes((params.get("range") || "") as never)
    ? (params.get("range") as string)
    : "24h";
  const query = useQuery({
    queryKey: ["model-stats", range],
    queryFn: () => systemApi.stats(range),
  });
  const palette = chartPalette(resolved);
  const locale = i18n.language.startsWith("zh") ? "zh-CN" : "en-US";

  const trendOption = useMemo(() => {
    const series = query.data?.series ?? [];
    return {
      color: palette.colors,
      textStyle: { color: palette.text, fontFamily: chartFont },
      tooltip: {
        trigger: "axis",
        backgroundColor: palette.tooltipBg,
        borderColor: palette.axis,
        textStyle: { color: palette.tooltipText, fontFamily: chartFont },
      },
      legend: {
        top: 0,
        textStyle: { color: palette.text, fontSize: 11, fontFamily: chartFont },
      },
      grid: { left: 12, right: 16, top: 40, bottom: 8, containLabel: true },
      xAxis: {
        type: "category",
        data: series.map(bucketLabel),
        axisLine: { lineStyle: { color: palette.axis } },
        axisLabel: { color: palette.text, fontSize: 10, fontFamily: chartFont },
      },
      yAxis: [
        {
          type: "value",
          name: t("system.stats.requestShort"),
          nameTextStyle: { color: palette.text, fontSize: 10, fontFamily: chartFont },
          axisLabel: { color: palette.text, fontSize: 10, fontFamily: chartFont },
          splitLine: { lineStyle: { color: palette.split } },
        },
        {
          type: "value",
          name: t("system.stats.tokenShort"),
          nameTextStyle: { color: palette.text, fontSize: 10, fontFamily: chartFont },
          axisLabel: { color: palette.text, fontSize: 10, fontFamily: chartFont },
          splitLine: { show: false },
        },
        {
          type: "value",
          name: t("system.stats.latencyShort"),
          position: "right",
          offset: 48,
          nameTextStyle: { color: palette.text, fontSize: 10, fontFamily: chartFont },
          axisLabel: { color: palette.text, fontSize: 10, fontFamily: chartFont },
          splitLine: { show: false },
        },
      ],
      series: [
        {
          name: t("system.stats.requestShort"),
          type: "line",
          smooth: true,
          symbolSize: 4,
          data: series.map((row) => row.request_count),
        },
        {
          name: t("system.stats.tokenShort"),
          type: "line",
          smooth: true,
          symbolSize: 4,
          yAxisIndex: 1,
          data: series.map((row) => row.total_tokens),
        },
        {
          name: t("system.stats.latencyShort"),
          type: "line",
          smooth: true,
          symbolSize: 4,
          yAxisIndex: 2,
          data: series.map((row) =>
            row.request_count > 0
              ? Math.round((row.latency_ms_sum / row.request_count) * 10) / 10
              : 0,
          ),
        },
      ],
    };
  }, [palette, query.data?.series, t]);

  const taskRows = useMemo(
    () =>
      (query.data?.by_task ?? []).map((row) => ({
        ...row,
        label: taskLabel(t, row.label),
      })),
    [query.data?.by_task, t],
  );
  const modelRows = useMemo(
    () => query.data?.by_model ?? [],
    [query.data?.by_model],
  );

  const pieOptions = useMemo(() => {
    const build = (rows: AggregateRow[], title: string) => ({
      color: palette.colors,
      textStyle: { color: palette.text, fontFamily: chartFont },
      tooltip: {
        trigger: "item",
        backgroundColor: palette.tooltipBg,
        borderColor: palette.axis,
        textStyle: { color: palette.tooltipText, fontFamily: chartFont },
      },
      legend: {
        bottom: 0,
        itemWidth: 10,
        itemHeight: 10,
        itemGap: 12,
        textStyle: { color: palette.text, fontSize: 11, fontFamily: chartFont },
      },
      title: {
        text: title,
        left: "center",
        textStyle: { color: palette.text, fontSize: 12, fontWeight: 500, fontFamily: chartFont },
      },
      series: [
        {
          type: "pie",
          radius: ["40%", "64%"],
          center: ["50%", "48%"],
          label: { show: false },
          data: rows.map((row) => ({
            name: row.label,
            value: row.request_count,
          })),
        },
      ],
    });
    return {
      task: build(taskRows, t("system.stats.byTask")),
      model: build(modelRows, t("system.stats.byModel")),
    };
  }, [palette, taskRows, modelRows, t]);

  if (query.isPending) return <PageSkeleton />;
  if (query.isError || !query.data) {
    return (
      <p className="text-sm text-muted-foreground">{t("common.loadFailed")}</p>
    );
  }
  const totals = query.data.totals;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <PageHeader
          title={t("system.title")}
          subtitle={t("system.subtitle")}
          tag={t("system.tabs.models")}
        />
        <SystemTabs />
      </div>

      <div className="flex justify-end">
        <div className="inline-flex overflow-hidden rounded-lg border border-border">
          {ranges.map((value) => (
            <Button
              key={value}
              variant={range === value ? "default" : "ghost"}
              size="sm"
              className="rounded-none"
              onClick={() => update({ range: value }, { replace: true })}
            >
              {t(`system.stats.ranges.${value}`)}
            </Button>
          ))}
        </div>
      </div>

      <Card>
        <div className="grid grid-cols-2 divide-x divide-border min-[1280px]:grid-cols-5">
          {[
            {
              label: t("system.stats.requests"),
              value: formatNumber(totals.request_count, locale),
              hint: t("system.stats.success", {
                count: formatNumber(
                  totals.request_count - totals.failure_count,
                  locale,
                ),
              }),
            },
            {
              label: t("system.stats.failureRate"),
              value: t("system.stats.percent", {
                value: failureRate(totals).toFixed(1),
              }),
            },
            {
              label: t("system.stats.avgLatency"),
              value: t("system.stats.millis", {
                value: totals.average_latency_ms.toFixed(0),
              }),
            },
            {
              label: t("system.stats.tokens"),
              value: formatNumber(totals.total_tokens, locale),
              hint: `${t("system.stats.averageTokens", {
                count: formatNumber(
                  totals.request_count > 0
                    ? Math.round(totals.total_tokens / totals.request_count)
                    : 0,
                  locale,
                ),
              })} · ${t("system.stats.coverage", {
                percent: coverage(totals).toFixed(0),
              })}`,
            },
            {
              label: t("system.stats.cacheHit"),
              value: t("system.stats.percent", {
                value: cacheRate(totals).toFixed(1),
              }),
              hint: t("system.stats.cacheTokens", {
                count: formatNumber(totals.cached_tokens, locale),
              }),
            },
          ].map((item) => (
            <div key={item.label} className="admin-card-pad-compact">
              <p className="admin-caption-text text-muted-foreground">{item.label}</p>
              <p className="admin-metric-value mt-2 text-2xl text-foreground">
                {item.value}
              </p>
              {item.hint ? (
                <p className="admin-caption-text mt-1 text-muted-foreground/60">
                  {item.hint}
                </p>
              ) : null}
            </div>
          ))}
        </div>
      </Card>

      <div className="grid grid-cols-1 gap-4 min-[1400px]:grid-cols-[minmax(0,1.6fr)_minmax(360px,1fr)]">
        <SectionCard title={t("system.stats.trend")}>
          {query.data.series.length === 0 ? (
            <p className="py-10 text-center text-sm text-muted-foreground">
              {t("system.stats.empty")}
            </p>
          ) : (
            <Chart option={trendOption} className="admin-model-chart" />
          )}
        </SectionCard>
        <SectionCard title={t("system.stats.distribution")}>
          <div className="grid grid-cols-2">
            <Chart option={pieOptions.task} className="admin-model-chart" />
            <Chart option={pieOptions.model} className="admin-model-chart" />
          </div>
        </SectionCard>
      </div>

      <div className="grid grid-cols-[repeat(auto-fit,minmax(420px,1fr))] gap-4">
        {[
          {
            title: t("system.stats.byTask"),
            label: t("system.stats.task"),
            rows: taskRows,
          },
          {
            title: t("system.stats.byModel"),
            label: t("system.stats.model"),
            rows: modelRows,
          },
        ].map((section) => (
          <SectionCard key={section.title} title={section.title}>
            {section.rows.length === 0 ? (
              <p className="py-8 text-center text-sm text-muted-foreground">
                {t("system.stats.empty")}
              </p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{section.label}</TableHead>
                    <TableHead className="text-right">
                      {t("system.stats.requests")}
                    </TableHead>
                    <TableHead className="text-right">
                      {t("system.stats.failureRate")}
                    </TableHead>
                    <TableHead className="text-right">
                      {t("system.stats.avgLatency")}
                    </TableHead>
                    <TableHead className="text-right">
                      {t("system.stats.tokens")}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {section.rows.map((row) => (
                    <TableRow key={row.label}>
                      <TableCell>{row.label}</TableCell>
                      <TableCell className="admin-number text-right">
                        {formatNumber(row.request_count, locale)}
                      </TableCell>
                      <TableCell className="admin-number text-right">
                        {t("system.stats.percent", {
                          value: failureRate(row).toFixed(1),
                        })}
                      </TableCell>
                      <TableCell className="admin-number text-right">
                        {t("system.stats.millis", {
                          value: row.average_latency_ms.toFixed(0),
                        })}
                      </TableCell>
                      <TableCell className="admin-number text-right">
                        {formatNumber(row.total_tokens, locale)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </SectionCard>
        ))}
      </div>

    </div>
  );
}
