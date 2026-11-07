import type { ReactNode } from "react";
import { Card } from "@/components/ui/card";

export function FilterPanel({
  title,
  actions,
  children,
}: {
  title?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Card>
      <div className="admin-card-pad-compact flex flex-col gap-3">
        {title || actions ? (
          <div className="flex items-center justify-between gap-6">
            {title ? (
              <h2 className="admin-card-title-compact text-foreground">
                {title}
              </h2>
            ) : (
              <span />
            )}
            {actions ? <div className="shrink-0">{actions}</div> : null}
          </div>
        ) : null}
        {children}
      </div>
    </Card>
  );
}

export function EmptyState({
  message,
  className,
}: {
  message: string;
  className?: string;
}) {
  return (
    <div
      className={
        "admin-body-text rounded-xl border border-dashed border-border bg-card/40 px-6 py-12 text-center text-muted-foreground " +
        (className ?? "")
      }
    >
      {message}
    </div>
  );
}
