import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ApiError } from "@/api/client";
import { mcpApi } from "@/api/client-endpoints";
import type { MCPServerSummary } from "@/api/types";
import { ConfirmDialog } from "@/components/admin/confirm-dialog";
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
import { useQueryParams } from "@/hooks/use-query-params";
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
  const list = useQuery({ queryKey: ["mcp-servers"], queryFn: mcpApi.list });
  const detail = useQuery({
    queryKey: ["mcp-server", editID],
    queryFn: () => mcpApi.get(editID),
    enabled: editID > 0,
  });
  const [form, setForm] = useState<FormState>({ ...emptyForm });
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState<MCPServerSummary | null>(null);
  const parsedHeaders = parseKeyValues(form.headers);

  useEffect(() => {
    if (detail.data) {
      setForm({
        name: detail.data.name,
        enabled: detail.data.enabled,
        type: detail.data.type,
        url: detail.data.url,
        command: detail.data.command,
        args: detail.data.args.join("\n"),
        env: detail.data.env.join("\n"),
        headers: Object.entries(detail.data.headers)
          .map(([key, value]) => `${key}=${value}`)
          .join("\n"),
        toolNames: detail.data.tool_names.join(", "),
      });
      return;
    }
    setForm({ ...emptyForm });
  }, [detail.data]);

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["mcp-servers"] });

  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    if (saving || parsedHeaders.error) return;
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
        headers: parsedHeaders.values,
        tool_names: form.toolNames
          .split(",")
          .map((value) => value.trim())
          .filter(Boolean),
      };
      if (editID > 0 && detail.data) {
        const result = await mcpApi.update(editID, {
          ...payload,
          version: detail.data.version,
        });
        if (result.warning) toastWarningCode(result.warning.code);
        else toastSuccess(t("mcp.saved"));
      } else {
        await mcpApi.create(payload);
        toastSuccess(t("mcp.saved"));
      }
      await invalidate();
      update({ edit: undefined });
    } catch (error) {
      toastApiError(error);
      if (error instanceof ApiError && error.code === "version_conflict") {
        update({ edit: undefined });
      }
      await invalidate();
    } finally {
      setSaving(false);
    }
  };

  const remove = useMutation({
    mutationFn: (server: MCPServerSummary) =>
      mcpApi.remove(server.id, server.version),
    onSuccess: async () => {
      setDeleting(null);
      await invalidate();
      toastSuccess(t("mcp.deleted"));
    },
    onError: (error) => {
      setDeleting(null);
      toastApiError(error);
    },
  });

  if (list.isPending) return <PageSkeleton />;
  if (list.isError || !list.data) {
    return (
      <p className="text-sm text-muted-foreground">{t("common.loadFailed")}</p>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-end justify-between gap-6">
        <PageHeader title={t("mcp.title")} subtitle={t("mcp.subtitle")} />
        <SettingsTabs />
      </div>

      {!list.data.secret_configured ? (
        <div className="rounded-xl border border-warning/40 bg-warning/10 px-4 py-3 text-sm text-warning">
          {t("mcp.secretUnavailable")}
        </div>
      ) : null}

      <SectionCard
        title={t("mcp.list")}
        description={t("mcp.listHint")}
      >
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
                <TableHead className="text-right">
                  {t("common.actions")}
                </TableHead>
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
                  <TableCell>
                    {enumLabel(t, "mcpType", server.transport)}
                  </TableCell>
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

      <SectionCard
        title={editID > 0 ? t("mcp.edit") : t("mcp.addOrEdit")}
        description={t("mcp.addHint")}
        actions={
          editID > 0 ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => update({ edit: undefined })}
            >
              {t("mcp.cancelEdit")}
            </Button>
          ) : null
        }
      >
        {editID > 0 && detail.isPending ? (
          <PageSkeleton />
        ) : (
          <form className="space-y-3" onSubmit={save}>
            <div className="grid grid-cols-2 gap-3">
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
            <Field label={t("mcp.url")}>
              <Input
                value={form.url}
                onChange={(event) =>
                  setForm((value) => ({ ...value, url: event.target.value }))
                }
              />
            </Field>
            <Field label={t("mcp.command")}>
              <Input
                value={form.command}
                onChange={(event) =>
                  setForm((value) => ({ ...value, command: event.target.value }))
                }
              />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label={t("mcp.args")}>
                <Textarea
                  rows={4}
                  className="font-mono text-xs"
                  value={form.args}
                  onChange={(event) =>
                    setForm((value) => ({ ...value, args: event.target.value }))
                  }
                />
              </Field>
              <Field label={t("mcp.env")}>
                <Textarea
                  rows={4}
                  className="font-mono text-xs"
                  value={form.env}
                  onChange={(event) =>
                    setForm((value) => ({ ...value, env: event.target.value }))
                  }
                />
              </Field>
            </div>
            <Field
              label={t("mcp.headers")}
              error={
                parsedHeaders.error
                  ? t("mcp.invalidKeyValue", { line: parsedHeaders.error })
                  : undefined
              }
            >
              <Textarea
                rows={4}
                className="font-mono text-xs"
                value={form.headers}
                onChange={(event) =>
                  setForm((value) => ({ ...value, headers: event.target.value }))
                }
              />
            </Field>
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
            <div className="flex items-center gap-2 border-t border-border pt-4">
              <Button
                type="submit"
                disabled={saving || Boolean(parsedHeaders.error)}
              >
                {saving ? <Spinner /> : null}
                {editID > 0 ? t("mcp.save") : t("mcp.create")}
              </Button>
            </div>
          </form>
        )}
      </SectionCard>

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
