export function formatDateTime(value?: string | null): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function formatNumber(value: number, locale: string): string {
  return new Intl.NumberFormat(locale).format(Number.isFinite(value) ? value : 0);
}

export function clampPercent(value: number): number {
  const safe = Number.isFinite(value) ? value : 0;
  return Math.min(100, Math.max(0, safe * 100));
}

export function truncateMiddle(value: string, head: number, tail = 0): string {
  if (!value) return "";
  const runes = Array.from(value);
  if (runes.length <= head + tail) return value;
  if (tail <= 0) return runes.slice(0, head).join("") + "…";
  return runes.slice(0, head).join("") + "…" + runes.slice(-tail).join("");
}
