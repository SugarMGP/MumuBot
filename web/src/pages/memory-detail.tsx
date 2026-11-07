import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useParams } from "react-router";
import { memoryApi } from "@/api/client-endpoints";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { toastApiError, toastSuccess } from "@/components/admin/toast";
import { ConfirmDialog } from "@/components/admin/confirm-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
  const [archiveOpen, setArchiveOpen] = useState(false);

  const mutation = useMutation({
    mutationFn: () => memoryApi.updateStatus(memoryId, "archived"),
    onSuccess: async () => {
      setArchiveOpen(false);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["memory", memoryId] }),
        queryClient.invalidateQueries({ queryKey: ["memories"] }),
      ]);
      toastSuccess(t("memories.archived"));
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
    <div className="space-y-6">
      <Link
        to={backTo}
        className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-primary"
      >
        ← {t("memories.back")}
      </Link>

      <div className="flex flex-wrap items-start justify-between gap-4">
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
          {item.status === "active" ? (
            <Button
              size="sm"
              variant="outline"
              onClick={() => setArchiveOpen(true)}
              disabled={mutation.isPending}
            >
              {t("memories.archive")}
            </Button>
          ) : null}
        </div>
      </div>

      <Card>
        <div className="admin-card-pad">
          <p className="admin-prose whitespace-pre-wrap break-words">
            {item.content}
          </p>
        </div>
      </Card>

      <Card>
        <div className="admin-card-pad">
          <div className="flex flex-wrap gap-x-6 gap-y-2 text-sm">
            <span className="text-muted-foreground">
              {item.status === "archived"
                ? t("memories.archivedHint")
                : item.pinned
                  ? t("memories.pinnedForever")
                  : t("memories.forgetAt", {
                      time: item.forget_at
                        ? formatDateTime(item.forget_at)
                        : t("memories.noDeadline"),
                    })}
            </span>
            <span className="text-muted-foreground">
              {t("memories.halfLife", { days: item.stability_days })}
            </span>
          </div>
        </div>
      </Card>

      <Card>
        <div className="admin-card-stack admin-card-pad">
          <h2 className="admin-card-title text-foreground">
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
      <ConfirmDialog
        open={archiveOpen}
        onOpenChange={setArchiveOpen}
        title={t("memories.archiveTitle")}
        description={t("memories.archiveBody")}
        confirmLabel={t("memories.archive")}
        loading={mutation.isPending}
        destructive
        onConfirm={() => mutation.mutate()}
      />
    </div>
  );
}
