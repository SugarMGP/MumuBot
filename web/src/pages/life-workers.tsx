import { useInfiniteQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { lifeApi } from "@/api/client-endpoints";
import type { LifeWorker } from "@/api/types";
import { EditPanel } from "@/components/admin/edit-panel";
import { SectionCard } from "@/components/admin/section-card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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

function EntryBody({ workerID, entryID }: { workerID: number; entryID: number }) {
  const { t } = useTranslation();
  const query = useInfiniteQuery({
    queryKey: ["worker-entry", workerID, entryID],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => lifeApi.workerEntry(workerID, entryID, pageParam),
    getNextPageParam: (last) => last.complete ? undefined : last.next_offset,
  });
  if (query.isPending) return <Spinner />;
  if (query.isError) return <p role="alert" className="text-sm text-destructive">{t("common.loadFailed")}</p>;
  const first = query.data.pages[0];
  const calls = first.tool_calls ?? [];
  return (
    <div className="mt-3 space-y-3">
      {first.arguments ? <pre className="whitespace-pre-wrap break-all text-xs text-muted-foreground">{first.arguments}</pre> : null}
      {calls.map((call, index) => <pre key={index} className="whitespace-pre-wrap break-all text-xs text-muted-foreground">{call.name}{"\n"}{call.arguments}</pre>)}
      <pre className="whitespace-pre-wrap break-words font-sans text-sm leading-relaxed">{query.data.pages.map((page) => page.text).join("") || t("life.workers.noText")}</pre>
      {query.hasNextPage ? <Button size="xs" variant="outline" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>{query.isFetchingNextPage ? <Spinner /> : null}{t("life.workers.readMore")}</Button> : null}
      {query.isFetchNextPageError ? <p role="alert" className="text-xs text-destructive">{t("common.loadFailed")}</p> : null}
    </div>
  );
}

function WorkerHistory({ worker }: { worker: LifeWorker }) {
  const { t } = useTranslation();
  const [selected, setSelected] = useState<number | null>(null);
  const query = useInfiniteQuery({
    queryKey: ["worker-history", worker.id],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => lifeApi.workerHistory(worker.id, pageParam),
    getNextPageParam: (last) => last.has_more ? last.next_before_id : undefined,
    refetchInterval: 15_000,
  });
  if (query.isPending) return <Spinner />;
  if (query.isError) return <p role="alert" className="text-sm text-destructive">{t("common.loadFailed")}</p>;
  const pages = query.data.pages;
  const latest = pages[0];
  const entries = [...pages.flatMap((page) => page.entries), ...latest.running_tools, ...latest.pending_entries];
  const pendingIDs = new Set(latest.pending_entries.map((entry) => entry.id));
  const unique = [...new Map(entries.map((entry) => [entry.id, entry])).values()].sort((a, b) => a.id - b.id);
  return (
    <div className="space-y-4">
      {query.hasNextPage ? <Button size="sm" variant="outline" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>{query.isFetchingNextPage ? <Spinner /> : null}{t("life.workers.older")}</Button> : null}
      {query.isFetchNextPageError ? <p role="alert" className="text-xs text-destructive">{t("common.loadFailed")}</p> : null}
      {unique.length === 0 ? <p className="text-sm text-muted-foreground">{t("life.workers.emptyHistory")}</p> : null}
      <div className="divide-y divide-border">
        {unique.map((entry) => (
          <div key={entry.id} className="py-4">
            <button type="button" aria-expanded={selected === entry.id} className="w-full rounded-lg text-left outline-none transition-colors hover:bg-muted/40 focus-visible:ring-2 focus-visible:ring-ring" onClick={() => setSelected(selected === entry.id ? null : entry.id)}>
              <div className="flex items-center justify-between gap-3 text-xs text-muted-foreground">
                <span className="flex min-w-0 flex-wrap items-center gap-2">
                  <span>{t(`life.workers.roles.${entry.role}`, { defaultValue: t("life.workers.record") })}{entry.tool_name ? ` · ${entry.tool_name}` : ""}</span>
                  {pendingIDs.has(entry.id) ? <Badge variant="outline">{t("life.workers.queued")}</Badge> : entry.tool_status ? <Badge variant={entry.tool_status === "failed" ? "destructive" : entry.tool_status === "succeeded" ? "success" : "ghost"}>{t(`life.workers.toolStatus.${entry.tool_status}`, { defaultValue: t("life.workers.status.unknown") })}</Badge> : null}
                </span>
                <time className="admin-number shrink-0">{formatDateTime(entry.at)}</time>
              </div>
              {selected !== entry.id ? <p className="mt-2 line-clamp-3 whitespace-pre-wrap break-words text-sm">{entry.text || entry.tool_calls?.map((call) => call.name).join(", ") || t("life.workers.noText")}</p> : null}
            </button>
            {selected === entry.id ? <EntryBody workerID={worker.id} entryID={entry.id} /> : null}
          </div>
        ))}
      </div>
    </div>
  );
}

export function LifeWorkers({ workers }: { workers: LifeWorker[] }) {
  const { t } = useTranslation();
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const selected = workers.find((worker) => worker.id === selectedID);
  return (
    <>
      <SectionCard title={t("life.workers.title")}>
        {workers.length === 0 ? <p className="text-sm text-muted-foreground">{t("life.workers.empty")}</p> : (
          <Table>
            <TableHeader><TableRow>
              <TableHead>{t("life.workers.session")}</TableHead><TableHead>{t("life.workers.task")}</TableHead><TableHead>{t("common.status")}</TableHead><TableHead>{t("common.updatedAt")}</TableHead><TableHead className="text-right">{t("common.actions")}</TableHead>
            </TableRow></TableHeader>
            <TableBody>{workers.map((worker) => (
              <TableRow key={worker.id}>
                <TableCell className="admin-number text-xs">{`worker:${worker.id}`}</TableCell>
                <TableCell className="max-w-lg"><p>{worker.title}</p>{worker.last_error ? <p className="mt-1 whitespace-pre-wrap break-words text-xs text-destructive">{worker.last_error}</p> : null}</TableCell>
                <TableCell><Badge variant={!worker.active ? "ghost" : worker.status === "failed" ? "destructive" : worker.status === "running" ? "success" : "outline"}>{t(`life.workers.status.${worker.active ? worker.status : "inactive"}`, { defaultValue: t("life.workers.status.unknown") })}</Badge></TableCell>
                <TableCell className="admin-number text-xs">{formatDateTime(worker.updated_at)}</TableCell>
                <TableCell className="text-right"><Button size="xs" variant="ghost" onClick={() => setSelectedID(worker.id)}>{t("life.workers.view")}</Button></TableCell>
              </TableRow>
            ))}</TableBody>
          </Table>
        )}
      </SectionCard>
      <EditPanel open={selected !== undefined} onOpenChange={(open) => !open && setSelectedID(null)} title={selected?.title ?? t("life.workers.title")} description={selected ? `worker:${selected.id}` : undefined}>
        {selected ? <WorkerHistory key={selected.id} worker={selected} /> : null}
      </EditPanel>
    </>
  );
}
