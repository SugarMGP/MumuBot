import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { mcpApi } from "@/api/client-endpoints";
import type { MCPServerSummary } from "@/api/types";
import { ConfirmDialog } from "@/components/admin/confirm-dialog";
import { EditPanel } from "@/components/admin/edit-panel";
import { AdminIcon } from "@/components/admin/icons";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { SectionCard } from "@/components/admin/section-card";
import { SettingsTabs } from "@/components/admin/tabs";
import {
  toastApiError,
  toastSuccess,
  toastWarningCode,
} from "@/components/admin/toast";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Field,
  Input,
  NativeSelect,
  SwitchRow,
  Textarea,
} from "@/components/ui/form-controls";
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
import { cn } from "@/lib/cn";
import { formatDateTime } from "@/lib/format";
import { enumLabel } from "@/lib/labels";

interface FormState {
  name: string;
  enabled: boolean;
  type: string;
  url: string;
  command: string;
  args: string;
  env: string;
  headers: string;
  toolNames: string;
}

const emptyForm: FormState = {
  name: "",
  enabled: true,
  type: "sse",
  url: "",
  command: "",
  args: "",
  env: "",
  headers: "",
  toolNames: "",
};

function parseLines(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
}

function parseKeyValues(text: string): {
  values: Record<string, string>;
  error: string;
} {
  const values: Record<string, string> = {};
  const lines = text.split("\n");
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index].trim();
    if (!line) continue;
    const separator = line.indexOf("=");
    if (separator <= 0) return { values: {}, error: String(index + 1) };
    values[line.slice(0, separator).trim()] = line.slice(separator + 1).trim();
  }
  return { values, error: "" };
}

export default function McpPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { params, update } = useQueryParams();
  const editID = Number(params.get("edit") || "0");
  const panelOpen = params.has("edit");
  const editMode = editID > 0;
  const list = useQuery({ queryKey: ["mcp-servers"], queryFn: mcpApi.list });
  const detail = useQuery({
    queryKey: ["mcp-server", editID],
    queryFn: () => mcpApi.get(editID),
    enabled: editMode,
  });
  const [form, setForm] = useState<FormState>({ ...emptyForm });
  const [baseline, setBaseline] = useState<FormState>({ ...emptyForm });
  const [draftVersion, setDraftVersion] = useState(0);
  const [leaveAfter, setLeaveAfter] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState<MCPServerSummary | null>(null);
  const [discardOpen, setDiscardOpen] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const parsedHeaders = parseKeyValues(form.headers);
  const headerError = form.type === "sse" ? parsedHeaders.error : "";
  const loadedKey = useRef("");
  const dirty = useMemo(
    () => JSON.stringify(form) !== JSON.stringify(baseline),
    [form, baseline],
  );
  const versionConflict = Boolean(
    editMode && detail.data && draftVersion > 0 && detail.data.version !== draftVersion,
  );
  const guard = useDirtyGuard(dirty && !leaveAfter);

  useEffect(() => {
    if (!leaveAfter) return;
    update({ edit: undefined });
    setLeaveAfter(false);
  }, [leaveAfter, update]);

  useEffect(() => {
    if (!panelOpen) {
      loadedKey.current = "";
      setForm({ ...emptyForm });
      setBaseline({ ...emptyForm });
      setDraftVersion(0);
      setLeaveAfter(false);
      return;
    }
    if (!editMode) {
      if (loadedKey.current === "new") return;
      loadedKey.current = "new";
      setForm({ ...emptyForm });
      setBaseline({ ...emptyForm });
      setDraftVersion(0);
      setAdvancedOpen(false);
      return;
    }
    if (!detail.data) return;
    const key = `id:${detail.data.id}`;
    if (loadedKey.current === key) return;
    loadedKey.current = key;
    const next = {
      name: detail.data.name,
      enabled: detail.data.enabled,
      type: detail.data.type,
      url: detail.data.url,
      command: detail.data.command,
      args: detail.data.args.join("\n"),
      env: detail.data.env.join("\n"),
      headers: Object.entries(detail.data.headers)
        .map(([name, value]) => `${name}=${value}`)
        .join("\n"),
      toolNames: detail.data.tool_names.join(", "),
    };
    setForm(next);
    setBaseline(next);
    setDraftVersion(detail.data.version);
    setAdvancedOpen(false);
  }, [detail.data, editMode, panelOpen]);

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["mcp-servers"] });

  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    if (saving || versionConflict || headerError || (editMode && !detail.data)) return;
    setSaving(true);
    try {
      const payload = {
        name: form.name.trim(),
        enabled: form.enabled,
        type: form.type,
        url: form.url.trim(),
        command: form.command.trim(),
        args: parseLines(form.args),
        env: parseLines(form.env),
        headers: form.type === "sse" ? parsedHeaders.values : {},
        tool_names: form.toolNames
          .split(",")
          .map((value) => value.trim())
          .filter(Boolean),
      };
      if (editMode && detail.data) {
        const result = await mcpApi.update(editID, {
          ...payload,
          version: draftVersion,
        });
        if (result.warning) toastWarningCode(result.warning.code);
        else toastSuccess(t("mcp.saved"));
        setDraftVersion(result.data.version);
      } else if (!editMode) {
        const result = await mcpApi.create(payload);
        if (result.warning) toastWarningCode(result.warning.code);
        else toastSuccess(t("mcp.saved"));
      }
      await invalidate();
      setBaseline({ ...form });
      setLeaveAfter(true);
    } catch (error) {
      toastApiError(error);
      await invalidate();
      await detail.refetch();
    } finally {
      setSaving(false);
    }
  };

  const remove = useMutation({
    mutationFn: (server: MCPServerSummary) =>
      mcpApi.remove(server.id, server.version),
    onSuccess: async (result) => {
      setDeleting(null);
      await invalidate();
      if (result.warning) toastWarningCode(result.warning.code);
      else toastSuccess(t("mcp.deleted"));
    },
    onError: (error) => {
      setDeleting(null);
      toastApiError(error);
    },
  });

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
    loadedKey.current = "";
    setDraftVersion(0);
    await detail.refetch();
    await invalidate();
  };

  if (list.isPending) return <PageSkeleton />;
  if (list.isError || !list.data) {
    return (
      <p className="text-sm text-muted-foreground">{t("common.loadFailed")}</p>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <PageHeader
          title={t("mcp.title")}
          subtitle={t("mcp.subtitle")}
          actions={
            <Button onClick={() => update({ edit: "new" })}>
              {t("mcp.add")}
            </Button>
          }
        />
        <SettingsTabs />
      </div>

      {!list.data.secret_configured ? (
        <div className="rounded-xl border border-warning/40 bg-warning/10 px-4 py-3 text-sm text-warning">
          {t("mcp.secretUnavailable")}
        </div>
      ) : null}

      <SectionCard title={t("mcp.list")} description={t("mcp.listHint")}>
        {list.data.items.length === 0 ? (
          <p className="py-8 text-center text-sm text-muted-foreground">
            {t("mcp.empty")}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("mcp.name")}</TableHead>
                <TableHead>{t("mcp.protocol")}</TableHead>
                <TableHead>{t("common.status")}</TableHead>
                <TableHead>{t("mcp.versionLabel")}</TableHead>
                <TableHead className="text-right">{t("common.actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.data.items.map((server) => (
                <TableRow key={server.id}>
                  <TableCell>
                    <p className="font-medium">{server.name}</p>
                    <p className="mt-0.5 text-xs text-muted-foreground">
                      {formatDateTime(server.updated_at)}
                    </p>
                  </TableCell>
                  <TableCell>{enumLabel(t, "mcpType", server.transport)}</TableCell>
                  <TableCell>
                    <Badge variant={server.enabled ? "success" : "ghost"}>
                      {server.enabled ? t("mcp.enabled") : t("mcp.disabled")}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {t("mcp.version", { version: server.version })}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="inline-flex gap-1">
                      <Button
                        variant="ghost"
                        size="xs"
                        onClick={() => update({ edit: server.id })}
                      >
                        {t("mcp.edit")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="xs"
                        className="text-destructive hover:bg-destructive/10"
                        onClick={() => setDeleting(server)}
                      >
                        {t("mcp.delete")}
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
        title={editMode ? t("mcp.edit") : t("mcp.create")}
        description={t("mcp.panelHint")}
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
              form="mcp-form"
              disabled={
                saving ||
                versionConflict ||
                Boolean(headerError) ||
                (editMode && !detail.data)
              }
            >
              {saving ? <Spinner /> : null}
              {editMode ? t("mcp.save") : t("mcp.create")}
            </Button>
          </>
        }
      >
        {editMode && detail.isPending ? (
          <PageSkeleton />
        ) : editMode && detail.isError ? (
          <div className="space-y-3 py-10 text-center">
            <p className="text-sm text-muted-foreground">
              {t("mcp.detailFailed")}
            </p>
            <Button variant="outline" onClick={() => detail.refetch()}>
              {t("common.retry")}
            </Button>
          </div>
        ) : editMode && !detail.data ? (
          <p className="text-sm text-muted-foreground">
            {t("mcp.detailFailed")}
          </p>
        ) : (
          <form id="mcp-form" className="space-y-5" onSubmit={save}>
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
              <Field label={t("mcp.name")}>
                <Input
                  required
                  value={form.name}
                  onChange={(event) =>
                    setForm((value) => ({ ...value, name: event.target.value }))
                  }
                />
              </Field>
              <Field label={t("mcp.type")}>
                <NativeSelect
                  value={form.type}
                  onChange={(event) =>
                    setForm((value) => ({ ...value, type: event.target.value }))
                  }
                >
                  <option value="sse">{t("enums.mcpType.sse")}</option>
                  <option value="stdio">{t("enums.mcpType.stdio")}</option>
                </NativeSelect>
              </Field>
            </div>

            <SwitchRow
              checked={form.enabled}
              onCheckedChange={(checked) =>
                setForm((value) => ({ ...value, enabled: checked }))
              }
              label={t("mcp.enableServer")}
            />

            {form.type === "sse" ? (
              <>
                <Field label={t("mcp.url")}>
                  <Input
                    value={form.url}
                    onChange={(event) =>
                      setForm((value) => ({ ...value, url: event.target.value }))
                    }
                  />
                </Field>
                <Field
                  label={t("mcp.headers")}
                  error={
                    parsedHeaders.error
                      ? t("mcp.invalidKeyValue", {
                          line: parsedHeaders.error,
                        })
                      : undefined
                  }
                >
                  <Textarea
                    rows={4}
                    className="font-mono text-xs"
                    value={form.headers}
                    onChange={(event) =>
                      setForm((value) => ({
                        ...value,
                        headers: event.target.value,
                      }))
                    }
                  />
                </Field>
              </>
            ) : (
              <>
                <Field label={t("mcp.command")}>
                  <Input
                    value={form.command}
                    onChange={(event) =>
                      setForm((value) => ({
                        ...value,
                        command: event.target.value,
                      }))
                    }
                  />
                </Field>
                <div className="admin-form-grid">
                  <Field label={t("mcp.args")}>
                    <Textarea
                      rows={4}
                      className="font-mono text-xs"
                      value={form.args}
                      onChange={(event) =>
                        setForm((value) => ({
                          ...value,
                          args: event.target.value,
                        }))
                      }
                    />
                  </Field>
                  <Field label={t("mcp.env")}>
                    <Textarea
                      rows={4}
                      className="font-mono text-xs"
                      value={form.env}
                      onChange={(event) =>
                        setForm((value) => ({
                          ...value,
                          env: event.target.value,
                        }))
                      }
                    />
                  </Field>
                </div>
              </>
            )}

            <div className="rounded-xl border border-border bg-muted/20">
              <button
                type="button"
                className="flex w-full items-center justify-between px-4 py-3 text-sm font-medium text-foreground"
                aria-expanded={advancedOpen}
                onClick={() => setAdvancedOpen((value) => !value)}
              >
                <span>{t("mcp.advanced")}</span>
                <AdminIcon
                  name="chevron-down"
                  className={cn(
                    "size-4 text-muted-foreground transition-transform duration-150",
                    advancedOpen && "rotate-180",
                  )}
                />
              </button>
              <div
                className={cn(
                  "grid transition-[grid-template-rows] duration-150 ease-out",
                  advancedOpen ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
                )}
              >
                <div className="overflow-hidden" inert={!advancedOpen}>
                  <div className="px-4 pb-4">
                    <Field label={t("mcp.toolNames")}>
                      <Input
                        value={form.toolNames}
                        onChange={(event) =>
                          setForm((value) => ({
                            ...value,
                            toolNames: event.target.value,
                          }))
                        }
                      />
                    </Field>
                  </div>
                </div>
              </div>
            </div>
          </form>
        )}
      </EditPanel>

      <DirtyGuardDialog blocker={guard} />

      <ConfirmDialog
        open={discardOpen}
        onOpenChange={setDiscardOpen}
        title={t("mcp.discardTitle")}
        description={t("mcp.discardBody")}
        confirmLabel={t("mcp.discard")}
        onConfirm={() => {
          setDiscardOpen(false);
          setForm({ ...baseline });
          setLeaveAfter(true);
        }}
      />

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t("mcp.deleteTitle")}
        description={t("mcp.deleteBody", { name: deleting?.name || "" })}
        confirmLabel={t("common.delete")}
        loading={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </div>
  );
}
