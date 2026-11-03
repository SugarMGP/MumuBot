import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ApiError } from "@/api/client";
import { settingsApi } from "@/api/client-endpoints";
import type {
  RuntimeConfig,
  SettingsData,
  TimeRuleConfig,
} from "@/api/types";
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
import { Button } from "@/components/ui/button";
import {
  CheckboxRow,
  Field,
  Input,
  Textarea,
} from "@/components/ui/form-controls";
import { Spinner } from "@/components/ui/spinner";

type Section = keyof Pick<
  RuntimeConfig,
  "agent" | "chat" | "compactor" | "maintenance" | "media" | "debug"
>;

function percent(value: number): number {
  return Number((value * 100).toFixed(1));
}

function ratio(value: number): number {
  return value / 100;
}

function numberValue(value: string): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function rulesToText(rules: TimeRuleConfig[]): string {
  return rules
    .map((rule) => `${rule.time_range},${rule.group_id},${rule.talk_value}`)
    .join("\n");
}

function parseRules(text: string): { rules: TimeRuleConfig[]; error: string } {
  const rules: TimeRuleConfig[] = [];
  const lines = text.split("\n");
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index].trim();
    if (!line) continue;
    const parts = line.split(",");
    if (parts.length !== 3) {
      return { rules: [], error: String(index + 1) };
    }
    const groupID = Number(parts[1].trim());
    const talk = Number(parts[2].trim());
    if (!Number.isFinite(groupID) || !Number.isFinite(talk)) {
      return { rules: [], error: String(index + 1) };
    }
    rules.push({
      time_range: parts[0].trim(),
      group_id: groupID,
      talk_value: talk,
    });
  }
  return { rules, error: "" };
}

function SaveButtons({
  busy,
  disabled,
  onSave,
  onReset,
}: {
  busy: boolean;
  disabled?: boolean;
  onSave: () => void;
  onReset: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-2 border-t border-border pt-4">
      <Button size="sm" onClick={onSave} disabled={busy || disabled}>
        {busy ? <Spinner /> : null}
        {t("settings.save")}
      </Button>
      <Button size="sm" variant="ghost" onClick={onReset} disabled={busy}>
        {t("settings.reset")}
      </Button>
    </div>
  );
}

export default function SettingsPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: ["settings"], queryFn: settingsApi.get });
  const [config, setConfig] = useState<RuntimeConfig | null>(null);
  const [versions, setVersions] = useState<Record<string, number>>({});
  const [agentReady, setAgentReady] = useState(true);
  const [secretConfigured, setSecretConfigured] = useState(true);
  const [busy, setBusy] = useState("");
  const [resetSection, setResetSection] = useState<Section | null>(null);
  const [rulesText, setRulesText] = useState("");
  const parsedRules = parseRules(rulesText);

  const applySettings = (data: SettingsData) => {
    setConfig(data.config);
    setVersions(data.versions);
    setAgentReady(data.agent_ready);
    setSecretConfigured(data.secret_configured);
    setRulesText(rulesToText(data.config.chat.time_rules));
  };

  useEffect(() => {
    if (query.data) applySettings(query.data);
  }, [query.data]);

  const refetch = async () => {
    try {
      const result = await settingsApi.get();
      queryClient.setQueryData(["settings"], result);
      applySettings(result);
    } catch (error) {
      toastApiError(error);
    }
  };

  const update = <K extends Section>(section: K, value: Partial<RuntimeConfig[K]>) => {
    setConfig((previous) =>
      previous
        ? { ...previous, [section]: { ...previous[section], ...value } }
        : previous,
    );
  };

  const saveSection = async (section: Section, values: unknown) => {
    setBusy(section);
    try {
      const result = await settingsApi.save(
        section,
        values,
        versions[section] ?? 0,
      );
      applySettings(result.data);
      if (result.warning) toastWarningCode(result.warning.code);
      else toastSuccess(t("settings.saved"));
    } catch (error) {
      toastApiError(error);
      if (error instanceof ApiError && error.code === "version_conflict") {
        await refetch();
      }
    } finally {
      setBusy("");
    }
  };

  const reset = async () => {
    if (!resetSection) return;
    setBusy(resetSection);
    try {
      const result = await settingsApi.reset(
        resetSection,
        versions[resetSection] ?? 0,
      );
      applySettings(result.data);
      if (result.warning) toastWarningCode(result.warning.code);
      else toastSuccess(t("settings.resetDone"));
    } catch (error) {
      toastApiError(error);
      if (error instanceof ApiError && error.code === "version_conflict") {
        await refetch();
      }
    } finally {
      setBusy("");
      setResetSection(null);
    }
  };

  if (query.isPending) return <PageSkeleton />;
  if (query.isError || !config) {
    return (
      <p className="text-sm text-muted-foreground">{t("common.loadFailed")}</p>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-end justify-between gap-6">
        <PageHeader
          title={t("settings.title")}
          subtitle={t("settings.subtitle")}
        />
        <SettingsTabs />
      </div>

      {!secretConfigured ? (
        <div className="rounded-xl border border-warning/40 bg-warning/10 px-4 py-3 text-sm text-warning">
          {t("common.secretUnavailable")}
        </div>
      ) : null}
      {!agentReady ? (
        <div className="rounded-xl border border-border bg-muted px-4 py-3 text-sm text-muted-foreground">
          {t("common.agentNotReady")}
        </div>
      ) : null}

      <SectionCard
        title={t("settings.agent.title")}
        description={t("settings.agent.hint")}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label={t("settings.agent.thinkInterval")}>
            <Input
              type="number"
              min={1}
              value={config.agent.think_interval}
              onChange={(event) =>
                update("agent", { think_interval: numberValue(event.target.value) })
              }
            />
          </Field>
          <Field label={t("settings.agent.debounce")}>
            <Input
              type="number"
              min={0}
              value={config.agent.think_debounce_ms}
              onChange={(event) =>
                update("agent", {
                  think_debounce_ms: numberValue(event.target.value),
                })
              }
            />
          </Field>
          <Field label={t("settings.agent.maxStep")}>
            <Input
              type="number"
              min={1}
              max={1000}
              value={config.agent.max_step}
              onChange={(event) =>
                update("agent", { max_step: numberValue(event.target.value) })
              }
            />
          </Field>
          <Field label={t("settings.agent.groupConcurrency")}>
            <Input
              type="number"
              min={0}
              value={config.agent.max_group_concurrency}
              onChange={(event) =>
                update("agent", {
                  max_group_concurrency: numberValue(event.target.value),
                })
              }
            />
          </Field>
          <Field label={t("settings.agent.privateConcurrency")}>
            <Input
              type="number"
              min={0}
              value={config.agent.max_private_concurrency}
              onChange={(event) =>
                update("agent", {
                  max_private_concurrency: numberValue(event.target.value),
                })
              }
            />
          </Field>
        </div>
        <SaveButtons
          busy={busy === "agent"}
          onSave={() => saveSection("agent", config.agent)}
          onReset={() => setResetSection("agent")}
        />
      </SectionCard>

      <SectionCard
        title={t("settings.chat.title")}
        description={t("settings.chat.hint")}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label={t("settings.chat.talkFrequency")}>
            <Input
              type="number"
              step="0.01"
              min={0}
              max={1}
              value={config.chat.talk_frequency}
              onChange={(event) =>
                update("chat", {
                  talk_frequency: numberValue(event.target.value),
                })
              }
            />
          </Field>
          <Field label={t("settings.chat.typingSpeed")}>
            <Input
              type="number"
              min={1}
              value={config.chat.typing_speed}
              onChange={(event) =>
                update("chat", { typing_speed: numberValue(event.target.value) })
              }
            />
          </Field>
        </div>
        <CheckboxRow
          checked={config.chat.typing_simulation}
          onCheckedChange={(checked) =>
            update("chat", { typing_simulation: checked })
          }
          label={t("settings.chat.typingSimulation")}
        />
        <CheckboxRow
          checked={config.chat.enable_time_rules}
          onCheckedChange={(checked) =>
            update("chat", { enable_time_rules: checked })
          }
          label={t("settings.chat.enableTimeRules")}
        />
        <Field
          label={t("settings.chat.timeRules")}
          error={
            parsedRules.error
              ? t("settings.chat.invalidRule", { line: parsedRules.error })
              : undefined
          }
        >
          <Textarea
            rows={5}
            className="font-mono text-xs"
            value={rulesText}
            placeholder={t("settings.chat.timeRulesPlaceholder")}
            onChange={(event) => setRulesText(event.target.value)}
          />
        </Field>
        <CheckboxRow
          checked={config.chat.rate_limit.enabled}
          onCheckedChange={(checked) =>
            update("chat", {
              rate_limit: { ...config.chat.rate_limit, enabled: checked },
            })
          }
          label={t("settings.chat.rateEnabled")}
        />
        <div className="grid grid-cols-3 gap-3">
          <Field label={t("settings.chat.periodSec")}>
            <Input
              type="number"
              min={1}
              value={config.chat.rate_limit.period_sec}
              onChange={(event) =>
                update("chat", {
                  rate_limit: {
                    ...config.chat.rate_limit,
                    period_sec: numberValue(event.target.value),
                  },
                })
              }
            />
          </Field>
          <Field label={t("settings.chat.maxMessages")}>
            <Input
              type="number"
              min={1}
              value={config.chat.rate_limit.max_messages}
              onChange={(event) =>
                update("chat", {
                  rate_limit: {
                    ...config.chat.rate_limit,
                    max_messages: numberValue(event.target.value),
                  },
                })
              }
            />
          </Field>
          <Field label={t("settings.chat.minProb")}>
            <Input
              type="number"
              step="0.01"
              min={0}
              max={1}
              value={config.chat.rate_limit.min_prob}
              onChange={(event) =>
                update("chat", {
                  rate_limit: {
                    ...config.chat.rate_limit,
                    min_prob: numberValue(event.target.value),
                  },
                })
              }
            />
          </Field>
        </div>
        <SaveButtons
          busy={busy === "chat"}
          disabled={Boolean(parsedRules.error)}
          onSave={() =>
            saveSection("chat", {
              ...config.chat,
              time_rules: parsedRules.rules,
            })
          }
          onReset={() => setResetSection("chat")}
        />
      </SectionCard>

      <SectionCard
        title={t("settings.compactor.title")}
        description={t("settings.compactor.hint")}
      >
        <div className="grid grid-cols-3 gap-3">
          <Field label={t("settings.compactor.triggerRatio")}>
            <Input
              type="number"
              step="0.1"
              min={1}
              max={99}
              value={percent(config.compactor.trigger_ratio)}
              onChange={(event) =>
                update("compactor", {
                  trigger_ratio: ratio(numberValue(event.target.value)),
                })
              }
            />
          </Field>
          <Field label={t("settings.compactor.digestBudget")}>
            <Input
              type="number"
              step="0.1"
              min={10}
              max={60}
              value={percent(config.compactor.digest_budget_ratio)}
              onChange={(event) =>
                update("compactor", {
                  digest_budget_ratio: ratio(numberValue(event.target.value)),
                })
              }
            />
          </Field>
          <Field label={t("settings.compactor.recentTail")}>
            <Input
              type="number"
              step="0.1"
              min={5}
              max={30}
              value={percent(config.compactor.recent_tail_ratio)}
              onChange={(event) =>
                update("compactor", {
                  recent_tail_ratio: ratio(numberValue(event.target.value)),
                })
              }
            />
          </Field>
        </div>
        <div className="grid grid-cols-3 gap-3">
          <Field label={t("settings.compactor.maxSteps")}>
            <Input
              type="number"
              min={1}
              max={100}
              value={config.compactor.max_steps}
              onChange={(event) =>
                update("compactor", {
                  max_steps: numberValue(event.target.value),
                })
              }
            />
          </Field>
          <Field
            label={t("settings.compactor.idleMinutes")}
            hint={t("settings.compactor.idleOff")}
          >
            <Input
              type="number"
              min={0}
              max={240}
              value={config.compactor.idle_minutes}
              onChange={(event) =>
                update("compactor", {
                  idle_minutes: numberValue(event.target.value),
                })
              }
            />
          </Field>
          <Field label={t("settings.compactor.idleMinRatio")}>
            <Input
              type="number"
              step="0.1"
              min={45}
              max={85}
              value={percent(config.compactor.idle_min_ratio)}
              onChange={(event) =>
                update("compactor", {
                  idle_min_ratio: ratio(numberValue(event.target.value)),
                })
              }
            />
          </Field>
        </div>
        <SaveButtons
          busy={busy === "compactor"}
          onSave={() => saveSection("compactor", config.compactor)}
          onReset={() => setResetSection("compactor")}
        />
      </SectionCard>

      <SectionCard
        title={t("settings.maintenance.title")}
        description={t("settings.maintenance.hint")}
      >
        <div className="grid grid-cols-2 gap-3">
          <Field label={t("settings.maintenance.memoryTurns")}>
            <Input
              type="number"
              min={0}
              max={50}
              value={config.maintenance.memory_turns}
              onChange={(event) =>
                update("maintenance", {
                  memory_turns: numberValue(event.target.value),
                })
              }
            />
          </Field>
          <Field label={t("settings.maintenance.topicTurns")}>
            <Input
              type="number"
              min={0}
              max={50}
              value={config.maintenance.topic_turns}
              onChange={(event) =>
                update("maintenance", {
                  topic_turns: numberValue(event.target.value),
                })
              }
            />
          </Field>
        </div>
        <SaveButtons
          busy={busy === "maintenance"}
          onSave={() => saveSection("maintenance", config.maintenance)}
          onReset={() => setResetSection("maintenance")}
        />
      </SectionCard>

      <SectionCard
        title={t("settings.media.title")}
        description={t("settings.media.hint")}
      >
        <div className="grid grid-cols-3 gap-3">
          <Field label={t("settings.media.maxBytesMB")}>
            <Input
              type="number"
              min={1}
              value={config.media.max_bytes_mb}
              onChange={(event) =>
                update("media", {
                  max_bytes_mb: numberValue(event.target.value),
                })
              }
            />
          </Field>
          <Field label={t("settings.media.imageQuality")}>
            <Input
              type="number"
              min={1}
              max={100}
              value={config.media.image_quality}
              onChange={(event) =>
                update("media", {
                  image_quality: numberValue(event.target.value),
                })
              }
            />
          </Field>
          <Field label={t("settings.media.narrowEdge")}>
            <Input
              type="number"
              min={1}
              value={config.media.narrow_edge}
              onChange={(event) =>
                update("media", {
                  narrow_edge: numberValue(event.target.value),
                })
              }
            />
          </Field>
        </div>
        <SaveButtons
          busy={busy === "media"}
          onSave={() => saveSection("media", config.media)}
          onReset={() => setResetSection("media")}
        />
      </SectionCard>

      <SectionCard
        title={t("settings.debug.title")}
        description={t("settings.debug.hint")}
      >
        <CheckboxRow
          checked={config.debug.show_tool_calls}
          onCheckedChange={(checked) =>
            update("debug", { show_tool_calls: checked })
          }
          label={t("settings.debug.showToolCalls")}
        />
        <SaveButtons
          busy={busy === "debug"}
          onSave={() => saveSection("debug", config.debug)}
          onReset={() => setResetSection("debug")}
        />
      </SectionCard>

      <ConfirmDialog
        open={resetSection !== null}
        onOpenChange={(open) => !open && setResetSection(null)}
        title={t("settings.resetTitle")}
        description={t("settings.resetBody")}
        confirmLabel={t("settings.reset")}
        loading={busy !== ""}
        destructive={false}
        onConfirm={reset}
      />
    </div>
  );
}
