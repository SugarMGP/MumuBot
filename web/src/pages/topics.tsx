import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation } from "react-router";
import { topicApi } from "@/api/client-endpoints";
import { EmptyState, FilterPanel } from "@/components/admin/list";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { Pagination } from "@/components/admin/pagination";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, NativeSelect } from "@/components/ui/form-controls";
import { useQueryParams } from "@/hooks/use-query-params";
import { formatDateTime } from "@/lib/format";
import { conversationLabel, enumLabel } from "@/lib/labels";

export default function TopicsPage() {
  const { t } = useTranslation();
  const location = useLocation();
  const { params, update, page } = useQueryParams();
  const conversation = params.get("conversation") || "";
  const status = params.get("status") || "";
  const [keyword, setKeyword] = useState(params.get("keyword") || "");

  useEffect(() => {
    setKeyword(params.get("keyword") || "");
  }, [params]);

  const conversations = useQuery({
    queryKey: ["topic-conversations"],
    queryFn: topicApi.conversations,
    staleTime: 60_000,
  });

  const query = useQuery({
    queryKey: ["topics", conversation, status, params.get("keyword"), page],
    queryFn: () =>
      topicApi.list({
        conversation,
        status,
        keyword: params.get("keyword") || "",
        page,
        page_size: 12,
      }),
  });

  const options = conversations.data?.items ?? [];

  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    update({ keyword, conversation, status }, { resetPage: true });
  };

  return (
    <div className="space-y-6">
      <PageHeader title={t("topics.title")} subtitle={t("topics.subtitle")} />

      <FilterPanel>
        <form
          className="grid grid-cols-[minmax(170px,240px)_160px_minmax(0,1fr)_auto] items-end gap-3"
          onSubmit={submit}
        >
          <Field label={t("topics.filters.conversation")}>
            <NativeSelect
              value={conversation}
              onChange={(event) =>
                update(
                  { conversation: event.target.value, keyword },
                  { resetPage: true },
                )
              }
              aria-label={t("topics.filters.conversation")}
            >
              <option value="">{t("topics.filters.allConversations")}</option>
              {options.map((option) => (
                <option
                  key={`${option.kind}:${option.target_id}`}
                  value={`${option.kind}:${option.target_id}`}
                >
                  {conversationLabel(
                    t,
                    option.kind,
                    option.target_id,
                    option.name,
                  )}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label={t("topics.filters.status")}>
            <NativeSelect
              value={status}
              onChange={(event) =>
                update(
                  { status: event.target.value, keyword },
                  { resetPage: true },
                )
              }
              aria-label={t("topics.filters.status")}
            >
              <option value="">{t("topics.filters.allStatuses")}</option>
              <option value="active">{t("topics.status.active")}</option>
              <option value="dormant">{t("topics.status.dormant")}</option>
              <option value="forgotten">{t("topics.status.forgotten")}</option>
            </NativeSelect>
          </Field>
          <Field label={t("topics.filters.keyword")}>
            <Input
              type="search"
              value={keyword}
              placeholder={t("topics.filters.keywordPlaceholder")}
              onChange={(event) => setKeyword(event.target.value)}
            />
          </Field>
          <Button type="submit">{t("common.filter")}</Button>
        </form>
      </FilterPanel>

      {query.isPending ? (
        <PageSkeleton />
      ) : query.isError ? (
        <EmptyState message={t("common.loadFailed")} />
      ) : query.data.items.length === 0 ? (
        <EmptyState message={t("topics.empty")} />
      ) : (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(380px,1fr))] gap-4">
          {query.data.items.map((item) => (
            <Link
              key={item.id}
              to={`/admin/topics/${item.id}`}
              state={{ from: location.pathname + location.search }}
              className="admin-record-card admin-card-pad rounded-xl border border-border bg-card pl-6 shadow-card transition-[border-color,box-shadow,background-color] duration-150"
              data-tone={
                item.status === "active" && !item.forgotten_at
                  ? "success"
                  : undefined
              }
            >
              <div className="flex items-start justify-between gap-4">
                <div className="min-w-0">
                  <h2 className="admin-card-title line-clamp-2 leading-6 text-foreground">
                    {item.title || t("topics.untitled", { id: item.id })}
                  </h2>
                  <p className="admin-number mt-1 text-xs text-muted-foreground">
                    {conversationLabel(
                      t,
                      item.conversation_kind,
                      item.target_id,
                      "",
                    )}
                    {" · #"}
                    {item.id}
                  </p>
                </div>
                <Badge
                  variant={
                    item.forgotten_at
                      ? "destructive"
                      : item.status === "active"
                        ? "success"
                        : "ghost"
                  }
                  className="shrink-0"
                >
                  {item.forgotten_at
                    ? t("topics.status.forgotten")
                    : enumLabel(t, "topicStatus", item.status)}
                </Badge>
              </div>
              <p className="admin-body-text mt-3 line-clamp-3 text-foreground/75">
                {item.gist || t("topics.noSummary")}
              </p>
              <div className="mt-3 flex flex-wrap gap-2">
                {item.keywords.slice(0, 3).map((keywordValue) => (
                  <Badge key={keywordValue}>{keywordValue}</Badge>
                ))}
              </div>
              <div className="mt-4 flex items-center justify-between border-t border-border pt-3 text-xs text-muted-foreground">
                <span className="flex flex-col gap-0.5">
                  <span>
                    {formatDateTime(item.updated_at)}
                    {" · "}
                    {t("topics.card.messages", { count: item.message_count })}
                    {" · "}
                    {t("topics.card.anchors", { count: item.anchor_count })}
                    {" · "}
                    {t("topics.card.openLoops", { count: item.open_loop_count })}
                  </span>
                  <span>
                    {item.forgotten_at
                      ? t("topics.card.forgotten")
                      : t("topics.card.forgetAt", {
                          time: formatDateTime(item.forget_at),
                        })}
                    {" · "}
                    {t("topics.card.sources", {
                      available: item.source_available,
                      total: item.source_total,
                    })}
                  </span>
                </span>
                <span className="font-medium text-primary">
                  {t("topics.card.detail")} →
                </span>
              </div>
            </Link>
          ))}
        </div>
      )}

      {query.data ? (
        <Pagination
          page={query.data.page}
          pageSize={query.data.page_size}
          total={query.data.total}
          onPage={(next) => update({ page: next })}
        />
      ) : null}
    </div>
  );
}
