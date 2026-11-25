import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { modelApi } from "@/api/client-endpoints";
import type { ModelProfile, ModelSelection, ModelProvider, ThinkingLevel, ThinkingMode } from "@/api/types";
import { ConfirmDialog } from "@/components/admin/confirm-dialog";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { EditPanel } from "@/components/admin/edit-panel";
import { SectionCard } from "@/components/admin/section-card";
import { SettingsTabs } from "@/components/admin/tabs";
import {
  toastApiError,
  toastSuccess,
  toastWarningCode,
} from "@/components/admin/toast";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input, NativeSelect, Textarea } from "@/components/ui/form-controls";
import { Spinner } from "@/components/ui/spinner";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { DirtyGuardDialog, useDirtyGuard } from "@/hooks/use-dirty-guard";
import { useQueryParams } from "@/hooks/use-query-params";
import { formatDateTime } from "@/lib/format";
import { enumLabel } from "@/lib/labels";

const emptyForm = {
  provider: "openai_responses" as ModelProvider,
  thinking_mode: "" as ThinkingMode,
  thinking_budget: null as number | null,
  thinking_level: "" as ThinkingLevel,
  body_override: "{}",
  name: "",
  base_url: "",
  model_name: "",
  context_window: 200000,
  reasoning_effort: "",
  api_key: "",
};

function profileForm(profile: ModelProfile) {
  return {
    provider: profile.provider,
    thinking_mode: profile.thinking_mode,
    thinking_budget: profile.thinking_budget,
    thinking_level: profile.thinking_level,
    body_override: profile.body_override,
    name: profile.name,
    base_url: profile.base_url,
    model_name: profile.model_name,
    context_window: profile.context_window,
    reasoning_effort: profile.reasoning_effort,
    api_key: "",
  };
}

export default function ModelsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { params, update } = useQueryParams();
  const editID = Number(params.get("edit") || "0");
  const panelOpen = params.has("edit");
  const editMode = editID > 0;
  const query = useQuery({ queryKey: ["models"], queryFn: modelApi.list });
  const [form, setForm] = useState({ ...emptyForm });
  const [baseline, setBaseline] = useState({ ...emptyForm });
  const [draftVersion, setDraftVersion] = useState(0);
  const [leaveAfter, setLeaveAfter] = useState(false);
  const loadedKey = useRef("");
  const editingRef = useRef<ModelProfile | undefined>(undefined);
  const [deleting, setDeleting] = useState<ModelProfile | null>(null);
  const [discardOpen, setDiscardOpen] = useState(false);
  const [testing, setTesting] = useState(0);
  const [saving, setSaving] = useState(false);
  const editing = query.data?.items.find((item) => item.id === editID);
  editingRef.current = editing;
  const editingID = editing?.id ?? 0;
  const dirty = useMemo(
    () => JSON.stringify(form) !== JSON.stringify(baseline),
    [form, baseline],
  );
  const versionConflict = Boolean(
    editMode && editing && draftVersion > 0 && editing.version !== draftVersion,
  );
  const guard = useDirtyGuard(dirty && !leaveAfter);
  const bodyError = useMemo(() => {
    try {
      const body: unknown = JSON.parse(form.body_override.trim() || "{}");
      if (body === null || typeof body !== "object" || Array.isArray(body)) return "models.fields.bodyObjectError";
      if (Object.hasOwn(body, "stream")) return "models.fields.bodyStreamError";
      return null;
    } catch {
      return "models.fields.bodyJSONError";
    }
  }, [form.body_override]);

  useEffect(() => {
    if (!leaveAfter) return;
    update({ edit: undefined });
    setLeaveAfter(false);
  }, [leaveAfter, update]);

  useLayoutEffect(() => {
    if (!panelOpen) {
      loadedKey.current = "";
      setForm({ ...emptyForm });
      setBaseline({ ...emptyForm });
      setDraftVersion(0);
      setLeaveAfter(false);
      return;
    }
    const key = editMode ? `id:${editID}` : "new";
    if (loadedKey.current === key) return;
    const current = editingRef.current;
    if (editMode && (!current || current.id !== editID)) return;
    loadedKey.current = key;
    const next = current ? profileForm(current) : { ...emptyForm };
    setForm(next);
    setBaseline(next);
    setDraftVersion(current?.version ?? 0);
  }, [editID, editMode, editingID, editing?.version, panelOpen]);

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["models"] });

  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    if (saving || versionConflict || bodyError || !panelOpen || (editMode && !editing)) return;
    setSaving(true);
    try {
      if (editMode && editing) {
        const result = await modelApi.update(editing.id, {
          ...form,
          version: draftVersion,
        });
        await invalidate();
        toastSuccess(t("models.saved"));
        if (result.warning) toastWarningCode(result.warning.code);
        setDraftVersion(result.data.version);
      } else {
        await modelApi.create(form);
        await invalidate();
        toastSuccess(t("models.saved"));
      }
      setBaseline({ ...form });
      setLeaveAfter(true);
    } catch (error) {
      toastApiError(error);
      await invalidate();
    } finally {
      setSaving(false);
    }
  };

  const requestClose = (next: boolean) => {
    if (!next) {
      if (dirty) {
        setDiscardOpen(true);
        return;
      }
      update({ edit: undefined });
    }
  };

  const reloadLatest = async () => {
    const result = await query.refetch();
    const current = result.data?.items.find((item) => item.id === editID);
    if (result.isError || !current) {
      toastApiError(result.error);
      return;
    }
    const next = profileForm(current);
    setForm(next);
    setBaseline(next);
    setDraftVersion(current.version);
  };

  const selectModel = useMutation({
    mutationFn: (selection: ModelSelection) => modelApi.select(selection),
    onSuccess: (result) => {
      queryClient.setQueryData(["models"], result.data);
      if (result.warning) toastWarningCode(result.warning.code);
      else toastSuccess(t("models.selectionSaved"));
    },
    onError: (error) => { toastApiError(error); void invalidate(); },
  });

  const test = async (id: number) => {
    setTesting(id);
    try {
      await modelApi.test(id);
      toastSuccess(t("models.testSuccess"));
    } catch (error) {
      toastApiError(error);
    } finally {
      setTesting(0);
    }
  };

  const remove = useMutation({
    mutationFn: (profile: ModelProfile) =>
      modelApi.remove(profile.id, profile.version),
    onSuccess: async () => {
      setDeleting(null);
      await invalidate();
      toastSuccess(t("models.deleted"));
    },
    onError: (error) => {
      setDeleting(null);
      toastApiError(error);
    },
  });

  if (query.isPending) return <PageSkeleton />;
  if (!query.data) {
    return (
      <p className="text-sm text-muted-foreground">{t("common.loadFailed")}</p>
    );
  }

  return (
    <div className="space-y-6">
      {query.isRefetchError ? <p role="alert" className="text-sm text-destructive">{t("common.refreshFailed")}</p> : null}
      <div className="flex flex-wrap items-end justify-between gap-4">
        <PageHeader
          title={t("models.title")}
          actions={
            <Button onClick={() => update({ edit: "new" })}>
              {t("models.add")}
            </Button>
          }
        />
        <SettingsTabs />
      </div>

      {!query.data.secret_configured ? (
        <div className="rounded-xl border border-warning/40 bg-warning/10 px-4 py-3 text-sm text-warning">
          {t("models.secretUnavailable")}
        </div>
      ) : null}

      <SectionCard title={t("models.selectionTitle")}>
        <div className="grid grid-cols-3 gap-4">
          {(["qq", "life", "worker"] as const).map((role) => (
            <Field key={role} label={t(`models.roles.${role}`)}>
              <div className="flex items-center gap-2">
                <NativeSelect
                  aria-label={t(`models.roles.${role}`)}
                  disabled={selectModel.isPending || query.data.items.length === 0}
                  value={query.data.selection[role]}
                  onChange={(event) => {
                    const id = Number(event.target.value);
                    const current = query.data.selection;
                    const next = current.qq === 0
                      ? { qq: id, life: id, worker: id, version: current.version }
                      : { ...current, [role]: id };
                    selectModel.mutate(next);
                  }}
                >
                  <option value={0} disabled>{t("models.choose")}</option>
                  {query.data.items.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}
                </NativeSelect>
                {selectModel.isPending ? <Spinner /> : null}
              </div>
            </Field>
          ))}
        </div>
      </SectionCard>

      <SectionCard title={t("models.list")}>
        {query.data.items.length === 0 ? (
          <p className="py-8 text-center text-sm text-muted-foreground">
            {t("models.empty")}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("models.fields.name")}</TableHead>
                <TableHead>{t("models.fields.modelName")}</TableHead>
                <TableHead>{t("models.fields.contextWindow")}</TableHead>
                <TableHead>{t("models.fields.reasoningEffort")}</TableHead>
                <TableHead>{t("models.usage")}</TableHead>
                <TableHead className="text-right">
                  {t("common.actions")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {query.data.items.map((profile) => (
                <TableRow key={profile.id}>
                  <TableCell>
                    <p className="font-medium">{profile.name}</p>
                    <p className="mt-0.5 text-xs text-muted-foreground">
                      {formatDateTime(profile.updated_at)}
                    </p>
                  </TableCell>
                  <TableCell className="text-xs">
                    <p>{profile.model_name}</p>
                    <p className="mt-0.5 text-muted-foreground">{enumLabel(t, "modelProvider", profile.provider)}</p>
                    <p className="mt-0.5 break-all text-muted-foreground">
                      {profile.base_url}
                    </p>
                  </TableCell>
                  <TableCell className="admin-number text-xs">
                    {profile.context_window}
                  </TableCell>
                  <TableCell className="text-xs">
                    {profile.provider === "gemini" ? (
                      <>
                        <p>{profile.thinking_budget === null ? t("models.fields.effortNone") : t("models.fields.thinkingBudgetValue", { value: profile.thinking_budget })}</p>
                        <p>{profile.thinking_level ? enumLabel(t, "thinkingLevel", profile.thinking_level) : t("models.fields.effortNone")}</p>
                      </>
                    ) : (
                      <>
                        {profile.provider === "openai_chat_completions" ? <p>{profile.thinking_mode ? enumLabel(t, "thinkingMode", profile.thinking_mode) : t("models.fields.effortNone")}</p> : null}
                        <p>{profile.reasoning_effort ? enumLabel(t, "reasoningEffort", profile.reasoning_effort) : t("models.fields.effortNone")}</p>
                      </>
                    )}
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap items-center gap-1">
                      {(["qq", "life", "worker"] as const).filter((role) => query.data.selection[role] === profile.id).map((role) => <Badge key={role}>{t(`models.roles.${role}`)}</Badge>)}
                    </div>
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="inline-flex flex-wrap justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="xs"
                        onClick={() => update({ edit: profile.id })}
                      >
                        {t("models.edit")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="xs"
                        disabled={testing === profile.id}
                        onClick={() => test(profile.id)}
                      >
                        {testing === profile.id ? <Spinner /> : null}
                        {testing === profile.id
                          ? t("models.testing")
                          : t("models.test")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="xs"
                        className="text-destructive hover:bg-destructive/10"
                        disabled={Object.values({ qq: query.data.selection.qq, life: query.data.selection.life, worker: query.data.selection.worker }).includes(profile.id) || selectModel.isPending}
                        onClick={() => setDeleting(profile)}
                      >
                        {t("models.delete")}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </SectionCard>

      <EditPanel
        open={panelOpen}
        onOpenChange={requestClose}
        title={editMode ? t("models.edit") : t("models.create")}
        footer={
          <>
            <Button
              type="button"
              variant="ghost"
              disabled={saving}
              onClick={() => requestClose(false)}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              form="model-form"
              disabled={saving || versionConflict || Boolean(bodyError) || (editMode && !editing)}
            >
              {saving ? <Spinner /> : null}
              {t("models.save")}
            </Button>
          </>
        }
      >
        {editMode && !editing ? (
          <p className="text-sm text-muted-foreground">{t("common.loadFailed")}</p>
        ) : (
          <form id="model-form" className="space-y-4" onSubmit={save}>
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
            <Field label={t("models.fields.name")}>
              <Input
                required
                value={form.name}
                onChange={(event) =>
                  setForm((value) => ({ ...value, name: event.target.value }))
                }
              />
            </Field>
            <Field label={t("models.fields.modelName")}>
              <Input
                required
                value={form.model_name}
                onChange={(event) =>
                  setForm((value) => ({
                    ...value,
                    model_name: event.target.value,
                  }))
                }
              />
            </Field>
          </div>

          <Field label={t("models.fields.provider")}>
            <NativeSelect value={form.provider} onChange={(event) => setForm((value) => ({ ...value, provider: event.target.value as ModelProvider }))}>
              {(["openai_responses", "openai_chat_completions", "gemini"] as const).map((provider) => <option key={provider} value={provider}>{enumLabel(t, "modelProvider", provider)}</option>)}
            </NativeSelect>
          </Field>

          <Field label={t("models.fields.baseUrl")}>
            <Input
              required
              placeholder={t("models.fields.baseUrlPlaceholder")}
              value={form.base_url}
              onChange={(event) =>
                setForm((value) => ({
                  ...value,
                  base_url: event.target.value,
                }))
              }
            />
          </Field>

          <Field
            label={t("models.fields.apiKey")}
            hint={
              editing?.api_key_hint
                ? t("models.fields.apiKeyKeep", {
                    hint: editing.api_key_hint,
                  })
                : editing
                  ? t("models.fields.apiKeyHint")
                  : undefined
            }
          >
            <Input
              type="password"
              autoComplete="new-password"
              value={form.api_key}
              onChange={(event) =>
                setForm((value) => ({
                  ...value,
                  api_key: event.target.value,
                }))
              }
            />
          </Field>

          <div className="border-t border-border pt-4">
            <p className="mb-4 text-sm font-medium text-foreground">
              {t("models.fields.reasoningGroup")}
            </p>
            <div className="admin-form-grid">
              <Field label={t("models.fields.contextWindow")} className={form.provider === "openai_chat_completions" || form.provider === "gemini" ? "col-span-2" : undefined}>
                <Input
                  type="number"
                  min={1}
                  required
                  value={form.context_window}
                  onChange={(event) =>
                    setForm((value) => ({
                      ...value,
                      context_window: Number(event.target.value) || 0,
                    }))
                  }
                />
              </Field>
              {form.provider === "gemini" ? (
                <>
                  <Field label={t("models.fields.thinkingBudget")} hint={t("models.fields.thinkingBudgetHint")}>
                    <Input
                      type="number"
                      min={-1}
                      step={1}
                      placeholder={t("models.fields.effortNone")}
                      value={form.thinking_budget === null ? "" : String(form.thinking_budget)}
                      onChange={(event) => {
                        const raw = event.target.value.trim();
                        const parsed = raw === "" ? null : Number(raw);
                        setForm((value) => ({
                          ...value,
                          thinking_budget: parsed !== null && Number.isFinite(parsed) ? parsed : null,
                        }));
                      }}
                    />
                  </Field>
                  <Field label={t("models.fields.thinkingLevel")}>
                    <NativeSelect value={form.thinking_level} onChange={(event) => setForm((value) => ({ ...value, thinking_level: event.target.value as ThinkingLevel }))}>
                      <option value="">{t("models.fields.effortNone")}</option>
                      {(["low", "medium", "high"] as const).map((level) => <option key={level} value={level}>{enumLabel(t, "thinkingLevel", level)}</option>)}
                    </NativeSelect>
                  </Field>
                </>
              ) : (
                <>
                  {form.provider === "openai_chat_completions" ? (
                    <Field label={t("models.fields.thinkingMode")}>
                      <NativeSelect value={form.thinking_mode} onChange={(event) => setForm((value) => ({ ...value, thinking_mode: event.target.value as ThinkingMode }))}>
                        <option value="">{t("models.fields.effortNone")}</option>
                        {(["enabled", "disabled"] as const).map((mode) => <option key={mode} value={mode}>{enumLabel(t, "thinkingMode", mode)}</option>)}
                      </NativeSelect>
                    </Field>
                  ) : null}
                  <Field label={t("models.fields.reasoningEffort")}>
                    <NativeSelect
                      value={form.reasoning_effort}
                      onChange={(event) =>
                        setForm((value) => ({
                          ...value,
                          reasoning_effort: event.target.value,
                        }))
                      }
                    >
                      <option value="">{t("models.fields.effortNone")}</option>
                      {["none", "low", "medium", "high", "xhigh", "max"].map((value) => (
                        <option key={value} value={value}>
                          {enumLabel(t, "reasoningEffort", value)}
                        </option>
                      ))}
                    </NativeSelect>
                  </Field>
                </>
              )}
            </div>
          </div>
          <div className="border-t border-border pt-4">
            <Field label={t("models.fields.bodyOverride")} hint={t("models.fields.bodyOverrideHint")} error={bodyError ? t(bodyError) : undefined}>
              <Textarea rows={6} className="font-mono" value={form.body_override} spellCheck={false} aria-invalid={Boolean(bodyError)} aria-describedby={bodyError ? "model-body-error" : undefined} onChange={(event) => setForm((value) => ({ ...value, body_override: event.target.value }))} />
            </Field>
            {bodyError ? <span id="model-body-error" role="alert" className="sr-only">{t(bodyError)}</span> : null}
          </div>
        </form>
        )}
      </EditPanel>

      <DirtyGuardDialog blocker={guard} />

      <ConfirmDialog
        open={discardOpen}
        onOpenChange={setDiscardOpen}
        title={t("models.discardTitle")}
        description={t("models.discardBody")}
        confirmLabel={t("models.discard")}
        onConfirm={() => {
          setDiscardOpen(false);
          setForm({ ...baseline });
          setLeaveAfter(true);
        }}
      />

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t("models.deleteTitle")}
        description={t("models.deleteBody", { name: deleting?.name || "" })}
        confirmLabel={t("common.delete")}
        loading={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </div>
  );
}
