import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { motion, useReducedMotion } from "motion/react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { identityApi, personaApi } from "@/api/client-endpoints";
import type { PersonaDetail, PersonaSessionMode, PersonaTexts } from "@/api/types";
import { ConfirmDialog } from "@/components/admin/confirm-dialog";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { toastApiError, toastSuccess, toastWarningCode } from "@/components/admin/toast";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Field, Input, NativeSelect, Textarea } from "@/components/ui/form-controls";
import { Spinner } from "@/components/ui/spinner";
import { DirtyGuardDialog, useDirtyGuard } from "@/hooks/use-dirty-guard";
import { useQueryParams } from "@/hooks/use-query-params";
import { cn } from "@/lib/cn";
import { formatDateTime } from "@/lib/format";
const personaTextLimit = 8000;
const personaDocs = [
  { key: "persona_card", label: "personaCard", limit: personaTextLimit },
  { key: "common_rules", label: "commonRules", limit: personaTextLimit },
  { key: "qq_prompt", label: "qqPrompt", limit: personaTextLimit },
  { key: "life_prompt", label: "lifePrompt", limit: personaTextLimit },
  { key: "worker_prompt", label: "workerPrompt", limit: personaTextLimit },
] as const;

const emptyTexts: PersonaTexts = {
  persona_card: "", common_rules: "", qq_prompt: "", life_prompt: "", worker_prompt: "",
};

function promptTexts(source: PersonaTexts): PersonaTexts {
  return {
    persona_card: source.persona_card, common_rules: source.common_rules,
    qq_prompt: source.qq_prompt, life_prompt: source.life_prompt, worker_prompt: source.worker_prompt,
  };
}

export default function PersonasPage() {
  const { t } = useTranslation();
  const reducedMotion = useReducedMotion();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { params } = useQueryParams();
  const selectedID = Number(params.get("id") || "0");
  const creating = params.get("new") === "1";
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [texts, setTexts] = useState<PersonaTexts>(emptyTexts);
  const [identityName, setIdentityName] = useState("");
  const [masterID, setMasterID] = useState(0);
  const [identityVersion, setIdentityVersion] = useState(0);
  const [identityBaselineName, setIdentityBaselineName] = useState("");
  const [identityBaselineMaster, setIdentityBaselineMaster] = useState(0);
  const [personaBaseline, setPersonaBaseline] = useState({ name: "", description: "", texts: emptyTexts });
  const [draftVersion, setDraftVersion] = useState(0);
  const [leaveTarget, setLeaveTarget] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [identityEditing, setIdentityEditing] = useState(false);
  const [activeDoc, setActiveDoc] = useState<(typeof personaDocs)[number]["key"]>("persona_card");
  const [previewMode, setPreviewMode] = useState<PersonaSessionMode>("group");
  const [previewing, setPreviewing] = useState(false);
  const [busyAction, setBusyAction] = useState("");
  const loadedKey = useRef("");

  const list = useQuery({
    queryKey: ["personas"],
    queryFn: personaApi.list,
  });
  const detail = useQuery({
    queryKey: ["persona", selectedID],
    queryFn: () => personaApi.get(selectedID),
    enabled: selectedID > 0 && !creating,
  });
  const template = useQuery({
    queryKey: ["persona-template"],
    queryFn: personaApi.template,
    enabled: creating,
  });
  const identity = useQuery({
    queryKey: ["identity"],
    queryFn: identityApi.get,
  });
  const friends = useQuery({
    queryKey: ["identity-friends"],
    queryFn: identityApi.friends,
  });

  useEffect(() => {
    if (creating) return;
    if (list.data && selectedID === 0) {
      const target = list.data.active_id || list.data.items[0]?.id;
      if (target) {
        navigate(`/admin/personas?id=${target}`, { replace: true });
      }
    }
  }, [creating, list.data, selectedID, navigate]);

  useEffect(() => {
    if (!identity.data || identityEditing) return;
    setIdentityName(identity.data.name);
    setMasterID(identity.data.master_user_id);
    setIdentityVersion(identity.data.version);
    setIdentityBaselineName(identity.data.name);
    setIdentityBaselineMaster(identity.data.master_user_id);
  }, [identity.data, identityEditing]);

  useLayoutEffect(() => {
    const source = creating ? template.data : detail.data;
    const key = creating ? "new" : `id:${selectedID}`;
    if (!source || loadedKey.current === key) return;
    loadedKey.current = key;
    const nextTexts = promptTexts(source);
    const nextName = creating ? "" : detail.data!.name;
    const nextDescription = creating ? "" : detail.data!.description;
    setName(nextName);
    setDescription(nextDescription);
    setTexts(nextTexts);
    setPersonaBaseline({ name: nextName, description: nextDescription, texts: nextTexts });
    setDraftVersion(creating ? 0 : detail.data!.version);
  }, [creating, selectedID, detail.data, template.data]);

  const personaDirty = name !== personaBaseline.name || description !== personaBaseline.description ||
    personaDocs.some(({ key }) => texts[key] !== personaBaseline.texts[key]);
  const promptsValid = personaDocs.every(({ key, limit }) => texts[key].trim() && Array.from(texts[key]).length <= limit);
  const preview = useQuery({
    queryKey: ["persona-preview", previewMode, texts, identity.data?.version],
    queryFn: async () => (await personaApi.preview({ ...texts, mode: previewMode })).data,
    enabled: previewing && promptsValid,
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
  });
  const identityDirty =
    identityEditing &&
    (identityName !== identityBaselineName || masterID !== identityBaselineMaster);
  const dirty = personaDirty || identityDirty;
  const versionConflict = Boolean(
    !creating && selectedID > 0 && detail.data && draftVersion > 0 && detail.data.version !== draftVersion,
  );
  const identityConflict = Boolean(
    identityEditing && identity.data && identityVersion > 0 && identity.data.version !== identityVersion,
  );
  const guard = useDirtyGuard(dirty && !leaveTarget);

  useEffect(() => {
    if (!leaveTarget) return;
    navigate(leaveTarget, { replace: true });
    setLeaveTarget(null);
  }, [leaveTarget, navigate]);

  const reloadLatest = async () => {
    const result = await detail.refetch();
    if (result.isError || !result.data) {
      toastApiError(result.error);
      return;
    }
    const fresh = result.data;
    const nextTexts = promptTexts(fresh);
    setName(fresh.name);
    setDescription(fresh.description);
    setTexts(nextTexts);
    setPersonaBaseline({ name: fresh.name, description: fresh.description, texts: nextTexts });
    setDraftVersion(fresh.version);
  };

  const reloadIdentity = async () => {
    const result = await identity.refetch();
    if (!result.data) return;
    setIdentityName(result.data.name);
    setMasterID(result.data.master_user_id);
    setIdentityVersion(result.data.version);
    setIdentityBaselineName(result.data.name);
    setIdentityBaselineMaster(result.data.master_user_id);
  };

  const save = useMutation({
    mutationFn: () => {
      const payload = {
        name: name.trim(),
        description: description.trim(),
        ...texts,
      };
      if (creating) return personaApi.create(payload);
      return personaApi.update(selectedID, {
        ...payload,
        version: draftVersion,
      });
    },
    onSuccess: async (result) => {
      if (result.warning) toastWarningCode(result.warning.code);
      else
        toastSuccess(
          creating ? t("personas.editor.created") : t("personas.editor.saved"),
        );
      await queryClient.invalidateQueries({ queryKey: ["personas"] });
      setName(name.trim());
      setDescription(description.trim());
      setPersonaBaseline({ name: name.trim(), description: description.trim(), texts });
      setDraftVersion(result.data.version);
      if (creating) {
        setLeaveTarget(`/admin/personas?id=${result.data.id}`);
      } else {
        await queryClient.invalidateQueries({
          queryKey: ["persona", selectedID],
        });
      }
    },
    onError: async (error) => {
      toastApiError(error);
      if (error instanceof ApiError && error.code === "version_conflict") {
        await queryClient.invalidateQueries({
          queryKey: ["persona", selectedID],
        });
      }
    },
  });

  const runAction = async (action: "activate" | "clone" | "delete") => {
    setBusyAction(action);
    try {
      if (action === "activate") {
        const result = await personaApi.activate(selectedID);
        if (result.warning) toastWarningCode(result.warning.code);
        else toastSuccess(t("personas.editor.activated"));
      } else if (action === "clone") {
        const result = await personaApi.clone(selectedID);
        if (result.warning) toastWarningCode(result.warning.code);
        else toastSuccess(t("personas.editor.cloned"));
        setLeaveTarget(`/admin/personas?id=${result.data.id}`);
      } else {
        await personaApi.remove(selectedID);
        toastSuccess(t("personas.editor.deleted"));
        setLeaveTarget("/admin/personas");
      }
      await queryClient.invalidateQueries({ queryKey: ["personas"] });
      await queryClient.invalidateQueries({ queryKey: ["persona", selectedID] });
    } catch (error) {
      toastApiError(error);
    } finally {
      setBusyAction("");
      setDeleteOpen(false);
    }
  };

  const saveIdentity = useMutation({
    mutationFn: () =>
      identityApi.update({
        name: identityName.trim(),
        master_user_id: masterID,
        version: identityVersion,
      }),
    onSuccess: async (result) => {
      if (result.warning) toastWarningCode(result.warning.code);
      else toastSuccess(t("personas.identity.saved"));
      await queryClient.invalidateQueries({ queryKey: ["identity"] });
      await queryClient.invalidateQueries({ queryKey: ["identity-friends"] });
      setIdentityVersion(result.data.version);
      setIdentityBaselineName(identityName.trim());
      setIdentityBaselineMaster(masterID);
      setIdentityEditing(false);
    },
    onError: async (error) => {
      toastApiError(error);
      if (error instanceof ApiError && error.code === "version_conflict") {
        await identity.refetch();
      }
    },
  });

  const selected = detail.data as PersonaDetail | undefined;
  const isBuiltin = selected?.builtin ?? false;
  const isActive = selected?.active ?? false;
  const activeDocument = personaDocs.find(({ key }) => key === activeDoc)!;
  const activeChars = Array.from(texts[activeDoc]).length;
  const busy = save.isPending || busyAction !== "";
  const friendItems = friends.data?.items ?? [];
  const currentMaster = identity.data?.master_user_id ?? 0;
  const currentMasterInFriends = friendItems.some(
    (friend) => friend.user_id === currentMaster,
  );

  if (list.isPending) return <PageSkeleton />;
  if (!list.data) {
    return (
      <div className="flex min-h-[50vh] flex-col items-center justify-center gap-3">
        <p className="text-sm text-muted-foreground">
          {t("common.loadFailed")}
        </p>
        <Button variant="outline" onClick={() => void list.refetch()}>
          {t("common.retry")}
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {list.isRefetchError ? <p role="alert" className="text-sm text-destructive">{t("common.refreshFailed")}</p> : null}
      <PageHeader
        title={t("personas.title")}
        subtitle={t("personas.subtitle")}
        actions={
          <Button
            size="sm"
            onClick={() => navigate("/admin/personas?new=1")}
          >
            {t("personas.newPreset")}
          </Button>
        }
      />

      <Card>
        <div className="admin-card-stack admin-card-pad">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h2 className="admin-card-title text-foreground">
                {t("personas.identity.title")}
              </h2>
              <p className="mt-1 admin-card-desc">
                {t("personas.identity.description")}
              </p>
            </div>
            {identity.data?.master_user_id &&
            !identity.data.master_available ? (
              <Badge variant="warning">
                {t("personas.identity.masterUnavailable")}
              </Badge>
            ) : null}
          </div>

          {identityEditing ? (
            <div className="admin-form-grid">
              <Field
                label={t("personas.identity.name")}
                hint={t("personas.identity.nameHint")}
              >
                <Input
                  value={identityName}
                  maxLength={32}
                  onChange={(event) => setIdentityName(event.target.value)}
                />
              </Field>
              <Field
                label={t("personas.identity.master")}
                hint={t("personas.identity.masterHint")}
              >
                <NativeSelect
                  value={String(masterID)}
                  onChange={(event) =>
                    setMasterID(Number(event.target.value))
                  }
                >
                  <option value="0">
                    {t("personas.identity.masterNone")}
                  </option>
                  {friendItems.map((friend) => (
                    <option
                      key={friend.user_id}
                      value={friend.user_id}
                      disabled={!friend.active || friend.blocked}
                    >
                      {t("personas.identity.friendOption", {
                        name: friend.name || `QQ ${friend.user_id}`,
                        qq: friend.user_id,
                      })}
                      {!friend.active
                        ? t("personas.identity.inactiveSuffix")
                        : ""}
                      {friend.blocked
                        ? t("personas.identity.blockedSuffix")
                        : ""}
                    </option>
                  ))}
                  {!currentMasterInFriends && currentMaster > 0 ? (
                    <option value={currentMaster}>
                      {t("personas.identity.masterMissingOption", {
                        name:
                          identity.data?.master_name || `QQ ${currentMaster}`,
                        qq: currentMaster,
                      })}
                    </option>
                  ) : null}
                </NativeSelect>
              </Field>
            </div>
          ) : (
            <div className="flex flex-wrap items-center gap-x-6 gap-y-2 rounded-xl bg-muted/40 px-4 py-3">
              <div>
                <p className="admin-label-text text-muted-foreground/60">
                  {t("personas.identity.name")}
                </p>
                <p className="mt-1 text-sm font-medium text-foreground">
                  {identity.data?.name || identityName || "-"}
                </p>
              </div>
              <div>
                <p className="admin-label-text text-muted-foreground/60">
                  {t("personas.identity.master")}
                </p>
                <p className="mt-1 text-sm font-medium text-foreground">
                  {currentMaster > 0
                    ? `${identity.data?.master_name || t("personas.identity.masterMissing")} (${currentMaster})`
                    : t("personas.identity.masterNone")}
                </p>
              </div>
              <Button
                variant="outline"
                size="sm"
                className="ml-auto"
                onClick={() => {
                  setIdentityVersion(identity.data?.version ?? identityVersion);
                  setIdentityEditing(true);
                }}
              >
                {t("personas.identity.edit")}
              </Button>
            </div>
          )}

          {identityConflict ? (
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning">
              <div>
                <p className="font-medium">{t("common.versionChanged")}</p>
                <p className="mt-0.5">{t("common.versionChangedHint")}</p>
              </div>
              <Button type="button" variant="outline" size="xs" onClick={() => void reloadIdentity()}>
                {t("common.reloadLatest")}
              </Button>
            </div>
          ) : null}

          {identityEditing ? <p className="admin-caption-text text-muted-foreground/75">
            {t("personas.identity.variables")}
          </p> : null}
          {identity.isError || friends.isError ? (
            <p className="text-xs text-destructive">
              {t("personas.identity.loadFailed")}
            </p>
          ) : null}
          {identityEditing ? (
            <div className="flex flex-wrap items-center gap-3">
              <Button
                onClick={() => saveIdentity.mutate()}
                disabled={
                  !identityName.trim() ||
                  identityConflict ||
                  saveIdentity.isPending ||
                  identity.isPending
                }
              >
                {saveIdentity.isPending ? <Spinner /> : null}
                {t("personas.identity.save")}
              </Button>
              <Button
                variant="ghost"
                onClick={() => {
                  setIdentityEditing(false);
                  setIdentityName(identityBaselineName);
                  setMasterID(identityBaselineMaster);
                }}
                disabled={saveIdentity.isPending}
              >
                {t("common.cancel")}
              </Button>
              <span className="text-xs text-muted-foreground/60">
                {t("personas.identity.effectiveHint")}
              </span>
            </div>
          ) : null}
        </div>
      </Card>

      <div className="grid grid-cols-[clamp(240px,20vw,300px)_minmax(0,1fr)] items-start gap-4">
        <Card>
          <div className="admin-card-pad-compact space-y-3">
            <h2 className="admin-card-title-compact text-foreground">
              {t("personas.list")}
            </h2>
            {(list.data?.items.length ?? 0) === 0 ? (
              <p className="text-sm text-muted-foreground">
                {t("personas.empty")}
              </p>
            ) : (
              <div className="space-y-2">
                {list.data?.items.map((preset) => (
                  <button
                    key={preset.id}
                    type="button"
                    onClick={() =>
                      navigate(`/admin/personas?id=${preset.id}`)
                    }
                    className={cn(
                      "block w-full rounded-xl border p-3 text-left transition-colors",
                      preset.id === selectedID && !creating
                        ? "border-primary/40 bg-primary/5"
                        : "border-border bg-card hover:border-primary/30",
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-sm font-medium text-foreground">
                        {preset.name}
                      </span>
                      <span className="flex shrink-0 items-center gap-1">
                        {preset.active ? (
                          <Badge variant="success">{t("personas.inUse")}</Badge>
                        ) : null}
                        {preset.builtin ? (
                          <Badge variant="ghost">
                            {t("personas.builtin")}
                          </Badge>
                        ) : null}
                      </span>
                    </div>
                    <p className="mt-0.5 text-xs text-muted-foreground/60">
                      {t("personas.updatedAt", {
                        time: formatDateTime(preset.updated_at),
                      })}
                    </p>
                  </button>
                ))}
              </div>
            )}
          </div>
        </Card>

        <Card>
          <div className="admin-card-stack admin-card-pad">
            {(detail.isPending && !creating) ||
            (creating && template.isPending) ? (
              <PageSkeleton />
            ) : (
              <>
                <div className="flex items-center justify-between gap-3">
                  <h2 className="admin-card-title text-foreground">
                    {creating
                      ? t("personas.editor.newTitle")
                      : selected?.name || ""}
                  </h2>
                  {isBuiltin ? (
                    <p className="text-xs text-muted-foreground">
                      {t("personas.editor.builtinReadonly")}
                    </p>
                  ) : null}
                </div>

                {versionConflict ? (
                  <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning">
                    <div>
                      <p className="font-medium">{t("common.versionChanged")}</p>
                      <p className="mt-0.5">{t("common.versionChangedHint")}</p>
                    </div>
                    <Button type="button" variant="outline" size="xs" onClick={() => void reloadLatest()}>
                      {t("common.reloadLatest")}
                    </Button>
                  </div>
                ) : null}

                <div className="admin-form-grid">
                  <Field label={t("personas.editor.name")}>
                    <Input
                      value={name}
                      onChange={(event) => setName(event.target.value)}
                      disabled={isBuiltin || save.isPending}
                    />
                  </Field>
                  <Field label={t("personas.editor.description")}>
                    <Input
                      value={description}
                      onChange={(event) => setDescription(event.target.value)}
                      disabled={isBuiltin || save.isPending}
                    />
                  </Field>
                </div>

                <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3">
                  <div className="flex flex-wrap gap-1">
                    {!previewing ? personaDocs.map(({ key, label }) => (
                      <button
                        key={key}
                        type="button"
                        aria-pressed={activeDoc === key}
                        onClick={() => setActiveDoc(key)}
                        className={cn(
                          "relative rounded-lg px-3 py-2 text-sm font-medium transition-colors",
                          activeDoc === key
                            ? "text-primary"
                            : "text-muted-foreground hover:bg-muted hover:text-foreground",
                        )}
                      >
                        {activeDoc === key ? (
                          <motion.span
                            layoutId="persona-doc-indicator"
                            className="absolute inset-x-3 bottom-1 h-0.5 rounded-full bg-primary"
                            transition={{
                              duration: reducedMotion ? 0 : 0.18,
                              ease: [0.22, 1, 0.36, 1],
                            }}
                          />
                        ) : null}
                        <span className="relative">{t(`personas.editor.${label}`)}</span>
                      </button>
                    )) : <span className="text-sm font-medium">{t("personas.editor.preview")}</span>}
                  </div>
                  <div className="flex gap-1">
                    <Button
                      variant={previewing ? "ghost" : "outline"}
                      size="sm"
                      onClick={() => setPreviewing(false)}
                    >
                      {t("personas.editor.edit")}
                    </Button>
                    <Button
                      variant={previewing ? "outline" : "ghost"}
                      size="sm"
                      onClick={() => setPreviewing(true)}
                      disabled={!promptsValid}
                    >
                      {t("personas.editor.preview")}
                    </Button>
                  </div>
                </div>

                {previewing ? (
                  <div className="space-y-4">
                    <NativeSelect aria-label={t("personas.preview.mode")} value={previewMode} onChange={(event) => setPreviewMode(event.target.value as PersonaSessionMode)}>
                      {(["group", "private", "life", "worker"] as const).map((mode) => <option key={mode} value={mode}>{t(`personas.preview.${mode}`)}</option>)}
                    </NativeSelect>
                    {preview.isFetching ? <PageSkeleton /> : preview.isError ? (
                      <div role="alert" className="space-y-3 text-sm text-destructive">
                        <p>{t("personas.preview.failed")}</p>
                        <Button variant="outline" onClick={() => void preview.refetch()}>{t("common.retry")}</Button>
                      </div>
                    ) : (
                      <pre tabIndex={0} aria-label={t("personas.editor.preview")} className="min-h-96 max-h-[65vh] overflow-y-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring whitespace-pre-wrap break-words rounded-lg border border-border bg-muted/30 p-4 font-sans text-sm leading-relaxed text-foreground/75">{preview.data?.text || t("personas.preview.empty")}</pre>
                    )}
                  </div>
                ) : (
                  <div className="space-y-2">
                    <Textarea
                      rows={20}
                      aria-label={t(`personas.editor.${activeDocument.label}`)}
                      value={texts[activeDoc]}
                      onChange={(event) => setTexts((before) => ({ ...before, [activeDoc]: event.target.value }))}
                      readOnly={isBuiltin}
                      disabled={save.isPending}
                      className="min-h-96 font-sans text-sm leading-relaxed"
                    />
                    <p className={cn("text-right text-xs", activeChars > activeDocument.limit ? "text-destructive" : "text-muted-foreground")}>
                      {t("personas.editor.charCount", { current: activeChars, limit: activeDocument.limit })}
                    </p>
                  </div>
                )}

                <div className="flex flex-wrap items-center gap-2 border-t border-border pt-4">
                  <Button
                    onClick={() => save.mutate()}
                    disabled={
                      busy ||
                      versionConflict ||
                      isBuiltin ||
                      !name.trim() ||
                      !promptsValid
                    }
                  >
                    {save.isPending ? <Spinner /> : null}
                    {creating
                      ? t("personas.editor.creating")
                      : t("personas.editor.save")}
                  </Button>
                  {!creating && selected ? (
                    <>
                      <Button
                        variant="outline"
                        onClick={() => runAction("activate")}
                        disabled={busy || personaDirty || isActive}
                      >
                        {t("personas.editor.activate")}
                      </Button>
                      <Button
                        variant="outline"
                        onClick={() => runAction("clone")}
                        disabled={busy || personaDirty}
                      >
                        {t("personas.editor.clone")}
                      </Button>
                      <Button
                        variant="ghost"
                        className="ml-auto text-destructive hover:bg-destructive/10"
                        onClick={() => setDeleteOpen(true)}
                        disabled={busy || personaDirty || isBuiltin || isActive}
                      >
                        {t("personas.editor.delete")}
                      </Button>
                    </>
                  ) : null}
                </div>
              </>
            )}
          </div>
        </Card>
      </div>

      <DirtyGuardDialog blocker={guard} />

      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title={t("personas.editor.deleteTitle")}
        description={t("personas.editor.deleteBody", {
          name: selected?.name || "",
        })}
        confirmLabel={t("common.delete")}
        loading={busyAction === "delete"}
        onConfirm={() => runAction("delete")}
      />
    </div>
  );
}
