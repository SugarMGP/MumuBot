import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { memberApi } from "@/api/client-endpoints";
import type { Member } from "@/api/types";
import { EmptyState, FilterPanel } from "@/components/admin/list";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { Pagination } from "@/components/admin/pagination";
import { SortToolbar } from "@/components/admin/sort-toolbar";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Field, Input } from "@/components/ui/form-controls";
import { useQueryParams } from "@/hooks/use-query-params";
import { formatDateTime } from "@/lib/format";

function primaryName(member: Member): string {
  const nickname = member.nickname.trim();
  if (nickname) return nickname;
  const first = member.names[0]?.value?.trim();
  if (first) return first;
  return String(member.user_id);
}

function initial(member: Member): string {
  const name = primaryName(member);
  return Array.from(name)[0] || "?";
}

export default function MembersPage() {
  const { t } = useTranslation();
  const { params, update, page } = useQueryParams();
  const keyword = params.get("keyword") || "";
  const sort = params.get("sort") || "updated";
  const order = params.get("order") || "desc";
  const [input, setInput] = useState(keyword);

  useEffect(() => {
    setInput(params.get("keyword") || "");
  }, [params]);

  const query = useQuery({
    queryKey: ["members", keyword, sort, order, page],
    queryFn: () =>
      memberApi.list({ keyword, sort, order, page, page_size: 15 }),
  });

  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    update({ keyword: input }, { resetPage: true });
  };

  return (
    <div className="admin-memory-page space-y-6">
      <PageHeader title={t("members.title")} subtitle={t("members.subtitle")} />

      <FilterPanel title={t("common.filter")}>
        <form className="flex items-end gap-3" onSubmit={submit}>
          <Field label={t("members.filters.keyword")} className="flex-1">
            <Input
              type="search"
              value={input}
              placeholder={t("members.filters.keywordPlaceholder")}
              onChange={(event) => setInput(event.target.value)}
            />
          </Field>
          <Button type="submit">{t("common.filter")}</Button>
        </form>
      </FilterPanel>

      <SortToolbar
        options={[{ key: "updated", label: t("common.updatedAt") }]}
        currentSort={sort}
        currentOrder={order}
        onSort={(key) => update({ sort: key }, { resetPage: true })}
        onOrder={(value) => update({ order: value }, { resetPage: true })}
      />

      {query.isPending ? (
        <PageSkeleton />
      ) : query.isError ? (
        <EmptyState message={t("common.loadFailed")} />
      ) : query.data.items.length === 0 ? (
        <EmptyState message={t("members.empty")} />
      ) : (
        <div className="space-y-3">
          {query.data.items.map((member) => {
            const name = primaryName(member);
            const visibleNames = member.names.slice(0, 2);
            const hiddenNames = member.names.slice(2);
            return (
              <Card key={member.user_id}>
                <div className="admin-member-row">
                  <section className="flex min-w-0 items-center gap-3">
                    <div className="grid size-10 shrink-0 place-content-center rounded-full bg-primary/10 text-sm font-semibold text-primary">
                      {initial(member)}
                    </div>
                    <div className="min-w-0">
                      <h2 className="break-words text-lg font-semibold text-foreground">
                        {name}
                      </h2>
                      <p className="admin-number mt-1 text-xs text-muted-foreground">
                        {t("members.qq", { id: member.user_id })}
                      </p>
                      {!member.nickname && member.names.length > 0 ? (
                        <span className="text-xs text-muted-foreground">
                          {t("members.names")}
                        </span>
                      ) : null}
                      {member.names.length > 0 ? (
                        hiddenNames.length > 0 ? (
                          <details className="admin-member-names mt-1">
                            <summary
                              aria-label={t("members.otherNamesAria", {
                                name,
                              })}
                            >
                              {visibleNames.map((entry, index) => (
                                <span
                                  key={`${entry.group_id}-${entry.value}`}
                                  className="block truncate text-xs text-muted-foreground"
                                  title={entry.value}
                                >
                                  {entry.value}
                                  {index === visibleNames.length - 1 ? (
                                    <span className="text-primary">
                                      {" "}
                                      (+{hiddenNames.length})
                                    </span>
                                  ) : null}
                                </span>
                              ))}
                            </summary>
                            <div className="admin-names-popover">
                              <p className="mb-2 text-xs font-semibold text-foreground/75">
                                {t("members.moreNames")}
                              </p>
                              {hiddenNames.map((entry) => (
                                <div
                                  key={`${entry.group_id}-${entry.value}`}
                                  className="border-t border-border py-2"
                                >
                                  <p className="break-words text-sm">
                                    {entry.value}
                                  </p>
                                  <span className="text-xs text-muted-foreground">
                                    {t("members.groupLabel", {
                                      id: entry.group_id,
                                    })}
                                  </span>
                                </div>
                              ))}
                            </div>
                          </details>
                        ) : (
                          <div className="mt-1 space-y-1">
                            {visibleNames.map((entry) => (
                              <span
                                key={`${entry.group_id}-${entry.value}`}
                                className="block truncate text-xs text-muted-foreground"
                                title={entry.value}
                              >
                                {entry.value}
                              </span>
                            ))}
                          </div>
                        )
                      ) : null}
                    </div>
                  </section>
                  <section className="grid grid-cols-1 gap-3 text-sm">
                    <Link
                      to={`/admin/memories?user_id=${member.user_id}`}
                      className="admin-member-stat text-primary"
                    >
                      <span>{t("members.knowledge")}</span>
                      <strong>{member.knowledge_count}</strong>
                      <span aria-hidden="true">→</span>
                    </Link>
                    <span className="text-right text-xs text-muted-foreground/60">
                      {formatDateTime(member.updated_at)}
                    </span>
                  </section>
                </div>
              </Card>
            );
          })}
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
