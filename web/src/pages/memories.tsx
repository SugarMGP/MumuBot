import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation } from "react-router";
import { memberApi, memoryApi } from "@/api/client-endpoints";
import { EmptyState, FilterPanel } from "@/components/admin/list";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { Pagination } from "@/components/admin/pagination";
import { SortToolbar } from "@/components/admin/sort-toolbar";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import {
  Field,
  Input,
  NativeSelect,
} from "@/components/ui/form-controls";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useQueryParams } from "@/hooks/use-query-params";
import { formatDateTime } from "@/lib/format";
import { enumLabel, subjectText } from "@/lib/labels";

interface FilterState {
  subjectType: string;
  subjectTarget: string;
  memoryType: string;
  keyword: string;
  status: string;
}

export default function MemoriesPage() {
  const { t } = useTranslation();
  const location = useLocation();
  const { params, update, page } = useQueryParams();
  const userId = params.get("user_id") || "";
  const sort = params.get("sort") || "updated";
  const order = params.get("order") || "desc";
  const fromParams = (): FilterState => ({
    subjectType: params.get("subject_type") || "",
    subjectTarget: params.get("subject_target") || "",
    memoryType: params.get("memory_type") || "",
    keyword: params.get("keyword") || "",
    status: params.get("status") || "",
  });
  const [filters, setFilters] = useState<FilterState>(fromParams);
  const [drafts, setDrafts] = useState<FilterState>(fromParams);

  useEffect(() => {
    const next = {
      subjectType: params.get("subject_type") || "",
      subjectTarget: params.get("subject_target") || "",
      memoryType: params.get("memory_type") || "",
      keyword: params.get("keyword") || "",
      status: params.get("status") || "",
    };
    setFilters(next);
    setDrafts(next);
  }, [params]);

  const query = useQuery({
    queryKey: ["memories", filters, sort, order, page, userId],
    queryFn: () =>
      memoryApi.list({
        subject_type: filters.subjectType,
        subject_target: filters.subjectTarget,
        memory_type: filters.memoryType,
        status: filters.status,
        keyword: filters.keyword,
        user_id: userId ? Number(userId) : undefined,
        sort,
        order,
        page,
        page_size: 15,
      }),
  });

  const memberQuery = useQuery({
    queryKey: ["member", userId],
    queryFn: () => memberApi.list({ keyword: userId, page: 1, page_size: 20 }),
    enabled: Boolean(userId),
  });
  const memberName = memberQuery.data?.items.find(
    (item) => String(item.user_id) === userId,
  );

  const submitFilters = (event: React.FormEvent) => {
    event.preventDefault();
    setFilters(drafts);
    update(
      {
        subject_type: drafts.subjectType,
        subject_target: drafts.subjectTarget,
        memory_type: drafts.memoryType,
        status: drafts.status,
        keyword: drafts.keyword,
      },
      { resetPage: true },
    );
  };

  return (
    <div className="space-y-6">
      <PageHeader title={t("memories.title")} subtitle={t("memories.subtitle")} />

      <FilterPanel>
        <form className="flex flex-wrap items-end gap-3" onSubmit={submitFilters}>
          <Field label={t("memories.filters.subjectType")} className="w-40">
            <NativeSelect
              value={drafts.subjectType}
              onChange={(event) =>
                setDrafts((value) => ({ ...value, subjectType: event.target.value }))
              }
              aria-label={t("memories.filters.subjectType")}
            >
              <option value="">{t("memories.filters.allSubjects")}</option>
              {["group", "user", "self", "object"].map((value) => (
                <option key={value} value={value}>
                  {enumLabel(t, "subjectType", value)}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label={t("memories.filters.subjectTarget")} className="w-44">
            <Input
              value={drafts.subjectTarget}
              placeholder={t("memories.filters.subjectTargetPlaceholder")}
              onChange={(event) =>
                setDrafts((value) => ({
                  ...value,
                  subjectTarget: event.target.value,
                }))
              }
            />
          </Field>
          <Field label={t("memories.filters.memoryType")} className="w-40">
            <NativeSelect
              value={drafts.memoryType}
              onChange={(event) =>
                setDrafts((value) => ({ ...value, memoryType: event.target.value }))
              }
              aria-label={t("memories.filters.memoryType")}
            >
              <option value="">{t("memories.filters.allTypes")}</option>
              {["profile", "experience"].map((value) => (
                <option key={value} value={value}>
                  {enumLabel(t, "memoryType", value)}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label={t("memories.filters.status")} className="w-36">
            <NativeSelect
              value={drafts.status}
              onChange={(event) =>
                setDrafts((value) => ({ ...value, status: event.target.value }))
              }
              aria-label={t("memories.filters.status")}
            >
              <option value="">{t("memories.filters.allStatuses")}</option>
              {["active", "archived"].map((value) => (
                <option key={value} value={value}>
                  {enumLabel(t, "memoryStatus", value)}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label={t("memories.filters.keyword")} className="min-w-56 flex-1">
            <Input
              type="search"
              value={drafts.keyword}
              placeholder={t("memories.filters.keywordPlaceholder")}
              onChange={(event) =>
                setDrafts((value) => ({ ...value, keyword: event.target.value }))
              }
            />
          </Field>
          <Button type="submit">
            {t("common.filter")}
          </Button>
        </form>
        {userId ? (
          <div className="flex items-center gap-2">
            <Badge variant="outline">
              {memberName
                ? `${memberName.nickname || t("members.qq", { id: userId })} · ${userId}`
                : `${t("members.qq", { id: userId })}`}
            </Badge>
            <Button
              variant="ghost"
              size="xs"
              onClick={() => update({ user_id: undefined }, { resetPage: true })}
            >
              {t("common.cancel")}
            </Button>
          </div>
        ) : null}
      </FilterPanel>

      <SortToolbar
        options={[
          { key: "updated", label: t("common.updatedAt") },
          { key: "created", label: t("common.createdAt") },
        ]}
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
        <EmptyState message={t("memories.empty")} />
      ) : (
        <Card className="admin-card-pad overflow-hidden">
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="w-[48%]">
                  {t("memories.columns.content")}
                </TableHead>
                <TableHead className="w-[22%]">
                  {t("memories.columns.subject")}
                </TableHead>
                <TableHead className="w-[10%]">
                  {t("memories.columns.status")}
                </TableHead>
                <TableHead className="w-[10%]">
                  {t("memories.columns.pinned")}
                </TableHead>
                <TableHead className="hidden w-[10%] min-[1200px]:table-cell">
                  {t("memories.columns.updatedAt")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {query.data.items.map((item) => (
                <TableRow key={item.id}>
                  <TableCell>
                    <Link
                      to={`/admin/memories/${item.id}`}
                      state={{ from: location.pathname + location.search }}
                      className="block font-medium text-foreground hover:text-primary"
                    >
                      {item.label ? (
                        <>
                          <span className="font-medium">{item.label}</span>
                          <p className="mt-1 line-clamp-3 text-sm font-normal leading-6 text-foreground/75">
                            {item.content}
                          </p>
                        </>
                      ) : (
                        <span className="line-clamp-3 leading-6">
                          {item.content}
                        </span>
                      )}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <p className="break-words font-medium">
                      {subjectText(t, item.subject_type, item.subject_target)}
                    </p>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {enumLabel(t, "memoryType", item.memory_type)}
                      {" · "}
                      {enumLabel(t, "subjectType", item.subject_type)}
                    </p>
                  </TableCell>
                  <TableCell className="whitespace-nowrap">
                    <Badge
                      variant={item.status === "archived" ? "ghost" : "success"}
                    >
                      {enumLabel(t, "memoryStatus", item.status)}
                    </Badge>
                  </TableCell>
                  <TableCell className="whitespace-nowrap">
                    {item.pinned ? (
                      <Badge>{t("memories.pinned")}</Badge>
                    ) : (
                      <span className="text-xs text-muted-foreground/60">
                        {t("memories.notPinned")}
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="hidden whitespace-nowrap text-xs text-muted-foreground min-[1200px]:table-cell">
                    {formatDateTime(item.updated_at)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
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
