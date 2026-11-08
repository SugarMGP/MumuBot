import { useQuery, useQueryClient } from "@tanstack/react-query";
import { motion, useReducedMotion } from "motion/react";
import { useEffect, useRef, useState } from "react";
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
import { DirtyGuardDialog, useDirtyGuard } from "@/hooks/use-dirty-guard";

type Section = keyof Pick<
  RuntimeConfig,
  "agent" | "chat" | "compactor" | "maintenance" | "retention" | "media" | "debug"
>;

const sectionOrder: Section[] = [
  "agent",
  "chat",
  "compactor",
  "maintenance",
  "retention",
  "media",
  "debug",
];

function sameSection(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

function percent(value: number): number {
  return Number((value * 100).toFixed(1));
}

function ratio(value: number): number {
  return value / 100;
}

function forgettingDays(halfLife: number, threshold: number): number {
  const safe = Math.min(0.5, Math.max(0.01, threshold));
  return Math.round(halfLife * Math.log2(1 / safe));
}

function numberValue(value: string): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function rulesToText(rules: TimeRuleConfig[] | null): string {
  return (rules ?? [])
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
      <Button onClick={onSave} disabled={busy || disabled}>
        {busy ? <Spinner /> : null}
        {t("settings.save")}
      </Button>
      <Button variant="ghost" onClick={onReset} disabled={busy}>
        {t("settings.reset")}
      </Button>
    </div>
  );
}

export default function SettingsPage() {
  const { t } = useTranslation();
  const reducedMotion = useReducedMotion();
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: ["settings"], queryFn: settingsApi.get });
  const [config, setConfig] = useState<RuntimeConfig | null>(null);
  const [versions, setVersions] = useState<Record<string, number>>({});
  const [draftVersions, setDraftVersions] = useState<Record<string, number>>({});
  const [conflicts, setConflicts] = useState<Record<string, boolean>>({});
  const [agentReady, setAgentReady] = useState(true);
  const [secretConfigured, setSecretConfigured] = useState(true);
  const [busy, setBusy] = useState("");
  const [resetSection, setResetSection] = useState<Section | null>(null);
  const [retentionConfirm, setRetentionConfirm] = useState(false);
  const [savedRetention, setSavedRetention] = useState<
    RuntimeConfig["retention"] | null
  >(null);
  const [rulesText, setRulesText] = useState("");
  const [activeSection, setActiveSection] = useState<Section>("agent");
  const parsedRules = parseRules(rulesText);
  const configRef = useRef<RuntimeConfig | null>(null);
  const savedConfigRef = useRef<RuntimeConfig | null>(null);
  const applySettingsRef = useRef<
    (data: SettingsData, confirmed?: Section, reloadConflicted?: boolean) => void
  >(() => {});
  const savedRulesRef = useRef("");
  const rulesDirty = Boolean(
    savedConfigRef.current && rulesText !== savedRulesRef.current,
  );
  const dirty = Boolean(
    rulesDirty ||
      (config &&
        savedConfigRef.current &&
        JSON.stringify(config) !== JSON.stringify(savedConfigRef.current)),
  );
  const blocker = useDirtyGuard(dirty);

  const applySettings = (
    data: SettingsData,
    confirmed?: Section,
    reloadConflicted = false,
  ) => {
    const previousServer = savedConfigRef.current;
    const previousConfig = configRef.current;
    const previousRules = rulesText;
    const merged: RuntimeConfig = { ...data.config };
    const target = merged as Record<Section, unknown>;
    const nextConflicts: Record<string, boolean> = {};
    for (const section of sectionOrder) {
      if (section === confirmed) {
        target[section] = data.config[section];
        continue;
      }
      const draftVersion = draftVersions[section];
      const versionChanged =
        draftVersion !== undefined && draftVersion !== data.versions[section];
      if (versionChanged && !reloadConflicted) {
        nextConflicts[section] = true;
      }
      if (!previousServer || !previousConfig) continue;
      if (
        !versionChanged &&
        !sameSection(previousConfig[section], previousServer[section])
      ) {
        target[section] = previousConfig[section];
      }
      if (nextConflicts[section]) {
        target[section] = previousConfig[section];
      }
    }
    configRef.current = merged;
    savedConfigRef.current = data.config;
    setConfig(merged);
    setVersions(data.versions);
    setDraftVersions((previous) => {
      if (!confirmed && !reloadConflicted) return previous;
      const next = { ...previous };
      if (confirmed) delete next[confirmed];
      if (reloadConflicted) {
        for (const section of sectionOrder) {
          if (next[section] !== undefined && next[section] !== data.versions[section]) {
            delete next[section];
          }
        }
      }
      return next;
    });
    setConflicts((previous) => {
      if (reloadConflicted) return {};
      if (confirmed) {
        const next = { ...previous };
        delete next[confirmed];
        return next;
      }
      const next = { ...previous };
      for (const section of sectionOrder) {
        if (nextConflicts[section]) next[section] = true;
        else delete next[section];
      }
      return next;
    });
    setAgentReady(data.agent_ready);
    setSecretConfigured(data.secret_configured);
    setSavedRetention(data.config.retention);
    const chatVersion = draftVersions.chat;
    const chatConflict =
      chatVersion !== undefined && chatVersion !== data.versions.chat;
    const keepRules =
      confirmed !== "chat" &&
      previousServer !== null &&
      previousConfig !== null &&
      chatVersion !== undefined &&
      previousRules !== savedRulesRef.current &&
      (!reloadConflicted || !chatConflict);
    if (keepRules) {
      setRulesText(previousRules);
    } else {
      const nextRules = rulesToText(data.config.chat.time_rules);
      savedRulesRef.current = nextRules;
      setRulesText(nextRules);
    }
  };

  applySettingsRef.current = applySettings;

  useEffect(() => {
    if (query.data) applySettingsRef.current(query.data);
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

  const update = <K extends Section>(
    section: K,
    value: Partial<RuntimeConfig[K]>,
  ) => {
    setConfig((previous) => {
      if (!previous) return previous;
      const next = {
        ...previous,
        [section]: { ...previous[section], ...value },
      };
      configRef.current = next;
      return next;
    });
    setDraftVersions((previous) =>
      previous[section] === undefined
        ? { ...previous, [section]: versions[section] ?? 0 }
        : previous,
    );
  };

  const saveSection = async (section: Section, values: unknown) => {
    if (conflicts[section]) return;
    setBusy(section);
    try {
      const result = await settingsApi.save(
        section,
        values,
        draftVersions[section] ?? versions[section] ?? 0,
      );
      setDraftVersions((previous) => {
        const next = { ...previous };
        delete next[section];
        return next;
      });
      applySettings(result.data, section);
      queryClient.setQueryData(["settings"], result.data);
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

  const reloadConflicts = async () => {
    try {
      const result = await settingsApi.get();
      queryClient.setQueryData(["settings"], result);
      applySettingsRef.current(result, undefined, true);
    } catch (error) {
      toastApiError(error);
    }
  };

  const saveRetention = () => {
    if (!config || !savedRetention) return;
    const next = config.retention;
    const destructive =
      next.history_days < savedRetention.history_days ||
      next.forget_threshold > savedRetention.forget_threshold ||
      next.max_half_life_days < savedRetention.max_half_life_days;
    if (destructive) {
      setRetentionConfirm(true);
      return;
    }
    void saveSection("retention", next);
  };

  const reset = async () => {
    if (!resetSection) return;
    setBusy(resetSection);
    try {
      const result = await settingsApi.reset(
        resetSection,
        versions[resetSection] ?? 0,
      );
      setDraftVersions((previous) => {
        const next = { ...previous };
        delete next[resetSection];
        return next;
      });
      applySettings(result.data, resetSection);
      queryClient.setQueryData(["settings"], result.data);
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
  if (!config) {
    return (
      <p className="text-sm text-muted-foreground">{t("common.loadFailed")}</p>
    );
  }

  return (
    <div className="space-y-6">
      {query.isRefetchError ? <p role="alert" className="text-sm text-destructive">{t("common.refreshFailed")}</p> : null}
      <div className="flex flex-wrap items-end justify-between gap-4">
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
      {Object.values(conflicts).some(Boolean) ? (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-warning/40 bg-warning/10 px-4 py-3 text-sm text-warning">
          <div>
            <p className="font-medium">{t("common.versionChanged")}</p>
            <p className="mt-0.5 text-xs">{t("common.versionChangedHint")}</p>
          </div>
          <Button type="button" variant="outline" size="sm" onClick={() => void reloadConflicts()}>
            {t("common.reloadLatest")}
          </Button>
        </div>
      ) : null}

      <nav
        className="flex flex-wrap gap-1 rounded-xl border border-border bg-card p-1.5"
        aria-label={t("settings.sectionNav")}
      >
        {sectionOrder.map((section) => (
          <button
            key={section}
            type="button"
            aria-current={activeSection === section ? "page" : undefined}
            onClick={() => setActiveSection(section)}
            className={
              activeSection === section
                ? "relative rounded-lg px-3 py-2 text-sm font-medium text-primary"
                : "relative rounded-lg px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            }
          >
            {activeSection === section ? (
              <motion.span
                layoutId="settings-section-indicator"
                className="absolute inset-x-3 bottom-1 h-0.5 rounded-full bg-primary"
                transition={{
                  duration: reducedMotion ? 0 : 0.18,
                  ease: [0.22, 1, 0.36, 1],
                }}
              />
            ) : null}
            <span className="relative">{t(`settings.sections.${section}`)}</span>
          </button>
        ))}
      </nav>

      <div hidden={activeSection !== "agent"}>
        <SectionCard
          title={t("settings.agent.title")}
          description={t("settings.agent.hint")}
        >
          <div className="admin-form-grid">
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
            <Field label={t("settings.agent.chatMaxStep")}>
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
            <Field label={t("settings.agent.lifeMaxStep")}>
              <Input
                type="number"
                min={1}
                max={1000}
                value={config.agent.life_max_step}
                onChange={(event) =>
                  update("agent", {
                    life_max_step: numberValue(event.target.value),
                  })
                }
              />
            </Field>
            <Field label={t("settings.agent.workerMaxStep")}>
              <Input
                type="number"
                min={1}
                max={1000}
                value={config.agent.worker_max_step}
                onChange={(event) =>
                  update("agent", {
                    worker_max_step: numberValue(event.target.value),
                  })
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
            <Field label={t("settings.agent.workerCount")}>
              <Input
                type="number"
                min={0}
                max={5}
                value={config.agent.worker_count}
                onChange={(event) =>
                  update("agent", {
                    worker_count: numberValue(event.target.value),
                  })
                }
              />
            </Field>
          </div>
          <SaveButtons
            busy={busy === "agent"}
            disabled={Boolean(conflicts.agent)}
            onSave={() => saveSection("agent", config.agent)}
            onReset={() => setResetSection("agent")}
          />
        </SectionCard>
      </div>

      <div hidden={activeSection !== "chat"}>
        <SectionCard
          title={t("settings.chat.title")}
          description={t("settings.chat.hint")}
        >
          <div className="admin-form-grid">
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
              onChange={(event) => {
                setDraftVersions((previous) =>
                  previous.chat === undefined
                    ? { ...previous, chat: versions.chat ?? 0 }
                    : previous,
                );
                setRulesText(event.target.value);
              }}
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
          <div className="grid grid-cols-3 gap-4">
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
            disabled={Boolean(parsedRules.error) || Boolean(conflicts.chat)}
            onSave={() =>
              saveSection("chat", {
                ...config.chat,
                time_rules: parsedRules.rules,
              })
            }
            onReset={() => setResetSection("chat")}
          />
        </SectionCard>
      </div>

      <div hidden={activeSection !== "compactor"}>
        <SectionCard
          title={t("settings.compactor.title")}
          description={t("settings.compactor.hint")}
        >
          <div className="grid grid-cols-3 gap-4">
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
          <div className="grid grid-cols-3 gap-4">
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
            disabled={Boolean(conflicts.compactor)}
            onSave={() => saveSection("compactor", config.compactor)}
            onReset={() => setResetSection("compactor")}
          />
        </SectionCard>
      </div>

      <div hidden={activeSection !== "maintenance"}>
        <SectionCard
          title={t("settings.maintenance.title")}
          description={t("settings.maintenance.hint")}
        >
          <div className="admin-form-grid">
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
            disabled={Boolean(conflicts.maintenance)}
            onSave={() => saveSection("maintenance", config.maintenance)}
            onReset={() => setResetSection("maintenance")}
          />
        </SectionCard>
      </div>

      <div hidden={activeSection !== "retention"}>
        <SectionCard
          title={t("settings.retention.title")}
          description={t("settings.retention.hint")}
        >
          <div className="admin-form-grid">
            <Field label={t("settings.retention.initialHalfLife")}>
              <Input
                type="number"
                min={1}
                max={365}
                value={config.retention.initial_half_life_days}
                onChange={(event) =>
                  update("retention", {
                    initial_half_life_days: numberValue(event.target.value),
                  })
                }
              />
            </Field>
            <Field label={t("settings.retention.maxHalfLife")}>
              <Input
                type="number"
                min={1}
                max={3650}
                value={config.retention.max_half_life_days}
                onChange={(event) =>
                  update("retention", {
                    max_half_life_days: numberValue(event.target.value),
                  })
                }
              />
            </Field>
            <Field label={t("settings.retention.reinforceFactor")}>
              <Input
                type="number"
                step="0.05"
                min={0}
                max={1}
                value={config.retention.reinforce_factor}
                onChange={(event) =>
                  update("retention", {
                    reinforce_factor: numberValue(event.target.value),
                  })
                }
              />
            </Field>
            <Field label={t("settings.retention.forgetThreshold")}>
              <Input
                type="number"
                step="0.1"
                min={1}
                max={50}
                value={percent(config.retention.forget_threshold)}
                onChange={(event) =>
                  update("retention", {
                    forget_threshold: ratio(numberValue(event.target.value)),
                  })
                }
              />
            </Field>
            <Field
              label={t("settings.retention.historyDays")}
              hint={t("settings.retention.historyHint")}
            >
              <Input
                type="number"
                min={1}
                max={3650}
                value={config.retention.history_days}
                onChange={(event) =>
                  update("retention", {
                    history_days: numberValue(event.target.value),
                  })
                }
              />
            </Field>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="admin-label-text text-muted-foreground">
              {t("settings.retention.quickHistory")}
            </span>
            {[30, 60, 90, 180].map((days) => (
              <Button
                key={days}
                type="button"
                variant={config.retention.history_days === days ? "default" : "outline"}
                size="sm"
                onClick={() => update("retention", { history_days: days })}
              >
                {t("settings.retention.days", { days })}
              </Button>
            ))}
          </div>
          <div className="admin-card-pad-compact space-y-1 rounded-xl border border-border bg-muted/40">
            <p className="admin-body-text text-foreground/75">
              {t("settings.retention.preview", {
                days: forgettingDays(
                  config.retention.initial_half_life_days,
                  config.retention.forget_threshold,
                ),
                history: config.retention.history_days,
              })}
            </p>
            <p className="admin-caption-text text-muted-foreground/75">
              {t("settings.retention.maxPreview", {
                days: forgettingDays(
                  config.retention.max_half_life_days,
                  config.retention.forget_threshold,
                ),
              })}
            </p>
            {config.retention.history_days <
            forgettingDays(
              config.retention.initial_half_life_days,
              config.retention.forget_threshold,
            ) ? (
              <p className="admin-caption-text text-warning">
                {t("settings.retention.historyShort")}
              </p>
            ) : null}
          </div>
          <SaveButtons
            busy={busy === "retention"}
            disabled={Boolean(conflicts.retention)}
            onSave={saveRetention}
            onReset={() => setResetSection("retention")}
          />
        </SectionCard>
      </div>

      <div hidden={activeSection !== "media"}>
        <SectionCard
          title={t("settings.media.title")}
          description={t("settings.media.hint")}
        >
          <div className="grid grid-cols-3 gap-4">
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
            disabled={Boolean(conflicts.media)}
            onSave={() => saveSection("media", config.media)}
            onReset={() => setResetSection("media")}
          />
        </SectionCard>
      </div>

      <div hidden={activeSection !== "debug"}>
        <SectionCard
          title={t("settings.debug.title")}
          description={t("settings.debug.hint")}
        >
          <CheckboxRow
            checked={config.debug.log_agent_content}
            onCheckedChange={(checked) =>
              update("debug", { log_agent_content: checked })
            }
            label={t("settings.debug.logAgentContent")}
          />
          <SaveButtons
            busy={busy === "debug"}
            disabled={Boolean(conflicts.debug)}
            onSave={() => saveSection("debug", config.debug)}
            onReset={() => setResetSection("debug")}
          />
        </SectionCard>
      </div>

      <ConfirmDialog
        open={retentionConfirm}
        onOpenChange={setRetentionConfirm}
        title={t("settings.retention.confirmTitle")}
        description={t("settings.retention.confirmBody")}
        confirmLabel={t("settings.retention.confirmSave")}
        destructive
        onConfirm={() => {
          setRetentionConfirm(false);
          if (config) void saveSection("retention", config.retention);
        }}
      />

      <DirtyGuardDialog blocker={blocker} />

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
