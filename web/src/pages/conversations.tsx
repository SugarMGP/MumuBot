import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
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
import { Textarea } from "@/components/ui/form-controls";
import { formatDateTime } from "@/lib/format";

function ConversationRow({
  conversation,
  busy,
  onBlockedChange,
  onPromptSaved,
}: {
  conversation: Conversation;
  busy: boolean;
  onBlockedChange: (blocked: boolean) => void;
  onPromptSaved: (prompt: string) => Promise<boolean>;
}) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [prompt, setPrompt] = useState(conversation.extra_prompt);

  const label = conversation.name || conversation.remote_remark || "";

  return (
    <div className="-mx-4 flex flex-col gap-3 px-4 py-3 transition-colors hover:bg-muted/40">
      <div className="flex items-center justify-between gap-3">
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
              onClick={() => setEditing((value) => !value)}
            >
              {t("contacts.editPrompt")}
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
            <div className="flex justify-end gap-2">
              <Button
                variant="ghost"
                size="xs"
                onClick={() => {
                  setPrompt(conversation.extra_prompt);
                  setEditing(false);
                }}
              >
                {t("common.cancel")}
              </Button>
              <Button
                size="xs"
                disabled={saving}
                onClick={async () => {
                  setSaving(true);
                  const saved = await onPromptSaved(prompt);
                  setSaving(false);
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
    </div>
  );
}

export default function ConversationsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: ["conversations"],
    queryFn: conversationApi.list,
  });

  const mutation = useMutation({
    mutationFn: (input: {
      kind: string;
      targetID: number;
      blocked?: boolean;
      extraPrompt?: string;
    }) =>
      conversationApi.update(input.kind, input.targetID, {
        blocked: input.blocked,
        extra_prompt: input.extraPrompt,
      }),
    onMutate: async (input) => {
      await queryClient.cancelQueries({ queryKey: ["conversations"] });
      const previous = queryClient.getQueryData<ConversationsData>([
        "conversations",
      ]);
      if (!previous) return { previous: undefined };
      const apply = (items: Conversation[]) =>
        items.map((item) =>
          item.kind === input.kind && item.target_id === input.targetID
            ? {
                ...item,
                blocked: input.blocked ?? item.blocked,
                extra_prompt: input.extraPrompt ?? item.extra_prompt,
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
    onSuccess: (result) => {
      queryClient.setQueryData(["conversations"], result.data);
      toastSuccess(
        result.warning
          ? t("errors.warning.runtime_reload_failed")
          : t("common.saved"),
      );
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["conversations"] });
    },
  });

  if (query.isPending) return <PageSkeleton />;
  if (query.isError || !query.data) {
    return (
      <EmptyState message={t("common.loadFailed")} />
    );
  }

  const renderList = (
    title: string,
    items: Conversation[],
    empty: string,
  ) => (
    <Card>
      <div className="admin-card-pad-compact">
        <h2 className="admin-card-title-compact text-foreground">{title}</h2>
        <div className="mt-2 divide-y divide-border">
          {items.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">
              {empty}
            </p>
          ) : (
            items.map((conversation) => (
              <ConversationRow
                key={`${conversation.kind}-${conversation.target_id}`}
                conversation={conversation}
                busy={mutation.isPending}
                onBlockedChange={(blocked) =>
                  mutation.mutate({
                    kind: conversation.kind,
                    targetID: conversation.target_id,
                    blocked,
                  })
                }
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
            ))
          )}
        </div>
      </div>
    </Card>
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title={t("contacts.title")}
        subtitle={t("contacts.subtitle")}
      />
      {renderList(t("contacts.groups"), query.data.groups, t("contacts.emptyGroups"))}
      {renderList(
        t("contacts.friends"),
        query.data.friends,
        t("contacts.emptyFriends"),
      )}
    </div>
  );
}
