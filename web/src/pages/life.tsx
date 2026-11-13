import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { lifeApi } from "@/api/client-endpoints";
import { ApiError } from "@/api/client";
import type { LifeData, LifeSchedule, LifeScheduleUpdate } from "@/api/types";
import { ConfirmDialog } from "@/components/admin/confirm-dialog";
import { EditPanel } from "@/components/admin/edit-panel";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { SectionCard } from "@/components/admin/section-card";
import { toastApiError, toastSuccess } from "@/components/admin/toast";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
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
import { formatDateTime } from "@/lib/format";
import { DirtyGuardDialog, useDirtyGuard } from "@/hooks/use-dirty-guard";
import { LifeWorkers, SessionHistoryList } from "./life-workers";

type TimingMode = "once" | "after" | "cron";

interface ScheduleForm {
  id?: number;
  text: string;
  timing: TimingMode;
  at: string;
  after: string;
  cron: string;
  enabled: boolean;
  // 编辑时记录原始时间，保存只发送真正改过的部分
  originalTiming?: TimingMode;
  originalAt?: string;
  originalCron?: string;
}

const emptyForm: ScheduleForm = {
  text: "",
  timing: "once",
  at: "",
  after: "60",
  cron: "0 23 * * *",
  enabled: true,
};

// toLocalInput 把后端时间转成 datetime-local 输入值
function toLocalInput(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

// fromLocalInput 按浏览器时区把输入转成带时区的 RFC3339，避免和服务端时区错位
function fromLocalInput(value: string): string {
  if (!value.trim()) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "" : date.toISOString();
}

function nextByPurpose(
  schedules: LifeSchedule[],
  purposes: string[],
): LifeSchedule | undefined {
  return schedules
    .filter((item) => item.enabled && purposes.includes(item.purpose))
    .sort(
      (left, right) =>
        new Date(left.next_run_at).getTime() -
        new Date(right.next_run_at).getTime(),
    )[0];
}

function StatusRow({
  label,
  value,
}: {
  label: string;
  value: ReactNode;
}) {
  return (
    <div className="flex items-start justify-between gap-4 py-2.5">
      <span className="admin-label-text shrink-0 text-muted-foreground">
        {label}
      </span>
      <span className="admin-body-text min-w-0 break-words text-right text-foreground">
        {value}
      </span>
    </div>
  );
}

export default function LifePage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [form, setForm] = useState<ScheduleForm | null>(null);
  const [wakeOpen, setWakeOpen] = useState(false);
  const [runOpen, setRunOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [formBaseline, setFormBaseline] = useState("");
  const [discardOpen, setDiscardOpen] = useState(false);
  const [formError, setFormError] = useState("");
  const dirty = form !== null && JSON.stringify(form) !== formBaseline;
  const blocker = useDirtyGuard(dirty);
  const openForm = (next: ScheduleForm) => {
    setForm(next);
    setFormBaseline(JSON.stringify(next));
    setFormError("");
  };
  const closeForm = () => {
    if (saveMutation.isPending) return;
    if (dirty) setDiscardOpen(true);
    else setForm(null);
  };

  const query = useQuery({
    queryKey: ["life"],
    queryFn: lifeApi.get,
    refetchInterval: 15_000,
  });

  const applyData = (data: LifeData) => {
    queryClient.setQueryData(["life"], data);
  };

  const runMutation = useMutation({
    mutationFn: () => lifeApi.run(),
    onSuccess: (result) => {
      applyData(result.data);
      setRunOpen(false);
      toastSuccess(t("life.runSubmitted"));
    },
    onError: toastApiError,
  });

  const wakeMutation = useMutation({
    mutationFn: () => lifeApi.wake(),
    onSuccess: (result) => {
      applyData(result.data);
      setWakeOpen(false);
      toastSuccess(t("life.wakeDone"));
    },
    onError: toastApiError,
  });

  const saveMutation = useMutation({
    mutationFn: (input: ScheduleForm) => {
      const body: LifeScheduleUpdate = {
        text: input.text.trim(),
        enabled: input.enabled,
      };
      const timingChanged =
        !input.id ||
        input.timing !== input.originalTiming ||
        (input.timing === "once" && input.at !== input.originalAt) ||
        (input.timing === "cron" && input.cron !== input.originalCron);
      if (timingChanged) {
        if (input.timing === "cron") {
          body.cron = input.cron.trim();
        } else if (input.timing === "after") {
          body.after_minutes = Number(input.after);
        } else {
          body.at = fromLocalInput(input.at);
        }
      }
      if (input.id) {
        return lifeApi.updateSchedule(input.id, body);
      }
      return lifeApi.createSchedule({ ...body, text: input.text.trim() });
    },
    onSuccess: (result) => {
      applyData(result.data);
      setForm(null);
      setFormError("");
      toastSuccess(t("life.saved"));
    },
    onError: (error) => {
      setFormError(error instanceof ApiError ? error.code : "unknown");
      toastApiError(error);
    },
  });

  const toggleMutation = useMutation({
    mutationFn: (input: { id: number; enabled: boolean }) =>
      lifeApi.updateSchedule(input.id, { enabled: input.enabled }),
    onSuccess: (result) => {
      applyData(result.data);
      toastSuccess(t("life.saved"));
    },
    onError: toastApiError,
  });

  if (query.isPending) return <PageSkeleton />;
  if (!query.data) {
    return (
      <p className="text-sm text-muted-foreground">{t("life.loadFailed")}</p>
    );
  }

  const data = query.data;
  const state = data.state;
  const fixedSchedule = nextByPurpose(data.schedules, ["next", "wake"]);
  const reminderSchedule = nextByPurpose(data.schedules, ["commitment"]);
  const wakeSchedule = data.schedules.find(
    (item) => item.enabled && item.purpose === "wake",
  );
  const errorLabel = state.last_error_code
    ? t(`life.errorCodes.${state.last_error_code}`, { defaultValue: "" }) ||
      t("life.errorCodes.unknown")
    : "";

  const openEdit = (schedule: LifeSchedule) => {
    const timing: TimingMode = schedule.cron ? "cron" : "once";
    const at = toLocalInput(schedule.at);
    openForm({
      id: schedule.id,
      text: schedule.text,
      timing,
      at,
      after: "60",
      cron: schedule.cron || "0 23 * * *",
      enabled: schedule.enabled,
      originalTiming: timing,
      originalAt: at,
      originalCron: schedule.cron || "",
    });
  };

  const timingMode = (value: string) => value as TimingMode;
  const canSave =
    form !== null &&
    form.text.trim().length > 0 &&
    (form.timing === "cron"
      ? form.cron.trim().length > 0
      : form.timing === "after"
        ? Number(form.after) > 0
        : form.at.trim().length > 0);

  return (
    <div className="space-y-6">
      {query.isRefetchError ? <p role="alert" className="text-sm text-destructive">{t("common.refreshFailed")}</p> : null}
      <PageHeader
        title={t("life.title")}
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              disabled={wakeMutation.isPending || state.awake}
              onClick={() => setWakeOpen(true)}
            >
              {wakeMutation.isPending ? <Spinner /> : null}
              {t("life.wakeUp")}
            </Button>
            <Button
              disabled={runMutation.isPending}
              onClick={() => setRunOpen(true)}
            >
              {runMutation.isPending ? <Spinner /> : null}
              {t("life.runNow")}
            </Button>
          </div>
        }
      />

      <SectionCard
        title={t("life.stateTitle")}
        actions={
          <div className="flex items-center gap-2">
            <Badge variant={state.running ? "accent" : "ghost"}>
              {state.running ? t("life.running") : t("life.idle")}
            </Badge>
            <Badge variant={state.awake ? "success" : "ghost"}>
              {state.awake ? t("life.awake") : t("life.asleep")}
            </Badge>
            <Button
              variant="outline"
              size="xs"
              onClick={() => setHistoryOpen(true)}
            >
              {t("life.workers.view")}
            </Button>
          </div>
        }
      >
        <div className="grid gap-x-10 gap-y-1 md:grid-cols-2">
          <div className="divide-y divide-border">
            <StatusRow
              label={t("life.activity")}
              value={state.activity || t("life.activityEmpty")}
            />
            <StatusRow
              label={t("life.activitySince")}
              value={formatDateTime(state.activity_since)}
            />
            <StatusRow
              label={t("life.lastSleep")}
              value={formatDateTime(state.sleep_started_at)}
            />
            {!state.awake ? (
              <StatusRow
                label={t("life.wakeSchedule")}
                value={
                  wakeSchedule
                    ? formatDateTime(wakeSchedule.next_run_at)
                    : t("life.noWakeSchedule")
                }
              />
            ) : null}
          </div>
          <div className="divide-y divide-border">
            <StatusRow
              label={t("life.wokeAt")}
              value={formatDateTime(state.woke_at)}
            />
            <StatusRow
              label={t("life.nextRun")}
              value={
                fixedSchedule ? (
                  <span>
                    {formatDateTime(fixedSchedule.next_run_at)}
                    <span className="text-muted-foreground">
                      {" · "}
                      {fixedSchedule.text}
                    </span>
                  </span>
                ) : (
                  t("life.nextNone")
                )
              }
            />
            <StatusRow
              label={t("life.nextReminder")}
              value={
                reminderSchedule ? (
                  <span>
                    {formatDateTime(reminderSchedule.next_run_at)}
                    <span className="text-muted-foreground">
                      {" · "}
                      {reminderSchedule.text}
                    </span>
                  </span>
                ) : (
                  t("life.noReminder")
                )
              }
            />
            <StatusRow
              label={t("life.lastError")}
              value={
                errorLabel || state.last_error ? (
                  <span className="text-destructive">
                    {errorLabel || state.last_error}
                    {errorLabel && state.last_error ? (
                      <span className="mt-1 block text-xs text-muted-foreground/75">
                        {state.last_error}
                      </span>
                    ) : null}
                  </span>
                ) : (
                  <span className="text-muted-foreground/60">
                    {t("life.noError")}
                  </span>
                )
              }
            />
            {state.retry_at || state.retry_exhausted ? <StatusRow label={t("life.retry")} value={state.retry_at ? t("life.retryAt", { count: state.retry_count, time: formatDateTime(state.retry_at) }) : t("life.retryExhausted")} /> : null}
          </div>
        </div>
      </SectionCard>

      <SectionCard
        title={t("life.schedulesTitle")}
        actions={
          <Button size="sm" onClick={() => openForm({ ...emptyForm })}>
            {t("life.newSchedule")}
          </Button>
        }
      >
        {data.schedules.length === 0 ? (
          <p className="admin-body-text text-muted-foreground/60">{t("life.empty")}</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("life.columns.purpose")}</TableHead>
                <TableHead>{t("life.columns.time")}</TableHead>
                <TableHead>{t("life.columns.text")}</TableHead>
                <TableHead>{t("life.columns.state")}</TableHead>
                <TableHead className="text-right">
                  {t("life.columns.actions")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.schedules.map((schedule) => (
                <TableRow key={schedule.id}>
                  <TableCell className="align-top">
                    <Badge
                      variant={
                        schedule.purpose === "wake"
                          ? "warning"
                          : schedule.purpose === "next"
                            ? "secondary"
                            : "outline"
                      }
                    >
                      {t(`life.purpose.${schedule.purpose}`)}
                    </Badge>
                  </TableCell>
                  <TableCell className="align-top">
                    {schedule.cron ? (
                      <div className="space-y-1">
                        <Badge variant="outline">{t("life.timeRepeat")}</Badge>
                        <p className="admin-number text-xs text-muted-foreground">
                          {schedule.cron}
                        </p>
                        <p className="text-xs text-muted-foreground/60">
                          {t("life.nextFormat", {
                            time: formatDateTime(schedule.next_run_at),
                          })}
                        </p>
                      </div>
                    ) : (
                      <div className="space-y-1">
                        <Badge variant="outline">{t("life.timeOnce")}</Badge>
                        <p className="admin-number text-xs text-muted-foreground">
                          {formatDateTime(schedule.at)}
                        </p>
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="max-w-md align-top text-sm text-foreground">
                    {schedule.text}
                  </TableCell>
                  <TableCell className="align-top">
                    <Badge variant={schedule.enabled ? "success" : "ghost"}>
                      {schedule.enabled
                        ? t("life.statusActive")
                        : t("life.statusDisabled")}
                    </Badge>
                  </TableCell>
                  <TableCell className="align-top">
                    <div className="flex justify-end gap-2">
                      <Button
                        variant="ghost"
                        size="xs"
                        onClick={() => openEdit(schedule)}
                      >
                        {t("life.edit")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="xs"
                        disabled={toggleMutation.isPending}
                        onClick={() =>
                          toggleMutation.mutate({
                            id: schedule.id,
                            enabled: !schedule.enabled,
                          })
                        }
                      >
                        {schedule.enabled ? t("life.disable") : t("life.enable")}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </SectionCard>

      <SectionCard title={t("life.usage.title")} description={t("life.usage.hint")}>
        {data.daily_usage_unavailable || !data.daily_usage ? (
          <p role="alert" className="text-sm text-destructive">{t("life.usage.unavailable")}</p>
        ) : (
          <div className="admin-form-grid">
            <StatusRow label={t("life.usage.date")} value={data.daily_usage.date} />
            <StatusRow label={t("life.usage.status")} value={<Badge variant={data.daily_usage.status === "over" ? "destructive" : data.daily_usage.status === "near" ? "accent" : "ghost"}>{t(`life.usage.${data.daily_usage.status}`)}</Badge>} />
            <StatusRow label={t("life.usage.total")} value={data.daily_usage.total.toLocaleString()} />
            <StatusRow label={t("life.usage.limit")} value={data.daily_usage.limit ? data.daily_usage.limit.toLocaleString() : t("life.usage.noLimit")} />
            <StatusRow label={t("life.usage.life")} value={data.daily_usage.life.toLocaleString()} />
            <StatusRow label={t("life.usage.worker")} value={data.daily_usage.worker.toLocaleString()} />
          </div>
        )}
      </SectionCard>

      <LifeWorkers workers={data.workers} />

      <EditPanel
        open={historyOpen}
        onOpenChange={setHistoryOpen}
        title={t("life.stateTitle")}
        description="life:1"
      >
        {historyOpen ? <SessionHistoryList workerID={0} /> : null}
      </EditPanel>

      <Dialog open={form !== null} onOpenChange={(open) => !open && closeForm()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {form?.id ? t("life.editTitle") : t("life.createTitle")}
            </DialogTitle>
            <DialogDescription>{t("life.schedulesSubtitle")}</DialogDescription>
          </DialogHeader>
          {form ? (
            <div className="mt-4 space-y-4">
              <Field label={t("life.text")} error={formError === "life_schedule_text_invalid" ? t(`errors.${formError}`) : undefined}>
                <Textarea
                  rows={3}
                  value={form.text}
                  placeholder={t("life.textPlaceholder")}
                  onChange={(event) =>
                    setForm({ ...form, text: event.target.value })
                  }
                />
              </Field>
              <Field label={t("life.timing")} error={formError === "life_schedule_time_invalid" ? t(`errors.${formError}`) : undefined}>
                <NativeSelect
                  value={form.timing}
                  onChange={(event) =>
                    setForm({ ...form, timing: timingMode(event.target.value) })
                  }
                >
                  <option value="once">{t("life.once")}</option>
                  <option value="after">{t("life.after")}</option>
                  <option value="cron">{t("life.cron")}</option>
                </NativeSelect>
              </Field>
              {form.timing === "once" ? (
                <Field label={t("life.atLabel")} hint={t("life.atPlaceholder")}>
                  <Input
                    type="datetime-local"
                    value={form.at}
                    min={toLocalInput(new Date().toISOString())}
                    onChange={(event) =>
                      setForm({ ...form, at: event.target.value })
                    }
                  />
                </Field>
              ) : null}
              {form.timing === "after" ? (
                <Field label={t("life.afterLabel")}>
                  <Input
                    type="number"
                    min={1}
                    value={form.after}
                    onChange={(event) =>
                      setForm({ ...form, after: event.target.value })
                    }
                  />
                </Field>
              ) : null}
              {form.timing === "cron" ? (
                <Field label={t("life.cronLabel")} hint={t("life.cronHint")}>
                  <Input
                    value={form.cron}
                    onChange={(event) =>
                      setForm({ ...form, cron: event.target.value })
                    }
                  />
                </Field>
              ) : null}
              <SwitchRow
                label={t("life.enabledLabel")}
                checked={form.enabled}
                onCheckedChange={(checked) =>
                  setForm({ ...form, enabled: checked })
                }
              />
            </div>
          ) : null}
          <DialogFooter>
            <Button
              variant="ghost"
              disabled={saveMutation.isPending}
              onClick={closeForm}
            >
              {t("common.cancel")}
            </Button>
            <Button
              disabled={!canSave || saveMutation.isPending}
              onClick={() => form && saveMutation.mutate(form)}
            >
              {saveMutation.isPending ? <Spinner /> : null}
              {saveMutation.isPending ? t("life.saving") : t("life.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <DirtyGuardDialog blocker={blocker} />
      <ConfirmDialog open={discardOpen} onOpenChange={setDiscardOpen} title={t("common.unsavedTitle")} description={t("common.unsavedBody")} confirmLabel={t("common.leave")} destructive={false} onConfirm={() => { setDiscardOpen(false); setForm(null); }} />

      <ConfirmDialog
        open={runOpen}
        onOpenChange={setRunOpen}
        title={t("life.runNow")}
        description={t("life.runBody")}
        confirmLabel={t("life.runNow")}
        loading={runMutation.isPending}
        destructive={false}
        onConfirm={() => runMutation.mutate()}
      />

      <ConfirmDialog
        open={wakeOpen}
        onOpenChange={setWakeOpen}
        title={t("life.wakeConfirmTitle")}
        description={t("life.wakeConfirmBody")}
        confirmLabel={t("life.wakeUp")}
        loading={wakeMutation.isPending}
        destructive={false}
        onConfirm={() => wakeMutation.mutate()}
      />
    </div>
  );
}
