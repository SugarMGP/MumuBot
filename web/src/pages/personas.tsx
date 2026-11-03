import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { personaApi } from "@/api/client-endpoints";
import type { PersonaDetail, PersonaTemplate } from "@/api/types";
import { ConfirmDialog } from "@/components/admin/confirm-dialog";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { toastApiError, toastSuccess, toastWarningCode } from "@/components/admin/toast";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Field, Input, Textarea } from "@/components/ui/form-controls";
import { Spinner } from "@/components/ui/spinner";
import { useQueryParams } from "@/hooks/use-query-params";
import { cn } from "@/lib/cn";
import { formatDateTime } from "@/lib/format";
import {
  parsePersonaMode,
  personaMarkerPattern,
  personaMarkerToken,
  type PersonaMode,
} from "@/lib/persona-marker";

const CARD_LIMIT = 10000;
const SIM_LIMIT = 5000;

interface PersonaBlock {
  mode: PersonaMode;
  startLine: number;
  markerLine: number;
  rest: string;
  lines: string[];
}

function parseBlocks(text: string): PersonaBlock[] {
  const lines = text.split("\n");
  const blocks: PersonaBlock[] = [];
  let current: PersonaBlock = {
    mode: "common",
    startLine: 0,
    markerLine: -1,
    rest: "",
    lines: [],
  };
  lines.forEach((line, index) => {
    const trimmed = line.trim();
    const marker = trimmed.match(personaMarkerPattern);
    if (marker) {
      if (current.lines.length > 0) blocks.push(current);
      current = {
        mode: parsePersonaMode(marker[1]),
        startLine: index,
        markerLine: index,
        rest: marker[2].trim(),
        lines: [],
      };
      return;
    }
    if (/^##\s/.test(trimmed)) {
      if (current.lines.length > 0) blocks.push(current);
      current = {
        mode: "common",
        startLine: index,
        markerLine: -1,
        rest: "",
        lines: [line],
      };
      return;
    }
    if (current.lines.length === 0) current.startLine = index;
    current.lines.push(line);
  });
  if (current.lines.length > 0 || current.markerLine >= 0) {
    blocks.push(current);
  }
  return blocks;
}

function setBlockMode(
  text: string,
  block: PersonaBlock,
  mode: PersonaMode,
): string {
  const lines = text.split("\n");
  const marker = `[${personaMarkerToken[mode]}]`;
  if (block.markerLine >= 0) {
    lines[block.markerLine] = block.rest
      ? `${marker} ${block.rest}`
      : marker;
  } else {
    lines.splice(block.startLine, 0, marker);
  }
  return lines.join("\n");
}

function BlockPreview({
  text,
  onModeChange,
  readOnly,
}: {
  text: string;
  onModeChange: (next: string) => void;
  readOnly?: boolean;
}) {
  const { t } = useTranslation();
  const blocks = useMemo(() => parseBlocks(text), [text]);
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">
          {t("personas.preview.title")}
        </h3>
        <p className="text-xs text-muted-foreground">
          {t("personas.preview.hint")}
        </p>
      </div>
      {blocks.length === 0 ? (
        <p className="rounded-lg border border-dashed border-border px-3 py-6 text-center text-xs text-muted-foreground">
          {t("personas.preview.empty")}
        </p>
      ) : (
        <div className="space-y-2">
          {blocks.map((block, index) => (
            <div
              key={`${block.startLine}-${index}`}
              className="rounded-lg border border-border bg-muted/40 p-3"
            >
              <div className="flex items-center justify-between gap-2">
                <Badge
                  variant={
                    block.mode === "group"
                      ? "info"
                      : block.mode === "private"
                        ? "accent"
                        : "ghost"
                  }
                >
                  {t(`personas.preview.${block.mode}`)}
                </Badge>
                <div className="flex gap-1">
                  {(["group", "private", "common"] as PersonaMode[]).map((mode) => (
                    <Button
                      key={mode}
                      variant="ghost"
                      size="xs"
                      className={cn(
                        "text-xs",
                        block.mode === mode && "text-primary",
                      )}
                      onClick={() => onModeChange(setBlockMode(text, block, mode))}
                      disabled={readOnly}
                    >
                      {t(`personas.preview.${mode}`)}
                    </Button>
                  ))}
                </div>
              </div>
              {block.rest ? (
                <p className="mt-2 text-xs text-foreground/75">{block.rest}</p>
              ) : null}
              {block.lines.length > 0 ? (
                <pre className="mt-2 whitespace-pre-wrap break-words font-sans text-xs leading-5 text-foreground/75">
                  {block.lines.join("\n")}
                </pre>
              ) : null}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export default function PersonasPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { params } = useQueryParams();
  const selectedID = Number(params.get("id") || "0");
  const creating = params.get("new") === "1";
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [personaCard, setPersonaCard] = useState("");
  const [simulationPrompt, setSimulationPrompt] = useState("");
  const [deleteOpen, setDeleteOpen] = useState(false);
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

  useEffect(() => {
    if (creating) return;
    if (list.data && selectedID === 0) {
      const target = list.data.active_id || list.data.items[0]?.id;
      if (target) {
        navigate(`/admin/personas?id=${target}`, { replace: true });
      }
    }
  }, [creating, list.data, selectedID, navigate]);

  useLayoutEffect(() => {
    if (creating) {
      const source: PersonaTemplate | undefined = template.data;
      if (!source || loadedKey.current === "new") return;
      loadedKey.current = "new";
      setName("");
      setDescription("");
      setPersonaCard(source.persona_card);
      setSimulationPrompt(source.simulation_prompt);
      return;
    }
    if (!detail.data) return;
    const key = `id:${detail.data.id}:${detail.data.version}`;
    if (loadedKey.current === key) return;
    loadedKey.current = key;
    setName(detail.data.name);
    setDescription(detail.data.description);
    setPersonaCard(detail.data.persona_card);
    setSimulationPrompt(detail.data.simulation_prompt);
  }, [creating, detail.data, template.data]);

  const save = useMutation({
    mutationFn: () => {
      const payload = {
        name: name.trim(),
        description: description.trim(),
        persona_card: personaCard,
        simulation_prompt: simulationPrompt,
      };
      if (creating) return personaApi.create(payload);
      return personaApi.update(selectedID, {
        ...payload,
        version: detail.data?.version ?? 0,
      });
    },
    onSuccess: async (result) => {
      if (result.warning) toastWarningCode(result.warning.code);
      else
        toastSuccess(
          creating ? t("personas.editor.created") : t("personas.editor.saved"),
        );
      await queryClient.invalidateQueries({ queryKey: ["personas"] });
      if (creating) {
        navigate(`/admin/personas?id=${result.data.id}`, { replace: true });
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
        navigate(`/admin/personas?id=${result.data.id}`);
      } else {
        await personaApi.remove(selectedID);
        toastSuccess(t("personas.editor.deleted"));
        navigate("/admin/personas", { replace: true });
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

  const selected = detail.data as PersonaDetail | undefined;
  const isBuiltin = selected?.builtin ?? false;
  const isActive = selected?.active ?? false;
  const cardChars = Array.from(personaCard).length;
  const simChars = Array.from(simulationPrompt).length;
  const busy = save.isPending || busyAction !== "";

  if (list.isPending) return <PageSkeleton />;

  return (
    <div className="space-y-6">
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

      <div className="grid grid-cols-[clamp(240px,20vw,300px)_minmax(0,1fr)] items-start gap-5">
        <Card>
          <div className="space-y-3 p-4">
            <h2 className="text-lg font-semibold text-foreground">
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
                          <Badge>{t("personas.inUse")}</Badge>
                        ) : null}
                        {preset.builtin ? (
                          <Badge variant="ghost">
                            {t("personas.builtin")}
                          </Badge>
                        ) : null}
                      </span>
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {t("personas.chars", {
                        card: preset.card_chars,
                        sim: preset.sim_chars,
                      })}
                    </p>
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
          <div className="space-y-4 p-5">
            {(detail.isPending && !creating) ||
            (creating && template.isPending) ? (
              <PageSkeleton />
            ) : (
              <>
                <div className="flex items-center justify-between gap-3">
                  <h2 className="text-lg font-semibold text-foreground">
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

                <div className="grid grid-cols-2 gap-3">
                  <Field label={t("personas.editor.name")}>
                    <Input
                      value={name}
                      onChange={(event) => setName(event.target.value)}
                      disabled={isBuiltin}
                    />
                  </Field>
                  <Field label={t("personas.editor.description")}>
                    <Input
                      value={description}
                      onChange={(event) => setDescription(event.target.value)}
                      disabled={isBuiltin}
                    />
                  </Field>
                </div>

                <Field
                  label={t("personas.editor.personaCard")}
                  hint={t("personas.editor.cardHint")}
                >
                  <Textarea
                    rows={12}
                    value={personaCard}
                    onChange={(event) => setPersonaCard(event.target.value)}
                    disabled={isBuiltin}
                    className="font-mono text-xs"
                  />
                </Field>
                <p
                  className={cn(
                    "text-right text-xs",
                    cardChars > CARD_LIMIT
                      ? "text-destructive"
                      : "text-muted-foreground",
                  )}
                >
                  {t("personas.editor.charCount", {
                    current: cardChars,
                    limit: CARD_LIMIT,
                  })}
                </p>

                <Field
                  label={t("personas.editor.simulationPrompt")}
                  hint={t("personas.editor.simHint")}
                >
                  <Textarea
                    rows={10}
                    value={simulationPrompt}
                    onChange={(event) =>
                      setSimulationPrompt(event.target.value)
                    }
                    disabled={isBuiltin}
                    className="font-mono text-xs"
                  />
                </Field>
                <p
                  className={cn(
                    "text-right text-xs",
                    simChars > SIM_LIMIT
                      ? "text-destructive"
                      : "text-muted-foreground",
                  )}
                >
                  {t("personas.editor.charCount", {
                    current: simChars,
                    limit: SIM_LIMIT,
                  })}
                </p>

                <BlockPreview
                  text={personaCard}
                  onModeChange={setPersonaCard}
                  readOnly={isBuiltin}
                />

                <div className="flex flex-wrap items-center gap-2 border-t border-border pt-4">
                  <Button
                    onClick={() => save.mutate()}
                    disabled={busy || isBuiltin || !name.trim()}
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
                        disabled={busy || isActive}
                      >
                        {t("personas.editor.activate")}
                      </Button>
                      <Button
                        variant="outline"
                        onClick={() => runAction("clone")}
                        disabled={busy}
                      >
                        {t("personas.editor.clone")}
                      </Button>
                      <Button
                        variant="ghost"
                        className="ml-auto text-destructive hover:bg-destructive/10"
                        onClick={() => setDeleteOpen(true)}
                        disabled={busy || isBuiltin || isActive}
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
