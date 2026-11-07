import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import ReactMarkdown from "react-markdown";
import { diariesApi } from "@/api/client-endpoints";
import { PageHeader } from "@/components/admin/page-header";
import { SectionCard } from "@/components/admin/section-card";
import { AdminIcon } from "@/components/admin/icons";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/tooltip";
import { Spinner } from "@/components/ui/spinner";
import { useQueryParams } from "@/hooks/use-query-params";
import { formatDateTime } from "@/lib/format";
import { cn } from "@/lib/cn";

const pad = (value: number) => String(value).padStart(2, "0");
const weekdayKeys = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"];
const dateKeyPattern = /^\d{4}-\d{2}-\d{2}$/;

// shanghaiToday 用上海时区取归属日，避免浏览器时区把凌晨算成另一天
function shanghaiToday(): string {
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).formatToParts(new Date());
  const value = (type: string) =>
    parts.find((part) => part.type === type)?.value ?? "";
  return `${value("year")}-${value("month")}-${value("day")}`;
}

function shiftMonth(month: string, delta: number): string {
  const [year, monthIndex] = month.split("-").map(Number);
  const date = new Date(year, monthIndex - 1 + delta, 1);
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}`;
}

// monthGrid 按周一开始生成月历格子，空缺用 null 占位
function monthGrid(month: string): (string | null)[] {
  const [year, monthIndex] = month.split("-").map(Number);
  const first = new Date(year, monthIndex - 1, 1);
  const offset = (first.getDay() + 6) % 7;
  const days = new Date(year, monthIndex, 0).getDate();
  const cells: (string | null)[] = Array.from({ length: offset }, () => null);
  for (let day = 1; day <= days; day += 1) {
    cells.push(`${month}-${pad(day)}`);
  }
  while (cells.length % 7 !== 0) cells.push(null);
  return cells;
}

// dateFromKey 只用于按年月日取展示标题，不参与时区换算
function dateFromKey(date: string): Date {
  const [year, month, day] = date.split("-").map(Number);
  return new Date(year, month - 1, day);
}

export default function DiariesPage() {
  const { t, i18n } = useTranslation();
  const { params, update } = useQueryParams();
  const language = i18n.language.startsWith("zh") ? "zh-CN" : "en-US";
  const today = useMemo(shanghaiToday, []);

  const rawDate = params.get("date") ?? "";
  const selectedDate = dateKeyPattern.test(rawDate) ? rawDate : today;
  const rawMonth = params.get("month") ?? "";
  const month = /^\d{4}-\d{2}$/.test(rawMonth)
    ? rawMonth
    : selectedDate.slice(0, 7);

  const monthQuery = useQuery({
    queryKey: ["diary-month", month],
    queryFn: () => diariesApi.month(month),
    refetchInterval: 30_000,
  });
  const dayQuery = useQuery({
    queryKey: ["diary-day", selectedDate],
    queryFn: () => diariesApi.day(selectedDate),
    refetchInterval: 30_000,
  });

  const dates = useMemo(
    () => new Set(monthQuery.data?.dates ?? []),
    [monthQuery.data],
  );
  const cells = useMemo(() => monthGrid(month), [month]);
  const monthLabel = new Intl.DateTimeFormat(language, {
    year: "numeric",
    month: "long",
  }).format(dateFromKey(`${month}-01`));
  const entryTitle = new Intl.DateTimeFormat(language, {
    dateStyle: "full",
  }).format(dateFromKey(selectedDate));
  const entry = dayQuery.data?.entry ?? null;

  const selectDate = (date: string) => update({ date, month: undefined });
  const shift = (delta: number) =>
    update({ month: shiftMonth(month, delta) });

  return (
    <div className="space-y-6">
      <PageHeader title={t("diaries.title")} subtitle={t("diaries.subtitle")} />

      <div className="grid items-start gap-4 min-[900px]:grid-cols-[300px_minmax(0,1fr)]">
        <SectionCard
          title={t("diaries.calendarTitle")}
        >
          <div className="flex items-center justify-between gap-2">
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t("diaries.monthPrev")}
              onClick={() => shift(-1)}
            >
              <AdminIcon name="chevron-left" className="size-4" />
            </Button>
            <span className="text-sm font-medium text-foreground">
              {monthLabel}
            </span>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t("diaries.monthNext")}
              onClick={() => shift(1)}
            >
              <AdminIcon name="chevron-right" className="size-4" />
            </Button>
          </div>

          <div className="mt-4 grid grid-cols-7 gap-1 text-center text-xs text-muted-foreground/75">
            {weekdayKeys.map((key) => (
              <span key={key} className="py-1">
                {t(`diaries.weekdays.${key}`)}
              </span>
            ))}
          </div>
          <div className="mt-1 grid grid-cols-7 gap-1">
            {cells.map((date, index) => {
              if (!date) {
                return <span key={`empty-${index}`} aria-hidden="true" />;
              }
              const selected = date === selectedDate;
              const isToday = date === today;
              return (
                <button
                  key={date}
                  type="button"
                  aria-label={t("diaries.selectDate", { date })}
                  aria-pressed={selected}
                  onClick={() => selectDate(date)}
                  className={cn(
                    "relative flex h-9 items-center justify-center rounded-lg text-sm tabular-nums transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/35",
                    selected
                      ? "bg-primary font-medium text-primary-foreground"
                      : "text-foreground/75 hover:bg-primary/10 hover:text-primary",
                    isToday && !selected ? "ring-1 ring-primary/40" : "",
                  )}
                >
                  {Number(date.slice(8))}
                  {dates.has(date) ? (
                    <span
                      className={cn(
                        "absolute bottom-1 size-1 rounded-full",
                        selected ? "bg-primary-foreground" : "bg-primary/70",
                      )}
                    />
                  ) : null}
                </button>
              );
            })}
          </div>
          {monthQuery.isError ? (
            <div className="mt-4 flex flex-wrap items-center gap-2">
              <p className="text-xs text-destructive">
                {t("diaries.loadFailed")}
              </p>
              <Button
                variant="ghost"
                size="xs"
                disabled={monthQuery.isFetching}
                onClick={() => void monthQuery.refetch()}
              >
                {monthQuery.isFetching ? <Spinner /> : null}
                {t("diaries.retry")}
              </Button>
            </div>
          ) : monthQuery.data && monthQuery.data.dates.length === 0 ? (
            <p className="mt-4 text-xs text-muted-foreground/60">
              {t("diaries.emptyMonth")}
            </p>
          ) : null}
        </SectionCard>

        <SectionCard
          title={
            <span className="flex items-center gap-2">
              {entryTitle}
              {selectedDate === today ? (
                <Badge variant="ghost">{t("diaries.today")}</Badge>
              ) : null}
            </span>
          }
          actions={
            <div className="flex items-center gap-1">
              <Button
                variant="ghost"
                size="xs"
                disabled={!dayQuery.data?.previous_date}
                onClick={() => {
                  const previous = dayQuery.data?.previous_date;
                  if (previous) selectDate(previous);
                }}
              >
                <AdminIcon name="chevron-left" className="size-3.5" />
                {t("diaries.previous")}
              </Button>
              <Button
                variant="ghost"
                size="xs"
                disabled={!dayQuery.data?.next_date}
                onClick={() => {
                  const next = dayQuery.data?.next_date;
                  if (next) selectDate(next);
                }}
              >
                {t("diaries.next")}
                <AdminIcon name="chevron-right" className="size-3.5" />
              </Button>
            </div>
          }
        >
          {dayQuery.isPending ? (
            <div className="space-y-3">
              <Skeleton className="h-4 w-1/2" />
              <Skeleton className="h-4 w-full" />
              <Skeleton className="h-4 w-5/6" />
              <Skeleton className="h-24 w-full" />
            </div>
          ) : dayQuery.isError || !dayQuery.data ? (
            <div className="flex flex-col items-start gap-3">
              <p className="text-sm text-destructive">
                {t("diaries.loadFailed")}
              </p>
              <p className="text-xs text-muted-foreground/75">
                {t("diaries.loadFailedHint")}
              </p>
              <Button
                variant="outline"
                size="sm"
                disabled={dayQuery.isFetching}
                onClick={() => void dayQuery.refetch()}
              >
                {dayQuery.isFetching ? <Spinner /> : null}
                {t("diaries.retry")}
              </Button>
            </div>
          ) : !entry ? (
            <p className="text-sm text-muted-foreground/75">
              {t("diaries.noEntry")}
            </p>
          ) : (
            <div className="space-y-4">
              <div className="flex flex-wrap gap-x-5 gap-y-1 text-xs text-muted-foreground/75">
                <span>
                  {t("diaries.createdAt", {
                    time: formatDateTime(entry.created_at),
                  })}
                </span>
                <span>
                  {t("diaries.updatedAt", {
                    time: formatDateTime(entry.updated_at),
                  })}
                </span>
              </div>
              <div className="admin-prose max-w-none space-y-4 [&_a]:text-primary [&_a]:underline [&_a]:underline-offset-4 [&_blockquote]:border-l-2 [&_blockquote]:border-primary/30 [&_blockquote]:pl-4 [&_blockquote]:text-muted-foreground [&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:py-0.5 [&_code]:text-xs [&_h1]:mt-6 [&_h1]:text-xl [&_h1]:font-semibold [&_h1]:text-foreground [&_h2]:mt-5 [&_h2]:text-lg [&_h2]:font-semibold [&_h2]:text-foreground [&_h3]:mt-4 [&_h3]:text-sm [&_h3]:font-semibold [&_h3]:text-foreground [&_hr]:border-border [&_li]:ml-4 [&_ul_li]:list-disc [&_ol_li]:list-decimal [&_pre]:overflow-x-auto [&_pre]:rounded-lg [&_pre]:bg-muted/60 [&_pre]:p-3 [&_strong]:font-semibold [&_strong]:text-foreground [&_ul]:space-y-1">
                <ReactMarkdown>{entry.markdown}</ReactMarkdown>
              </div>
            </div>
          )}
        </SectionCard>
      </div>
    </div>
  );
}
