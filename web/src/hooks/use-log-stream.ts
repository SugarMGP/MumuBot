import { useEffect, useRef, useState } from "react";
import { ApiError } from "@/api/client";
import { systemApi } from "@/api/client-endpoints";
import type { LogLine, LogSnapshot } from "@/api/types";

export interface LogStreamState extends LogSnapshot {
  status: "connecting" | "live" | "reconnecting";
}

const emptyState: LogStreamState = {
  lines: [],
  total: 0,
  filtered: 0,
  first_seq: 1,
  last_seq: 0,
  status: "connecting",
};

function mergeAppendedLines(previous: LogLine[], payload: LogSnapshot) {
  const lines = new Map<number, LogLine>();
  for (const line of previous) {
    if (line.seq >= payload.first_seq) lines.set(line.seq, line);
  }
  for (const line of payload.lines) lines.set(line.seq, line);
  return Array.from(lines.values()).sort((left, right) => left.seq - right.seq);
}

export function useLogStream(keyword: string, level: string) {
  const [state, setState] = useState<LogStreamState>(emptyState);
  const retryRef = useRef(1000);
  const timerRef = useRef<number | null>(null);

  useEffect(() => {
    retryRef.current = 1000;
    let disposed = false;
    let loaded = false;
    let lastSeq = 0;
    let controller: AbortController | null = null;

    setState({ ...emptyState, status: "connecting" });

    const scheduleRetry = () => {
      if (disposed) return;
      setState((previous) => ({ ...previous, status: "reconnecting" }));
      const delay = Math.min(retryRef.current, 10_000);
      retryRef.current = Math.min(retryRef.current * 2, 10_000);
      timerRef.current = window.setTimeout(connect, delay);
    };

    const handleFrame = (frame: string) => {
      const dataLines: string[] = [];
      let event = "message";
      for (const line of frame.split("\n")) {
        if (line.startsWith(":")) continue;
        if (line.startsWith("event:")) event = line.slice(6).trim();
        if (line.startsWith("data:")) dataLines.push(line.slice(5).trim());
      }
      if (dataLines.length === 0) return;
      try {
        const payload = JSON.parse(dataLines.join("\n")) as LogSnapshot;
        retryRef.current = 1000;
        if (event === "snapshot") {
          lastSeq = payload.last_seq;
          setState({ ...payload, status: "live" });
        } else if (event === "append") {
          lastSeq = Math.max(lastSeq, payload.last_seq);
          setState((previous) => ({
            ...payload,
            lines: mergeAppendedLines(previous.lines, payload),
            status: "live",
          }));
        }
      } catch {
        // 忽略单帧解析失败，等待下一帧
      }
    };

    const connectStream = async () => {
      controller = new AbortController();
      const params = new URLSearchParams();
      if (keyword) params.set("keyword", keyword);
      if (level) params.set("level", level);
      params.set("after_seq", String(lastSeq));
      const response = await fetch(
        `/api/v1/system/logs/stream?${params.toString()}`,
        {
          credentials: "same-origin",
          headers: { Accept: "text/event-stream" },
          signal: controller.signal,
        },
      );
      if (response.status === 401) {
        window.dispatchEvent(new Event("mumu:unauthorized"));
        return;
      }
      if (!response.ok || !response.body) {
        throw new Error("stream failed");
      }
      setState((previous) => ({ ...previous, status: "live" }));
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      while (!disposed) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        let boundary = buffer.indexOf("\n\n");
        while (boundary >= 0) {
          const frame = buffer.slice(0, boundary);
          buffer = buffer.slice(boundary + 2);
          handleFrame(frame);
          boundary = buffer.indexOf("\n\n");
        }
      }
      if (!disposed) scheduleRetry();
    };

    const connect = async () => {
      if (disposed) return;
      try {
        if (!loaded) {
          const snapshot = await systemApi.logs(keyword, level);
          if (disposed) return;
          loaded = true;
          lastSeq = snapshot.last_seq;
          retryRef.current = 1000;
          setState({ ...snapshot, status: "live" });
        }
        await connectStream();
      } catch (error) {
        if (disposed) return;
        if (error instanceof ApiError && error.status === 401) return;
        scheduleRetry();
      }
    };

    void connect();

    return () => {
      disposed = true;
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
      controller?.abort();
    };
  }, [keyword, level]);

  return state;
}
