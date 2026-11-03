import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { stickerApi } from "@/api/client-endpoints";
import type { Sticker } from "@/api/types";
import { ConfirmDialog } from "@/components/admin/confirm-dialog";
import { EmptyState, FilterPanel } from "@/components/admin/list";
import { PageHeader } from "@/components/admin/page-header";
import { PageSkeleton } from "@/components/admin/loading";
import { Pagination } from "@/components/admin/pagination";
import { SortToolbar } from "@/components/admin/sort-toolbar";
import { toastApiError, toastSuccess } from "@/components/admin/toast";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/form-controls";
import { useQueryParams } from "@/hooks/use-query-params";
import { formatDateTime, truncateMiddle } from "@/lib/format";

function previewText(description: string): string {
  const text = description.trim();
  if (!text) return "";
  return Array.from(text).slice(0, 2).join("");
}

function StickerImage({
  sticker,
  onOpen,
  alt,
}: {
  sticker: Sticker;
  onOpen: () => void;
  alt: string;
}) {
  const [failed, setFailed] = useState(false);
  const fallback = previewText(sticker.description);
  return (
    <button
      type="button"
      className="h-full w-full"
      onClick={onOpen}
      aria-label={alt}
    >
      {failed || !sticker.file_url ? (
        <span className="flex h-full w-full items-center justify-center text-xl font-semibold text-muted-foreground">
          {fallback || "?"}
        </span>
      ) : (
        <img
          src={sticker.file_url}
          alt={alt}
          loading="lazy"
          className="h-full w-full object-contain"
          onError={() => setFailed(true)}
        />
      )}
    </button>
  );
}

export default function StickersPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { params, update, page } = useQueryParams();
  const keyword = params.get("keyword") || "";
  const sort = params.get("sort") || "use";
  const order = params.get("order") || "desc";
  const pageSize = Number(params.get("page_size") || "12") || 12;
  const [input, setInput] = useState(keyword);
  const [preview, setPreview] = useState<Sticker | null>(null);
  const [deleting, setDeleting] = useState<Sticker | null>(null);
  const gridRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setInput(params.get("keyword") || "");
  }, [params]);

  const query = useQuery({
    queryKey: ["stickers", keyword, sort, order, page, pageSize],
    queryFn: () =>
      stickerApi.list({ keyword, sort, order, page, page_size: pageSize }),
  });

  const remove = useMutation({
    mutationFn: (sticker: Sticker) => stickerApi.remove(sticker.id),
    onSuccess: async () => {
      setDeleting(null);
      await queryClient.invalidateQueries({ queryKey: ["stickers"] });
      toastSuccess(t("stickers.deleted"));
    },
    onError: (error) => toastApiError(error),
  });

  useEffect(() => {
    const grid = gridRef.current;
    if (!grid) return;
    const observer = new ResizeObserver(() => {
      const columns = (getComputedStyle(grid).gridTemplateColumns || "")
        .split(" ")
        .filter(Boolean).length;
      if (!columns) return;
      const target = Math.min(100, columns * 3);
      if (target !== pageSize) {
        update({ page_size: target }, { resetPage: true, replace: true });
      }
    });
    observer.observe(grid);
    return () => observer.disconnect();
  }, [pageSize, update, query.data?.items.length]);

  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    update({ keyword: input }, { resetPage: true });
  };

  return (
    <div className="space-y-6">
      <PageHeader title={t("stickers.title")} subtitle={t("stickers.subtitle")} />

      <FilterPanel title={t("common.filter")}>
        <form className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-3" onSubmit={submit}>
          <Field label={t("common.search")}>
            <Input
              type="search"
              value={input}
              placeholder={t("stickers.searchPlaceholder")}
              onChange={(event) => setInput(event.target.value)}
            />
          </Field>
          <Button type="submit">{t("common.filter")}</Button>
        </form>
      </FilterPanel>

      <SortToolbar
        options={[
          { key: "use", label: t("stickers.sortUse") },
          { key: "updated", label: t("common.updatedAt") },
          { key: "created", label: t("common.createdAt") },
        ]}
        currentSort={sort}
        currentOrder={order}
        onSort={(key) => update({ sort: key }, { resetPage: true })}
        onOrder={(value) => update({ order: value }, { resetPage: true })}
      />

      {query.isPending ? (
        <PageSkeleton />
      ) : query.isError ? (
        <EmptyState message={t("common.loadFailed")} />
      ) : query.data.items.length === 0 ? (
        <EmptyState message={t("stickers.empty")} />
      ) : (
        <div
          ref={gridRef}
          className="grid grid-cols-[repeat(auto-fill,minmax(clamp(200px,17vw,240px),1fr))] gap-4"
        >
          {query.data.items.map((sticker) => {
            const description =
              sticker.description.trim() || t("stickers.noDescription");
            const title =
              sticker.description.trim() ||
              t("stickers.fallbackName", { id: sticker.id });
            const alt = sticker.description.trim()
              ? t("stickers.previewAlt", { description })
              : t("stickers.previewAltFallback", { id: sticker.id });
            return (
              <Card key={sticker.id} className="overflow-hidden">
                <figure className="m-3 mb-0 aspect-square overflow-hidden rounded-xl bg-muted p-2">
                  <StickerImage
                    sticker={sticker}
                    onOpen={() => setPreview(sticker)}
                    alt={alt}
                  />
                </figure>
                <div className="space-y-2 p-3">
                  <h2
                    className="truncate text-sm font-medium"
                    title={title}
                  >
                    {title}
                  </h2>
                  <p
                    className="truncate text-xs text-muted-foreground"
                    title={sticker.file_name}
                  >
                    {truncateMiddle(sticker.file_name, 28, 8)}
                  </p>
                  <div className="flex items-center justify-between text-xs text-muted-foreground">
                    <span>
                      {t("stickers.useCount", { count: sticker.use_count })}
                    </span>
                    <time>{formatDateTime(sticker.updated_at)}</time>
                  </div>
                  <div className="flex justify-end">
                    <Button
                      variant="ghost"
                      size="xs"
                      className="text-destructive hover:bg-destructive/10"
                      onClick={() => setDeleting(sticker)}
                    >
                      {t("common.delete")}
                    </Button>
                  </div>
                </div>
              </Card>
            );
          })}
        </div>
      )}

      {query.data ? (
        <Pagination
          page={query.data.page}
          pageSize={query.data.page_size}
          total={query.data.total}
          onPage={(next) => update({ page: next })}
        />
      ) : null}

      <Dialog
        open={preview !== null}
        onOpenChange={(open) => !open && setPreview(null)}
      >
        <DialogContent className="max-w-4xl">
          <DialogHeader>
            <DialogTitle>{t("stickers.previewTitle")}</DialogTitle>
            <DialogDescription>
              {preview?.description || t("stickers.noDescription")}
            </DialogDescription>
          </DialogHeader>
          {preview ? (
            <div className="space-y-4">
              <div className="flex max-h-[60vh] justify-center rounded-xl bg-muted p-4">
                {preview.file_url ? (
                  <img
                    src={preview.file_url}
                    alt={preview.description || preview.file_name}
                    className="max-h-[54vh] object-contain"
                  />
                ) : (
                  <span className="py-16 text-sm text-muted-foreground">
                    {t("stickers.noPreview")}
                  </span>
                )}
              </div>
              <div className="grid grid-cols-2 gap-3 text-sm">
                <div>
                  <p className="text-xs text-muted-foreground">
                    {t("stickers.fileName")}
                  </p>
                  <p className="mt-1 break-all">{preview.file_name}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">
                    {t("stickers.updatedAt")}
                  </p>
                  <p className="mt-1">{formatDateTime(preview.updated_at)}</p>
                </div>
              </div>
            </div>
          ) : null}
          <DialogFooter>
            <Button variant="outline" onClick={() => setPreview(null)}>
              {t("common.close")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t("stickers.deleteTitle")}
        description={t("stickers.deleteBody")}
        confirmLabel={t("common.delete")}
        loading={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </div>
  );
}
