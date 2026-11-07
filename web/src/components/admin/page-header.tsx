import type { ReactNode } from "react";
import { Badge } from "@/components/ui/badge";

export function PageHeader({
  title,
  subtitle,
  tag,
  actions,
}: {
  title: string;
  subtitle?: string;
  tag?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="flex items-end justify-between gap-6">
      <div>
        <div className="flex items-center gap-3">
          <h1 className="admin-page-title text-foreground">{title}</h1>
          {tag ? <Badge>{tag}</Badge> : null}
        </div>
        {subtitle ? (
          <p className="admin-body-text mt-1.5 max-w-3xl text-muted-foreground">
            {subtitle}
          </p>
        ) : null}
      </div>
      {actions ? <div className="shrink-0">{actions}</div> : null}
    </div>
  );
}
