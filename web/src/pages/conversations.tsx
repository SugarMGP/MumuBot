import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { conversationApi } from "@/api/client-endpoints";
import type { Conversation, ConversationsData } from "@/api/types";
import { EmptyState } from "@/components/admin/list";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { toastApiError, toastSuccess } from "@/components/admin/toast";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Card } from "@/components/ui/card";
import { Checkbox, Textarea } from "@/components/ui/form-controls";
import { cn } from "@/lib/cn";
import { formatDateTime } from "@/lib/format";
import { ConfirmDialog } from "@/components/admin/confirm-dialog";
import { DirtyGuardDialog, useDirtyGuard } from "@/hooks/use-dirty-guard";

function ConversationRow({
  conversation,
  busy,
  compacting,
  selected,
  onSelect,
  onBlockedChange,
  onPromptSaved,
  onCompact,
  onDirtyChange,
}: {
  conversation: Conversation;
  busy: boolean;
  compacting: boolean;
  selected: boolean;
  onSelect: (selected: boolean) => void;
  onBlockedChange: (blocked: boolean) => void;
  onPromptSaved: (prompt: string) => Promise<boolean>;
  onCompact: () => void;
  onDirtyChange: (key: string, dirty: boolean) => void;
}) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [prompt, setPrompt] = useState(conversation.extra_prompt);
  const [discardOpen, setDiscardOpen] = useState(false);
  const [compactOpen, setCompactOpen] = useState(false);
  const [saveFailed, setSaveFailed] = useState(false);
  const dirty = editing && prompt !== conversation.extra_prompt;
  const key = `${conversation.kind}:${conversation.target_id}`;
  useEffect(() => {
    onDirtyChange(key, dirty);
    return () => onDirtyChange(key, false);
  }, [dirty, key, onDirtyChange]);
  const closeEditor = () => {
    setPrompt(conversation.extra_prompt);
    setEditing(false);
    setSaveFailed(false);
  };
  const requestClose = () => {
    if (saving) return;
    if (dirty) setDiscardOpen(true);
    else closeEditor();
  };

  const label = conversation.name || conversation.remote_remark || "";

  return (
    <div className={cn(
      "-mx-4 flex flex-col gap-3 px-4 py-3 transition-colors hover:bg-muted/40",
      selected && "bg-primary/5",
    )}>
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <Checkbox
            checked={selected}
            disabled={busy}
            onCheckedChange={(checked) => onSelect(checked === true)}
            aria-label={t("contacts.selectConversation", { name: label || conversation.target_id })}
          />
          <div className="min-w-0">
            <p className="truncate text-sm font-medium text-foreground">
              {label || t("contacts.idLabel", { id: conversation.target_id })}
            </p>
            <p className="admin-number mt-0.5 text-xs text-muted-foreground">
              {conversation.target_id}
              {" · "}
              {formatDateTime(conversation.updated_at)}
            </p>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Badge variant={conversation.active ? "success" : "ghost"}>
            {conversation.active ? t("contacts.active") : t("contacts.inactive")}
          </Badge>
          {conversation.blocked ? (
            <Badge variant="destructive">{t("contacts.blocked")}</Badge>
          ) : null}
          <Button
            variant={conversation.blocked ? "outline" : "ghost"}
            size="xs"
            disabled={busy}
            onClick={() => onBlockedChange(!conversation.blocked)}
          >
            {conversation.blocked ? t("contacts.unblock") : t("contacts.block")}
          </Button>
          {conversation.kind === "group" ? (
            <Button
              variant="ghost"
              size="xs"
              disabled={saving || busy}
              onClick={() => {
                if (editing) requestClose();
                else { setPrompt(conversation.extra_prompt); setEditing(true); }
              }}
            >
              {t("contacts.editPrompt")}
            </Button>
          ) : null}
          {conversation.active ? (
            <Button
              variant="ghost"
              size="xs"
              disabled={busy || compacting || saving}
              onClick={() => setCompactOpen(true)}
            >
              {compacting ? <Spinner /> : null}
              {t("contacts.compactNow")}
            </Button>
          ) : null}
        </div>
      </div>
      {conversation.kind === "group" ? (
        editing ? (
          <div className="space-y-2">
            <Textarea
              rows={2}
              value={prompt}
              placeholder={t("contacts.extraPromptPlaceholder")}
              onChange={(event) => setPrompt(event.target.value)}
            />
            {saveFailed ? <p role="alert" className="text-xs text-destructive">{t("contacts.saveFailed")}</p> : null}
            <div className="flex justify-end gap-2">
              <Button
                variant="ghost"
                size="xs"
                disabled={saving || busy}
                onClick={requestClose}
              >
                {t("common.cancel")}
              </Button>
              <Button
                size="xs"
                disabled={saving || busy}
                onClick={async () => {
                  setSaving(true);
                  const saved = await onPromptSaved(prompt);
                  setSaving(false);
                  setSaveFailed(!saved);
                  if (saved) setEditing(false);
                }}
              >
                {saving ? <Spinner /> : null}
                {t("contacts.savePrompt")}
              </Button>
            </div>
          </div>
        ) : conversation.extra_prompt ? (
          <p className="rounded-lg bg-muted/60 px-3 py-2 text-xs leading-5 text-foreground/75">
            {conversation.extra_prompt}
          </p>
        ) : (
          <p className="text-xs text-muted-foreground/60">
            {t("contacts.extraPromptEmpty")}
          </p>
        )
      ) : null}
      <ConfirmDialog open={discardOpen} onOpenChange={setDiscardOpen} title={t("common.unsavedTitle")} description={t("common.unsavedBody")} confirmLabel={t("common.leave")} destructive={false} onConfirm={() => { setDiscardOpen(false); closeEditor(); }} />
      <ConfirmDialog
        open={compactOpen}
        onOpenChange={setCompactOpen}
        title={t("contacts.compactTitle")}
        description={t("contacts.compactBody")}
        confirmLabel={t("contacts.compactConfirm")}
        destructive={false}
        onConfirm={() => {
          setCompactOpen(false);
          onCompact();
        }}
      />
    </div>
  );
}

export default function ConversationsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [dirtyRows, setDirtyRows] = useState<Record<string, boolean>>({});
  const [selected, setSelected] = useState<Record<string, number[]>>({});
  const blocker = useDirtyGuard(Object.values(dirtyRows).some(Boolean));
  const onDirtyChange = useCallback((key: string, dirty: boolean) => {
    setDirtyRows((current) => current[key] === dirty || (!current[key] && !dirty) ? current : { ...current, [key]: dirty });
  }, []);
  const query = useQuery({
    queryKey: ["conversations"],
    queryFn: conversationApi.list,
  });

  const mutation = useMutation({
    mutationFn: (input:
      | { kind: string; targetIDs: number[]; blocked: boolean }
      | { kind: string; targetID: number; extraPrompt: string }
    ) => "targetIDs" in input
      ? conversationApi.setBlocked(input.kind, input.targetIDs, input.blocked)
      : conversationApi.update(input.kind, input.targetID, { extra_prompt: input.extraPrompt }),
    onMutate: async (input) => {
      await queryClient.cancelQueries({ queryKey: ["conversations"] });
      const previous = queryClient.getQueryData<ConversationsData>([
        "conversations",
      ]);
      if (!previous) return { previous: undefined };
      const ids = new Set("targetIDs" in input ? input.targetIDs : [input.targetID]);
      const apply = (items: Conversation[]) =>
        items.map((item) =>
          item.kind === input.kind && ids.has(item.target_id)
            ? {
                ...item,
                blocked: "targetIDs" in input ? input.blocked : item.blocked,
                extra_prompt: "extraPrompt" in input ? input.extraPrompt : item.extra_prompt,
              }
            : item,
        );
      queryClient.setQueryData<ConversationsData>(["conversations"], {
        groups: apply(previous.groups),
        friends: apply(previous.friends),
      });
      return { previous };
    },
    onError: (error, _input, context) => {
      if (context?.previous) {
        queryClient.setQueryData(["conversations"], context.previous);
      }
      toastApiError(error);
    },
    onSuccess: (result, input) => {
      queryClient.setQueryData(["conversations"], result.data);
      if ("targetIDs" in input) {
        setSelected((current) => ({
          ...current,
          [input.kind]: (current[input.kind] ?? []).filter((id) => !input.targetIDs.includes(id)),
        }));
      }
      toastSuccess(
        result.warning
          ? t("errors.warning.runtime_reload_failed")
          : "targetIDs" in input
            ? t(input.blocked ? "contacts.blockedCount" : "contacts.restoredCount", { count: input.targetIDs.length })
            : t("contacts.promptSaved"),
      );
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["conversations"] });
    },
  });

  const compactMutation = useMutation({
    mutationFn: (input: { kind: string; targetID: number }) =>
      conversationApi.compact(input.kind, input.targetID),
    onSuccess: (result) => {
      toastSuccess(
        result.data.action === "compacted"
          ? t("contacts.compacted", { count: result.data.messages })
          : t("contacts.promptApplied"),
      );
      void queryClient.invalidateQueries({ queryKey: ["conversations"] });
    },
    onError: (error) => toastApiError(error),
  });

  if (query.isPending) return <PageSkeleton />;
  if (!query.data) {
    return (
      <EmptyState message={t("common.loadFailed")} />
    );
  }

  const renderList = (
    kind: string,
    title: string,
    items: Conversation[],
    empty: string,
  ) => {
    const chosen = items.filter((item) => selected[kind]?.includes(item.target_id));
    const allSelected = items.length > 0 && chosen.length === items.length;
    const pending = mutation.isPending && mutation.variables.kind === kind && "targetIDs" in mutation.variables;
    return (
      <Card>
        <div className="admin-card-pad-compact">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex items-center gap-3">
              <Checkbox
                checked={allSelected ? true : chosen.length > 0 ? "indeterminate" : false}
                disabled={mutation.isPending || items.length === 0}
                aria-label={t("contacts.selectAll", { kind: title })}
                onCheckedChange={(checked) => setSelected((current) => ({
                  ...current,
                  [kind]: checked === true ? items.map((item) => item.target_id) : [],
                }))}
              />
              <h2 className="admin-card-title-compact text-foreground">{title}</h2>
              {chosen.length > 0 ? <Badge variant="ghost">{t("contacts.selected", { count: chosen.length, total: items.length })}</Badge> : null}
            </div>
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                disabled={mutation.isPending || !chosen.some((item) => !item.blocked)}
                onClick={() => mutation.mutate({ kind, targetIDs: chosen.map((item) => item.target_id), blocked: true })}
              >
                {pending && "targetIDs" in mutation.variables && mutation.variables.blocked ? <Spinner /> : null}
                {t("contacts.batchBlock")}
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={mutation.isPending || !chosen.some((item) => item.blocked)}
                onClick={() => mutation.mutate({ kind, targetIDs: chosen.map((item) => item.target_id), blocked: false })}
              >
                {pending && "targetIDs" in mutation.variables && !mutation.variables.blocked ? <Spinner /> : null}
                {t("contacts.batchRestore")}
              </Button>
            </div>
          </div>
          <div className="mt-2 divide-y divide-border">
            {items.length === 0 ? (
              <p className="py-8 text-center text-sm text-muted-foreground">
                {empty}
              </p>
            ) : (
              items.map((conversation) => {
                const compacting =
                  compactMutation.isPending &&
                  compactMutation.variables?.kind === conversation.kind &&
                  compactMutation.variables?.targetID === conversation.target_id;
                return (
                  <ConversationRow
                    key={`${conversation.kind}-${conversation.target_id}`}
                    conversation={conversation}
                    busy={mutation.isPending || compacting}
                    compacting={compacting}
                    selected={selected[kind]?.includes(conversation.target_id) ?? false}
                    onSelect={(checked) => setSelected((current) => ({
                      ...current,
                      [kind]: checked
                        ? [...(current[kind] ?? []), conversation.target_id]
                        : (current[kind] ?? []).filter((id) => id !== conversation.target_id),
                    }))}
                    onDirtyChange={onDirtyChange}
                    onBlockedChange={(blocked) =>
                      mutation.mutate({
                        kind: conversation.kind,
                        targetIDs: [conversation.target_id],
                        blocked,
                      })
                    }
                    onCompact={() => compactMutation.mutate({
                      kind: conversation.kind,
                      targetID: conversation.target_id,
                    })}
                    onPromptSaved={async (prompt) => {
                      try {
                        await mutation.mutateAsync({
                          kind: conversation.kind,
                          targetID: conversation.target_id,
                          extraPrompt: prompt,
                        });
                        return true;
                      } catch {
                        return false;
                      }
                    }}
                  />
                );
              })
            )}
          </div>
        </div>
      </Card>
    );
  };

  return (
    <div className="space-y-6">
      {query.isRefetchError ? <p role="alert" className="text-sm text-destructive">{t("common.refreshFailed")}</p> : null}
      <DirtyGuardDialog blocker={blocker} />
      <PageHeader
        title={t("contacts.title")}
        subtitle={t("contacts.subtitle")}
      />
      {renderList("group", t("contacts.groups"), query.data.groups, t("contacts.emptyGroups"))}
      {renderList(
        "private",
        t("contacts.friends"),
        query.data.friends,
        t("contacts.emptyFriends"),
      )}
    </div>
  );
}
