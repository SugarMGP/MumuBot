import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { memoryApi, type MemoryUpdate } from "@/api/client-endpoints";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { toastApiError, toastSuccess } from "@/components/admin/toast";
import { ConfirmDialog } from "@/components/admin/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, Textarea } from "@/components/ui/form-controls";
import { Spinner } from "@/components/ui/spinner";
import { DirtyGuardDialog, useDirtyGuard } from "@/hooks/use-dirty-guard";
import { ApiError } from "@/api/client";
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
  const navigate = useNavigate();
  const [draft, setDraft] = useState<{ content: string; original: string; version: number } | null>(null);
  const [discard, setDiscard] = useState<"close" | "reload" | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [editError, setEditError] = useState("");
  const blocker = useDirtyGuard(draft !== null && draft.content !== draft.original);
  const memoryId = Number(id);
  const validMemoryId = Number.isInteger(memoryId) && memoryId > 0;
  const query = useQuery({
    queryKey: ["memory", memoryId],
    queryFn: () => memoryApi.get(memoryId),
    enabled: validMemoryId,
  });
  const [archiveOpen, setArchiveOpen] = useState(false);

  const mutation = useMutation({
    mutationFn: (update: MemoryUpdate) => memoryApi.update(memoryId, update),
    onSuccess: async (_, update) => {
      if (update.content !== undefined) setDraft(null);
      setEditError("");
      setArchiveOpen(false);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["memory", memoryId] }),
        queryClient.invalidateQueries({ queryKey: ["memories"] }),
        queryClient.invalidateQueries({ queryKey: ["members"] }),
        queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
      ]);
      toastSuccess(t(update.status ? "memories.archived" : "memories.saved"));
    },
    onError: (error) => {
      if (draft) setEditError(error instanceof ApiError ? error.code : "unknown");
      toastApiError(error);
    },
  });

  const backTo =
    (location.state as { from?: string } | null)?.from || "/admin/memories";

  const deletion = useMutation({
    mutationFn: () => memoryApi.remove(memoryId),
    onSuccess: async () => {
      setDeleteOpen(false);
      navigate(backTo, { replace: true });
      queryClient.removeQueries({ queryKey: ["memory", memoryId] });
      await Promise.all(["memories", "members", "dashboard"].map((key) => queryClient.invalidateQueries({ queryKey: [key] })));
      toastSuccess(t("memories.deleted"));
    },
    onError: toastApiError,
  });

  if (!validMemoryId) {
    return <p className="text-sm text-muted-foreground">{t("common.notFound")}</p>;
  }
  if (query.isPending) return <PageSkeleton />;
  if (!query.data) {
    return <p className="text-sm text-muted-foreground">{t("common.notFound")}</p>;
  }
  const { item, revisions } = query.data;

  return (
    <div className="space-y-6">
      {query.isRefetchError ? <p role="alert" className="text-sm text-destructive">{t("common.refreshFailed")}</p> : null}
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
          {item.status === "active" ? <>
            <Button size="sm" variant="outline" disabled={mutation.isPending} onClick={() => { setEditError(""); setDraft({ content: item.content, original: item.content, version: item.version }); }}>{t("memories.edit")}</Button>
            <Button size="sm" variant="outline" disabled={mutation.isPending} onClick={() => mutation.mutate({ version: item.version, pinned: !item.pinned })}>
              {mutation.isPending && mutation.variables.pinned !== undefined ? <Spinner /> : null}
              {t(item.pinned ? "memories.unpin" : "memories.pin")}
            </Button>
            <Button size="sm" variant="outline" onClick={() => setArchiveOpen(true)} disabled={mutation.isPending}>{t("memories.archive")}</Button>
          </> : <Button size="sm" variant="destructive" disabled={deletion.isPending} onClick={() => setDeleteOpen(true)}>{t("memories.delete")}</Button>}

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
        onConfirm={() => mutation.mutate({ version: item.version, status: "archived" })}
      />
      <Dialog open={draft !== null} onOpenChange={(open) => {
        if (open || mutation.isPending) return;
        if (draft && draft.content !== draft.original) setDiscard("close"); else setDraft(null);
      }}>
        <DialogContent>
          <DialogHeader><DialogTitle>{t("memories.edit")}</DialogTitle><DialogDescription>{t("memories.editHint")}</DialogDescription></DialogHeader>
          <Field label={t("memories.content")}>
            <Textarea rows={10} value={draft?.content ?? ""} disabled={mutation.isPending} aria-label={t("memories.content")}
              onChange={(event) => setDraft((value) => value ? { ...value, content: event.target.value } : value)} />
          </Field>
          {editError ? <p role="alert" className="text-sm text-destructive">{t(`errors.${editError}`, { defaultValue: t("errors.unknown") })}</p> : null}
          {draft && draft.version !== item.version ? <p role="alert" className="text-sm text-destructive">{t("errors.version_conflict")}</p> : null}
          <DialogFooter>
            <Button variant="ghost" disabled={mutation.isPending} onClick={() => setDiscard("reload")}>{t("memories.reload")}</Button>
            <Button disabled={mutation.isPending || !draft?.content.trim() || Array.from(draft.content.trim()).length > 8000 || draft.content === draft.original}
              onClick={() => draft && mutation.mutate({ version: draft.version, content: draft.content.trim() })}>
              {mutation.isPending ? <Spinner /> : null}{t("common.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <ConfirmDialog open={discard !== null} onOpenChange={(open) => !open && setDiscard(null)} title={t("common.unsavedTitle")} description={t("common.unsavedBody")} confirmLabel={t("memories.discard")}
        onConfirm={() => {
          if (discard === "reload") {
            void query.refetch().then((result) => {
              if (result.data && !result.isError) {
                const fresh = result.data.item;
                setDraft(fresh.status === "active" ? { content: fresh.content, original: fresh.content, version: fresh.version } : null);
                setEditError("");
              }
              else toastApiError(result.error);
            });
          } else setDraft(null);
          setDiscard(null);
        }} />
      <ConfirmDialog open={deleteOpen} onOpenChange={setDeleteOpen} title={t("memories.deleteTitle")} description={t("memories.deleteBody")} confirmLabel={t("memories.delete")} loading={deletion.isPending} onConfirm={() => deletion.mutate()} />
      <DirtyGuardDialog blocker={blocker} />

    </div>
  );
}
