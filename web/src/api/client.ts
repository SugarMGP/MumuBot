import type { ApiErrorPayload, WarningPayload } from "./types";

export class ApiError extends Error {
  status: number;
  code: string;
  details?: Record<string, unknown>;

  constructor(
    status: number,
    code: string,
    message: string,
    details?: Record<string, unknown>,
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

export interface ApiResult<T> {
  data: T;
  warning?: WarningPayload;
}

const unauthorizedEvent = "mumu:unauthorized";

function dispatchUnauthorized() {
  window.dispatchEvent(new Event(unauthorizedEvent));
}

export function onUnauthorized(handler: () => void): () => void {
  window.addEventListener(unauthorizedEvent, handler);
  return () => window.removeEventListener(unauthorizedEvent, handler);
}

export async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  headers?: Record<string, string>,
): Promise<ApiResult<T>> {
  let response: Response;
  try {
    response = await fetch(path, {
      method,
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
        ...headers,
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, "network_error", "network_error");
  }

  if (response.status === 204) {
    return { data: undefined as T };
  }

  const contentType = response.headers.get("content-type") || "";
  const payload = contentType.includes("application/json")
    ? ((await response.json().catch(() => null)) as
        | (ApiResult<T> & ApiErrorPayload)
        | null)
    : null;

  if (!response.ok) {
    const error = payload?.error;
    if (response.status === 401) {
      dispatchUnauthorized();
    }
    throw new ApiError(
      response.status,
      error?.code || "internal",
      error?.message || response.statusText || "request_failed",
      error?.details,
    );
  }

  if (!payload || payload.data === undefined) {
    throw new ApiError(
      response.status,
      "invalid_response",
      "invalid_response",
    );
  }
  return { data: payload.data, warning: payload.warning };
}

export async function apiGet<T>(path: string): Promise<T> {
  return (await request<T>("GET", path)).data;
}

export async function apiMutate<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<ApiResult<T>> {
  return request<T>(method, path, body);
}

export function buildQuery(values: object): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) {
    if (value === undefined || value === null || value === "") continue;
    if (typeof value !== "string" && typeof value !== "number") continue;
    params.set(key, String(value));
  }
  const query = params.toString();
  return query ? `?${query}` : "";
}
