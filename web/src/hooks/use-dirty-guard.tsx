import { useEffect } from "react";
import { useBlocker } from "react-router";
import { useTranslation } from "react-i18next";
import { ConfirmDialog } from "@/components/admin/confirm-dialog";

export type DirtyBlocker = ReturnType<typeof useBlocker>;

// 所有 dirty 表单共用离开确认，避免各页重复维护两套导航/刷新拦截
export function useDirtyGuard(dirty: boolean): DirtyBlocker {
  const blocker = useBlocker(dirty);

  useEffect(() => {
    if (!dirty) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  return blocker;
}

export function DirtyGuardDialog({ blocker }: { blocker: DirtyBlocker }) {
  const { t } = useTranslation();
  return (
    <ConfirmDialog
      open={blocker.state === "blocked"}
      onOpenChange={(open) => !open && blocker.reset?.()}
      title={t("common.unsavedTitle")}
      description={t("common.unsavedBody")}
      confirmLabel={t("common.leave")}
      destructive={false}
      onConfirm={() => blocker.proceed?.()}
    />
  );
}
