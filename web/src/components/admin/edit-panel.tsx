import type { ReactNode } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

interface EditPanelProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  children: ReactNode;
  footer?: ReactNode;
}

export function EditPanel({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
}: EditPanelProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        variant="sheet"
        className="left-auto right-0 top-0 flex h-dvh w-[min(680px,100vw)] max-w-none translate-x-0 translate-y-0 flex-col gap-0 rounded-none border-y-0 border-r-0 p-0"
      >
        <DialogHeader className="shrink-0 border-b border-border px-5 py-4">
          <DialogTitle>{title}</DialogTitle>
          {description ? (
            <DialogDescription>{description}</DialogDescription>
          ) : null}
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-5">
          {children}
        </div>
        {footer ? (
          <DialogFooter className="shrink-0 border-t border-border px-5 py-4">
            {footer}
          </DialogFooter>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
