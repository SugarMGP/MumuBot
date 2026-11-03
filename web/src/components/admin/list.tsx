import type { ReactNode } from "react";
import { Card } from "@/components/ui/card";

export function FilterPanel({
  title,
  actions,
  children,
}: {
  title: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Card>
      <div className="flex flex-col gap-3 p-4">
        <div className="flex items-center justify-between gap-6">
          <h2 className="text-lg font-semibold text-foreground">{title}</h2>
          {actions ? <div className="shrink-0">{actions}</div> : null}
        </div>
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
        "rounded-xl border border-dashed border-border bg-card/40 px-4 py-14 text-center text-sm text-muted-foreground " +
        (className ?? "")
      }
    >
      {message}
    </div>
  );
}
