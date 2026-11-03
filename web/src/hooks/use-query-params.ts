import { useCallback } from "react";
import { useSearchParams } from "react-router";

export type QueryParams = URLSearchParams;

export function useQueryParams() {
  const [params, setParams] = useSearchParams();

  const update = useCallback(
    (
      values: Record<string, string | number | undefined | null>,
      options: { resetPage?: boolean; replace?: boolean } = {},
    ) => {
    const next = new URLSearchParams(params);
    for (const [key, value] of Object.entries(values)) {
      if (value === undefined || value === null || value === "") {
        next.delete(key);
      } else {
        next.set(key, String(value));
      }
    }
    if (options.resetPage) next.delete("page");
    setParams(next, { replace: options.replace });
    },
    [params, setParams],
  );

  const page = Number(params.get("page") || "1") || 1;

  return { params, update, page };
}
