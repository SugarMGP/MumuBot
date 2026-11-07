import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useParams } from "react-router";
import { topicApi } from "@/api/client-endpoints";
import type { TopicDiffSegment, TopicSummaryChange } from "@/api/types";
import { PageSkeleton } from "@/components/admin/loading";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { formatDateTime } from "@/lib/format";
import { conversationLabel, enumLabel } from "@/lib/labels";

function DiffSegments({
  segments,
  emptyLabel,
}: {
  segments: TopicDiffSegment[];
  emptyLabel: string;
}) {
  if (segments.length === 0) {
    return (
      <span className="admin-topic-inline-diff__placeholder">{emptyLabel}</span>
    );
  }
  return (
    <>
      {segments.map((segment, index) => (
        <span
          key={`${segment.kind}-${index}`}
          className={`admin-topic-inline-diff__segment admin-topic-inline-diff__segment--${segment.kind}`}
        >
          {segment.text}
        </span>
      ))}
    </>
  );
}

function ChangeCard({ change }: { change: TopicSummaryChange }) {
  const { t } = useTranslation();
  const parts: string[] = [];
  if (change.title.changed) parts.push(t("topics.change.title"));
  if (change.gist.changed) parts.push(t("topics.change.gist"));
  const loopCount =
    change.added_open_loops.length + change.removed_open_loops.length;
  if (loopCount > 0) {
    parts.push(t("topics.change.openLoops", { count: loopCount }));
  }
  const headline = change.initial
    ? t("topics.change.initial")
    : parts.length > 0
      ? parts.join(" · ")
      : t("topics.change.updated");

  return (
    <details className="admin-topic-change-card">
      <summary className="admin-topic-change-card__summary">
        <span className="admin-topic-change-card__rail" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <time className="text-xs text-muted-foreground">
            {formatDateTime(change.captured_at)}
          </time>
          <p className="mt-1 text-sm font-medium leading-5">{headline}</p>
          <div className="mt-2 flex flex-wrap gap-1">
            {change.title.changed ? (
              <Badge variant="info">{t("topics.change.badgeTitle")}</Badge>
            ) : null}
            {change.gist.changed ? (
              <Badge variant="success">{t("topics.change.badgeGist")}</Badge>
            ) : null}
            {loopCount > 0 ? (
              <Badge variant="warning">
                {t("topics.change.badgeOpenLoops")}
              </Badge>
            ) : null}
          </div>
        </div>
        <span className="admin-topic-change-card__chevron">
          <svg
            viewBox="0 0 24 24"
            className="size-4"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            aria-hidden="true"
          >
            <path d="m6 9 6 6 6-6" />
          </svg>
        </span>
      </summary>
      <div className="admin-topic-change-card__body">
        {change.title.changed ? (
          <div>
            <h4 className="mb-2 text-xs font-medium text-muted-foreground">
              {t("topics.change.badgeTitle")}
            </h4>
            <div className="admin-topic-inline-diff">
              <DiffSegments
                segments={change.title.segments}
                emptyLabel={t("topics.change.empty")}
              />
            </div>
          </div>
        ) : null}
        {change.gist.changed ? (
          <div>
            <h4 className="mb-2 text-xs font-medium text-muted-foreground">
              {t("topics.change.badgeGist")}
            </h4>
            <div className="admin-topic-inline-diff">
              <DiffSegments
                segments={change.gist.segments}
                emptyLabel={t("topics.change.empty")}
              />
            </div>
          </div>
        ) : null}
        {change.added_open_loops.length > 0 ? (
          <div>
            <h4 className="text-xs font-medium text-warning">
              {t("topics.change.added")}
            </h4>
            <ul className="mt-1 space-y-1">
              {change.added_open_loops.map((item, index) => (
                <li
                  key={`${item}-${index}`}
                  className="text-xs leading-5 text-foreground/75"
                >
                  + {item}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
        {change.removed_open_loops.length > 0 ? (
          <div>
            <h4 className="text-xs font-medium text-destructive">
              {t("topics.change.removed")}
            </h4>
            <ul className="mt-1 space-y-1">
              {change.removed_open_loops.map((item, index) => (
                <li
                  key={`${item}-${index}`}
                  className="text-xs leading-5 text-foreground/75"
                >
                  - {item}
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </div>
    </details>
  );
}

export default function TopicDetailPage() {
  const { t } = useTranslation();
  const { id } = useParams();
  const location = useLocation();
  const topicId = Number(id);
  const validTopicId = Number.isInteger(topicId) && topicId > 0;
  const query = useQuery({
    queryKey: ["topic", topicId],
    queryFn: () => topicApi.get(topicId),
    enabled: validTopicId,
  });
  const backTo =
    (location.state as { from?: string } | null)?.from || "/admin/topics";

  if (!validTopicId) {
    return <p className="text-sm text-muted-foreground">{t("common.notFound")}</p>;
  }
  if (query.isPending) return <PageSkeleton />;
  if (query.isError || !query.data) {
    return <p className="text-sm text-muted-foreground">{t("common.notFound")}</p>;
  }
  const data = query.data;
  const thread = data.thread;
  const summary = data.current_summary;
  const changes = data.summary_changes;

  return (
    <div className="space-y-6">
      <header className="flex flex-col gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <Link
            to={backTo}
            className="inline-flex size-8 items-center justify-center rounded-full border border-border text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            aria-label={t("topics.back")}
          >
            ←
          </Link>
          <h1 className="admin-page-title min-w-0 truncate font-semibold">
            {thread.title || t("topics.untitled", { id: thread.id })}
          </h1>
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 pl-1 text-xs text-muted-foreground">
          <Badge variant="outline">
            {conversationLabel(
              t,
              thread.conversation_kind,
              thread.target_id,
              "",
            )}
          </Badge>
          <Badge
            variant={
              thread.forgotten_at
                ? "destructive"
                : thread.status === "active"
                  ? "success"
                  : "ghost"
            }
          >
            {thread.forgotten_at
              ? t("topics.status.forgotten")
              : enumLabel(t, "topicStatus", thread.status)}
          </Badge>
          <Badge variant="outline">
            {t("topics.anchorsBadge", { count: thread.anchor_count })}
          </Badge>
          <Badge variant="outline">
            {t("topics.card.sources", {
              available: thread.source_available,
              total: thread.source_total,
            })}
          </Badge>
          <span>
            {thread.forgotten_at
              ? t("topics.card.forgotten")
              : t("topics.card.forgetAt", {
                  time: formatDateTime(thread.forget_at),
                })}
          </span>
          <span>{t("topics.updatedAt", { time: formatDateTime(thread.updated_at) })}</span>
        </div>
      </header>

      <div className="grid grid-cols-1 items-start gap-4 min-[1280px]:grid-cols-[minmax(0,1fr)_clamp(280px,24vw,340px)]">
        <div className="space-y-4">
          <Card>
            <div className="admin-card-stack admin-card-pad">
              <div className="flex items-center gap-2 text-primary">
                <h2 className="admin-card-title text-foreground">
                  {t("topics.currentSummary")}
                </h2>
                {data.current_summary_version > 0 ? (
                  <Badge variant="ghost">
                    v{data.current_summary_version}
                  </Badge>
                ) : null}
              </div>
              <p className="admin-prose">
                {summary?.gist || t("topics.noSummaryYet")}
              </p>
              {summary ? (
                <>
                  <div className="border-t border-border" />
                  <div>
                    <h3 className="text-xs font-semibold text-muted-foreground">
                      {t("topics.keywords")}
                    </h3>
                    <div className="mt-2 flex flex-wrap items-center gap-2">
                      <span className="text-xs text-muted-foreground">
                        {formatDateTime(thread.updated_at)}
                      </span>
                      {summary.keywords?.map((keyword) => (
                        <Badge key={keyword}>{keyword}</Badge>
                      ))}
                    </div>
                  </div>
                  {summary.participants?.length > 0 ? (
                    <div>
                      <h3 className="text-xs font-semibold text-muted-foreground">
                        {t("topics.participants")}
                      </h3>
                      <div className="mt-2 flex flex-wrap gap-2">
                        {summary.participants.map((participant) => (
                          <span
                            key={`${participant.user_id}-${participant.nickname}`}
                            className="rounded-lg border border-border px-2.5 py-1.5 text-xs font-medium text-foreground/75"
                          >
                            {participant.nickname || participant.user_id}
                          </span>
                        ))}
                      </div>
                    </div>
                  ) : null}
                  {summary.open_loops?.length > 0 ? (
                    <div>
                      <h3 className="text-xs font-semibold text-muted-foreground">
                        {t("topics.openLoops")}
                      </h3>
                      <ul className="mt-2 space-y-2">
                        {summary.open_loops.map((item) => (
                          <li
                            key={item}
                            className="flex items-start gap-2 text-sm"
                          >
                            <span className="mt-2 size-1.5 shrink-0 rounded-full bg-warning" />
                            <span className="admin-body-text text-foreground">
                              {item}
                            </span>
                          </li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                  {summary.recent_turns?.length > 0 ? (
                    <div>
                      <h3 className="text-xs font-semibold text-muted-foreground">
                        {t("topics.recentTurns")}
                      </h3>
                      <ul className="admin-body-text mt-2 space-y-1 text-foreground">
                        {summary.recent_turns.map((item, index) => (
                          <li key={`${item}-${index}`}>· {item}</li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                </>
              ) : null}
            </div>
          </Card>

          <Card>
            <div className="admin-card-stack admin-card-pad">
              <div>
                <h2 className="admin-card-title text-foreground">
                  {t("topics.relatedMessages")}
                </h2>
                <p className="mt-1 text-xs text-muted-foreground">
                  {t("topics.messageOrder")}
                </p>
                {data.messages.length >= data.message_limit ? (
                  <p className="mt-1 text-xs text-warning">
                    {t("topics.messageCap", { count: data.message_limit })}
                  </p>
                ) : null}
              </div>
              {data.messages.length === 0 ? (
                <div className="py-10 text-center text-sm text-muted-foreground">
                  {t("topics.noMessages")}
                </div>
              ) : (
                <div className="space-y-3">
                  {data.messages.map((message) => (
                    <article
                      key={message.id}
                      className={
                        message.recalled
                          ? "rounded-xl border border-destructive/25 bg-destructive/5 p-3"
                          : "rounded-xl border border-border bg-muted/50 p-3"
                      }
                    >
                      <div className="flex items-center justify-between gap-3">
                        <div className="flex min-w-0 items-center gap-2">
                          <span className="min-w-0 truncate text-xs font-medium text-foreground/75">
                            {message.sender
                              ? `${message.sender} (${message.user_id})`
                              : String(message.user_id)}
                          </span>
                          {message.anchor ? (
                            <Badge variant="warning">
                              {t("topics.messageFlags.anchor")}
                            </Badge>
                          ) : null}
                          {message.source ? (
                            <Badge>{t("topics.messageFlags.source")}</Badge>
                          ) : null}
                          {message.recalled ? (
                            <Badge variant="destructive">
                              {t("topics.messageFlags.recalled")}
                            </Badge>
                          ) : null}
                        </div>
                        <time className="shrink-0 text-xs text-muted-foreground">
                          {formatDateTime(message.message_time)}
                        </time>
                      </div>
                      <p
                        className={
                          message.recalled
                            ? "mt-1 whitespace-pre-wrap break-words text-sm leading-6 text-destructive"
                            : "admin-body-text mt-1 whitespace-pre-wrap break-words text-foreground"
                        }
                      >
                        {message.text || t("common.none")}
                      </p>
                    </article>
                  ))}
                </div>
              )}
            </div>
          </Card>
        </div>

        <aside className="space-y-4">
          <Card>
            <div className="admin-card-pad-compact space-y-3">
              <div>
                <h2 className="admin-card-title-compact text-foreground">
                  {t("topics.summaryHistory")}
                </h2>
                <p className="mt-1 text-xs text-muted-foreground">
                  {data.summary_snapshot_count === 0
                    ? t("topics.noSnapshots")
                    : changes.length === 0
                      ? t("topics.noChanges")
                      : t("topics.changeCount", { count: changes.length })}
                </p>
              </div>
              {data.summary_snapshot_count > 0 ? (
                <div className="admin-topic-summary-timeline">
                  {changes.map((change) => (
                    <ChangeCard
                      key={`${change.captured_at}-${change.initial}`}
                      change={change}
                    />
                  ))}
                </div>
              ) : null}
            </div>
          </Card>
        </aside>
      </div>
    </div>
  );
}
