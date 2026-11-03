import { useEffect, useRef, useState } from "react";

export interface LogStreamState {
  lines: Array<{
    seq: number;
    time: string;
    level: string;
    message: string;
    fields: Array<{ name: string; value: string }>;
    text: string;
  }>;
  total: number;
  filtered: number;
  status: "connecting" | "live" | "reconnecting";
}

const emptyState: LogStreamState = {
  lines: [],
  total: 0,
  filtered: 0,
  status: "connecting",
};

export function useLogStream(keyword: string, level: string) {
  const [state, setState] = useState<LogStreamState>(emptyState);
  const retryRef = useRef(1000);
  const timerRef = useRef<number | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    let disposed = false;
    let controller: AbortController | null = null;

    const scheduleRetry = () => {
      if (disposed) return;
      setState((previous) => ({ ...previous, status: "reconnecting" }));
      const delay = Math.min(retryRef.current, 10_000);
      retryRef.current = Math.min(retryRef.current * 2, 10_000);
      timerRef.current = window.setTimeout(connect, delay);
    };

    const connect = async () => {
      if (disposed) return;
      controller = new AbortController();
      abortRef.current = controller;
      setState((previous) => ({
        ...previous,
        status: previous.lines.length === 0 ? "connecting" : "reconnecting",
      }));
      const params = new URLSearchParams();
      if (keyword) params.set("keyword", keyword);
      if (level) params.set("level", level);
      try {
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
        retryRef.current = 1000;
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
      } catch {
        if (disposed) return;
        scheduleRetry();
      }
    };

    const handleFrame = (frame: string) => {
      const dataLines: string[] = [];
      let event = "message";
      for (const line of frame.split("\n")) {
        if (line.startsWith(":")) continue;
        if (line.startsWith("event:")) event = line.slice(6).trim();
        if (line.startsWith("data:")) dataLines.push(line.slice(5).trim());
      }
      if (event !== "snapshot" || dataLines.length === 0) return;
      try {
        const payload = JSON.parse(dataLines.join("\n")) as {
          lines: LogStreamState["lines"];
          total: number;
          filtered: number;
        };
        setState({ ...payload, status: "live" });
      } catch {
        // 忽略单帧解析失败，等待下一帧
      }
    };

    void connect();

    return () => {
      disposed = true;
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
      abortRef.current?.abort();
    };
  }, [keyword, level]);

  return state;
}
