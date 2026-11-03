import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useParams } from "react-router";
import { memoryApi } from "@/api/client-endpoints";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { toastApiError, toastSuccess } from "@/components/admin/toast";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/form-controls";
import { Card } from "@/components/ui/card";
import { formatDateTime } from "@/lib/format";
import { enumLabel, subjectText } from "@/lib/labels";

export default function MemoryDetailPage() {
  const { t } = useTranslation();
  const { id } = useParams();
  const location = useLocation();
  const queryClient = useQueryClient();
  const memoryId = Number(id);
  const validMemoryId = Number.isInteger(memoryId) && memoryId > 0;
  const query = useQuery({
    queryKey: ["memory", memoryId],
    queryFn: () => memoryApi.get(memoryId),
    enabled: validMemoryId,
  });
  const [status, setStatus] = useState("active");

  useEffect(() => {
    if (query.data) setStatus(query.data.item.status);
  }, [query.data]);

  const mutation = useMutation({
    mutationFn: (next: string) => memoryApi.updateStatus(memoryId, next),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["memory", memoryId] }),
        queryClient.invalidateQueries({ queryKey: ["memories"] }),
      ]);
      toastSuccess(t("memories.statusUpdated"));
    },
    onError: (error) => toastApiError(error),
  });

  const backTo =
    (location.state as { from?: string } | null)?.from || "/admin/memories";

  if (!validMemoryId) {
    return <p className="text-sm text-muted-foreground">{t("common.notFound")}</p>;
  }
  if (query.isPending) return <PageSkeleton />;
  if (query.isError || !query.data) {
    return <p className="text-sm text-muted-foreground">{t("common.notFound")}</p>;
  }
  const { item, revisions } = query.data;

  return (
    <div className="admin-memory-page space-y-6">
      <Link
        to={backTo}
        className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-primary"
      >
        ← {t("memories.back")}
      </Link>

      <div className="flex items-start justify-between gap-6">
        <PageHeader
          title={item.label || item.content.slice(0, 40)}
          subtitle={`${enumLabel(t, "memoryType", item.memory_type)} · ${subjectText(t, item.subject_type, item.subject_target)}`}
          tag={
            <span className="flex items-center gap-2">
              <Badge variant={item.status === "archived" ? "ghost" : "success"}>
                {enumLabel(t, "memoryStatus", item.status)}
              </Badge>
              {item.pinned ? <Badge>{t("memories.pinnedBadge")}</Badge> : null}
            </span>
          }
        />
        <div className="flex shrink-0 items-center gap-2">
          <NativeSelect
            className="h-8 w-32"
            value={status}
            onChange={(event) => setStatus(event.target.value)}
            aria-label={t("memories.columns.status")}
          >
            {["active", "archived"].map((value) => (
              <option key={value} value={value}>
                {enumLabel(t, "memoryStatus", value)}
              </option>
            ))}
          </NativeSelect>
          <Button
            size="sm"
            onClick={() => mutation.mutate(status)}
            disabled={mutation.isPending || status === item.status}
          >
            {mutation.isPending
              ? t("memories.updating")
              : t("memories.updateStatus")}
          </Button>
        </div>
      </div>

      <Card>
        <div className="p-5">
          <p className="whitespace-pre-wrap break-words text-base leading-8">
            {item.content}
          </p>
        </div>
      </Card>

      <Card>
        <div className="space-y-4 p-5">
          <h2 className="text-lg font-semibold text-foreground">
            {t("memories.revisions")}
          </h2>
          {revisions.length === 0 ? (
            <p className="rounded-xl border border-dashed border-border px-4 py-6 text-center text-sm text-muted-foreground">
              {t("memories.noRevisions")}
            </p>
          ) : (
            <div className="space-y-2">
              {revisions.map((revision) => (
                <details
                  key={revision.id}
                  className="rounded-xl border border-border bg-muted/50 px-4 py-3"
                >
                  <summary className="flex cursor-pointer items-center justify-between gap-3 text-sm">
                    <span>
                      {t("memories.version", { version: revision.version })}
                    </span>
                    <time className="text-xs text-muted-foreground">
                      {formatDateTime(revision.created_at)}
                    </time>
                  </summary>
                  <div className="mt-3 space-y-2 text-sm leading-6">
                    {revision.after_content ? (
                      <p className="whitespace-pre-wrap break-words">
                        {revision.after_content}
                      </p>
                    ) : null}
                    {revision.reason ? (
                      <p className="text-xs text-muted-foreground">
                        {t("memories.reason", { reason: revision.reason })}
                      </p>
                    ) : null}
                  </div>
                </details>
              ))}
            </div>
          )}
        </div>
      </Card>
    </div>
  );
}
