import { toast } from "sonner";
import i18n from "@/app/i18n";
import { ApiError } from "@/api/client";

function translateError(error: ApiError): string {
  const key = `errors.${error.code}`;
  return i18n.exists(key) ? i18n.t(key) : i18n.t("errors.unknown");
}

export function toastSuccess(message: string) {
  toast.success(message);
}


export function toastApiError(error: unknown) {
  if (error instanceof ApiError) {
    if (error.code === "model_context_too_small") {
      const sessions = error.details?.sessions;
      toast.error(i18n.t("errors.model_context_too_small"), {
        description: Array.isArray(sessions) ? sessions.join(", ") : undefined,
      });
      return;
    }
    if (error.code === "model_test_failed") {
      const reason = error.details?.reason;
      toast.error(i18n.t("errors.model_test_failed"), {
        description: typeof reason === "string" ? reason : undefined,
      });
      return;
    }
    toast.error(translateError(error));
    return;
  }
  toast.error(i18n.t("errors.unknown"));
}

export function toastWarningCode(code: string) {
  const key = `errors.warning.${code}`;
  toast.warning(
    i18n.exists(key) ? i18n.t(key) : i18n.t("errors.warning.runtime_reload_failed"),
  );
}
