import { useTranslation } from "react-i18next";
import { AdminIcon } from "@/components/admin/icons";
import { Button } from "@/components/ui/button";

export interface SortOption {
  key: string;
  label: string;
}

export function SortToolbar({
  options,
  currentSort,
  currentOrder,
  onSort,
  onOrder,
}: {
  options: SortOption[];
  currentSort: string;
  currentOrder: string;
  onSort: (key: string) => void;
  onOrder: (order: string) => void;
}) {
  const { t } = useTranslation();
  const activeLabel =
    options.find((option) => option.key === currentSort)?.label ??
    options[0]?.label ??
    "";
  const summary = t("common.currentSort", {
    sort: activeLabel,
    order: currentOrder === "asc" ? t("common.asc") : t("common.desc"),
  });

  return (
    <div className="flex items-center justify-between gap-4">
      <div className="admin-body-text text-muted-foreground">{summary}</div>
      <div className="flex items-center gap-2">
        <div className="inline-flex overflow-hidden rounded-lg border border-border">
          {options.map((option) => {
            const active = option.key === currentSort;
            return (
              <button
                key={option.key}
                type="button"
                className={
                  active
                    ? "bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground"
                    : "bg-card px-3 py-1.5 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                }
                onClick={() => onSort(option.key)}
              >
                {option.label}
              </button>
            );
          })}
        </div>
        <div className="inline-flex overflow-hidden rounded-lg border border-border">
          {[
            { value: "desc", icon: "sort-desc", label: t("common.desc") },
            { value: "asc", icon: "sort-asc", label: t("common.asc") },
          ].map((option) => {
            const active = option.value === currentOrder;
            return (
              <Button
                key={option.value}
                type="button"
                variant={active ? "default" : "ghost"}
                size="xs"
                className="rounded-none"
                onClick={() => onOrder(option.value)}
                aria-label={option.label}
                title={option.label}
              >
                <AdminIcon name={option.icon} className="size-4" />
              </Button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
