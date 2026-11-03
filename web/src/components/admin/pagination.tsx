import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { ChevronLeft, ChevronRight } from "lucide-react";

export function Pagination({
  page,
  pageSize,
  total,
  onPage,
}: {
  page: number;
  pageSize: number;
  total: number;
  onPage: (page: number) => void;
}) {
  const { t } = useTranslation();
  const start = total === 0 ? 0 : (page - 1) * pageSize + 1;
  const end = Math.min(page * pageSize, total);
  const hasPrev = page > 1;
  const hasNext = page * pageSize < total;

  return (
    <div className="mt-6 flex items-center justify-between border-t border-border pt-4 text-sm text-muted-foreground">
      <div>
        {t("common.page", { page, start, end, total })}
      </div>
      <div className="flex items-center gap-1">
        <Button
          variant="ghost"
          size="icon-sm"
          className="border border-border"
          disabled={!hasPrev}
          onClick={() => onPage(page - 1)}
          aria-label={t("common.prevPage")}
        >
          <ChevronLeft />
        </Button>
        <span className="inline-flex h-8 min-w-8 items-center justify-center rounded-lg bg-primary px-2 text-xs font-semibold text-primary-foreground">
          {page}
        </span>
        <Button
          variant="ghost"
          size="icon-sm"
          className="border border-border"
          disabled={!hasNext}
          onClick={() => onPage(page + 1)}
          aria-label={t("common.nextPage")}
        >
          <ChevronRight />
        </Button>
      </div>
    </div>
  );
}
