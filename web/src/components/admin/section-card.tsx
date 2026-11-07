import type { ReactNode } from "react";
import { Card } from "@/components/ui/card";

export function SectionCard({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Card className={className}>
      <div className="admin-card-stack admin-card-pad">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="admin-card-title text-foreground">{title}</h2>
            {description ? (
              <p className="admin-card-desc mt-1">{description}</p>
            ) : null}
          </div>
          {actions ? <div className="shrink-0">{actions}</div> : null}
        </div>
        {children}
      </div>
    </Card>
  );
}
