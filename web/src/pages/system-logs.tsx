import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { systemApi } from "@/api/client-endpoints";
import { PageHeader } from "@/components/admin/page-header";
import { SystemTabs } from "@/components/admin/tabs";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import {
  Field,
  Input,
  NativeSelect,
  SwitchRow,
} from "@/components/ui/form-controls";
import { useLogStream } from "@/hooks/use-log-stream";
import { cn } from "@/lib/cn";

export default function SystemLogsPage() {
  const { t } = useTranslation();
  const [keywordInput, setKeywordInput] = useState("");
  const [keyword, setKeyword] = useState("");
  const [level, setLevel] = useState("debug");
  const [formatted, setFormatted] = useState(false);
  const stream = useLogStream(keyword, level);
  const containerRef = useRef<HTMLDivElement>(null);
  const followRef = useRef(true);
  const [paused, setPaused] = useState(false);
  const anchorRef = useRef<{ seq: number; offset: number } | null>(null);

  useEffect(() => {
    const timer = window.setTimeout(() => setKeyword(keywordInput.trim()), 250);
    return () => window.clearTimeout(timer);
  }, [keywordInput]);

  const captureAnchor = () => {
    const container = containerRef.current;
    if (!container) return;
    const lines = container.querySelectorAll<HTMLElement>("[data-seq]");
    for (const line of lines) {
      if (line.offsetTop + line.offsetHeight > container.scrollTop) {
        anchorRef.current = {
          seq: Number(line.dataset.seq),
          offset: line.offsetTop - container.scrollTop,
        };
        return;
      }
    }
    anchorRef.current = null;
  };

  const onScroll = () => {
    const container = containerRef.current;
    if (!container) return;
    const distance =
      container.scrollHeight - container.scrollTop - container.clientHeight;
    followRef.current = distance < 24;
    setPaused(!followRef.current);
    if (!followRef.current) captureAnchor();
  };

  useLayoutEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    if (followRef.current) {
      container.scrollTop = container.scrollHeight;
      return;
    }
    const anchor = anchorRef.current;
    if (!anchor) return;
    const line = container.querySelector<HTMLElement>(
      `[data-seq="${anchor.seq}"]`,
    );
    if (line) {
      container.scrollTop = line.offsetTop - anchor.offset;
    }
  }, [stream.lines]);

  const jumpToLatest = () => {
    const container = containerRef.current;
    if (!container) return;
    followRef.current = true;
    setPaused(false);
    container.scrollTo({ top: container.scrollHeight, behavior: "smooth" });
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <PageHeader
          title={t("system.title")}
          subtitle={t("system.subtitle")}
          tag={t("system.tabs.logs")}
        />
        <SystemTabs />
      </div>

      <Card>
        <div className="admin-card-stack admin-card-pad">
          <div className="flex items-center justify-between gap-4">
            <div>
              <h2 className="admin-card-title text-foreground">
                {t("system.logs.title")}
              </h2>
              <p className="admin-card-desc mt-1">{t("system.logs.hint")}</p>
            </div>
            <Badge
              variant={stream.status === "live" ? "success" : "warning"}
              className="gap-2"
            >
              <span className="size-2 rounded-full bg-current" />
              {stream.status === "live"
                ? t("system.logs.live")
                : stream.status === "connecting"
                  ? t("system.logs.connecting")
                  : t("system.logs.reconnecting")}
            </Badge>
          </div>

          <div className="flex flex-wrap items-center gap-3 rounded-xl border border-border bg-muted/40 p-3">
            <Field label={t("common.search")} className="w-64">
              <Input
                type="search"
                value={keywordInput}
                placeholder={t("system.logs.searchPlaceholder")}
                onChange={(event) => setKeywordInput(event.target.value)}
              />
            </Field>
            <Field label={t("system.logs.levelLabel")} className="w-40">
              <NativeSelect
                value={level}
                onChange={(event) => setLevel(event.target.value)}
              >
                {["debug", "info", "warn", "error"].map((value) => (
                  <option key={value} value={value}>
                    {t(`system.logs.levels.${value}`)}
                  </option>
                ))}
              </NativeSelect>
            </Field>
            <div className="ml-auto flex items-center gap-4">
              <SwitchRow
                checked={formatted}
                onCheckedChange={setFormatted}
                label={t("system.logs.format")}
              />
              <Button variant="outline" size="sm" asChild>
                <a href={systemApi.logDownloadUrl(keyword, level)}>
                  {t("system.logs.download")}
                </a>
              </Button>
            </div>
          </div>

          <div className="flex items-center justify-between">
            <div className="text-xs text-muted-foreground">
              {stream.status === "reconnecting"
                ? t("system.logs.reconnecting")
                : null}
            </div>
            <div id="admin-log-count" className="admin-log-count">
              {stream.status === "connecting" ? t("common.loading") : (
                <>
                  {t("system.logs.filtered", { count: stream.filtered })}
                  <span />
                  {t("system.logs.buffer", { count: stream.total })}
                </>
              )}
            </div>
          </div>

          <div className={cn(formatted && "admin-log-formatted")}>
            <div
              ref={containerRef}
              onScroll={onScroll}
              className="admin-log-view"
              role="log"
              aria-live="off"
            >
              {stream.status === "connecting" ? (
                <div className="flex items-center gap-2 px-4 py-6 text-sm text-muted-foreground">
                  <Spinner />
                  {t("system.logs.connecting")}
                </div>
              ) : stream.lines.length === 0 ? (
                <p className="px-4 py-6 text-sm text-muted-foreground">
                  {t("system.logs.empty")}
                </p>
              ) : (
                stream.lines.map((line) => (
                  <div
                    key={line.seq}
                    data-seq={line.seq}
                    data-level={line.level}
                    className="admin-log-line"
                  >
                    <time>{line.time}</time>
                    <strong>{line.level}</strong>
                    <span className="admin-log-message">{line.message}</span>
                    {line.fields.length > 0 ? (
                      <span className="admin-log-fields">
                        {line.fields.map((field) => (
                          <span
                            key={`${line.seq}-${field.name}`}
                            className="admin-log-field"
                          >
                            <b>{field.name}</b>
                            <span>{formatted ? (field.formatted_value ?? field.value) : field.value}</span>
                          </span>
                        ))}
                      </span>
                    ) : null}
                  </div>
                ))
              )}
            </div>
          </div>

          {paused ? (
            <div className="flex justify-end">
              <Button variant="outline" size="sm" onClick={jumpToLatest}>
                {t("system.logs.resumeFollow")}
              </Button>
            </div>
          ) : null}
        </div>
      </Card>
    </div>
  );
}
