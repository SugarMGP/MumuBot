import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Navigate, useNavigate, useSearchParams } from "react-router";
import { sessionApi } from "@/api/client-endpoints";
import { toastApiError } from "@/components/admin/toast";
import { PreferenceSwitchers } from "@/components/admin/switchers";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/form-controls";
import { Spinner } from "@/components/ui/spinner";
import { useSession } from "@/hooks/use-session";
import { useState } from "react";
import { ApiError } from "@/api/client";

function safeNext(value: string | null): string {
  if (!value || !value.startsWith("/") || value.startsWith("//")) {
    return "/admin";
  }
  return value;
}

export default function LoginPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const queryClient = useQueryClient();
  const session = useSession();
  const [key, setKey] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const next = safeNext(searchParams.get("next"));

  if (session.isPending) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <Spinner className="size-6 text-muted-foreground" />
      </div>
    );
  }

  if (session.data?.enabled === false) {
    return (
      <div className="flex min-h-screen items-center justify-center px-6">
        <div className="absolute right-6 top-6">
          <PreferenceSwitchers />
        </div>
        <div className="admin-login-card w-full max-w-md rounded-2xl p-8 text-center">
          <h1 className="text-xl font-semibold text-foreground">
            {t("login.disabledTitle")}
          </h1>
          <p className="mt-3 text-sm leading-6 text-muted-foreground">
            {t("login.disabledBody")}
          </p>
        </div>
      </div>
    );
  }

  if (session.data?.authenticated) {
    return <Navigate to={next} replace />;
  }

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!key.trim() || submitting) return;
    setSubmitting(true);
    setError("");
    try {
      await sessionApi.login(key.trim());
      await queryClient.invalidateQueries({ queryKey: ["session"] });
      navigate(next, { replace: true });
    } catch (submitError) {
      if (submitError instanceof ApiError && submitError.code === "invalid_key") {
        setError(t("login.invalidKey"));
      } else {
        toastApiError(submitError);
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center px-6">
      <div className="absolute right-6 top-6">
        <PreferenceSwitchers />
      </div>
      <div className="admin-login-card w-full max-w-md rounded-2xl p-8">
        <div className="flex items-center gap-3">
          <span className="admin-icon-tile size-11 bg-primary/12 text-primary">
            <img src="/favicon.svg" alt="" className="size-9" />
          </span>
          <div>
            <h1 className="text-xl font-semibold text-foreground">
              {t("login.title")}
            </h1>
            <p className="mt-1 text-sm text-muted-foreground">
              {t("login.subtitle")}
            </p>
          </div>
        </div>
        <form className="mt-7 space-y-4" onSubmit={submit}>
          <div className="space-y-1.5">
            <Label htmlFor="admin-key">{t("login.adminKey")}</Label>
            <Input
              id="admin-key"
              type="password"
              autoComplete="current-password"
              autoFocus
              placeholder={t("login.placeholder")}
              value={key}
              onChange={(event) => setKey(event.target.value)}
            />
            {error ? (
              <p className="text-xs text-destructive">{error}</p>
            ) : null}
          </div>
          <Button
            type="submit"
            className="w-full"
            disabled={submitting || key.trim().length === 0}
          >
            {submitting ? <Spinner /> : null}
            {submitting ? t("login.submitting") : t("login.submit")}
          </Button>
        </form>
      </div>
    </div>
  );
}
