import { Component, type ErrorInfo, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import i18n from "./i18n";

interface State {
  hasError: boolean;
}

export class AppErrorBoundary extends Component<
  { children: ReactNode },
  State
> {
  state: State = { hasError: false };

  static getDerivedStateFromError(): State {
    return { hasError: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("admin page error", error, info);
  }

  render() {
    if (this.state.hasError) {
      return (
        <div className="mx-auto max-w-lg py-24 text-center">
          <h1 className="text-xl font-semibold text-foreground">
            {i18n.t("common.pageError")}
          </h1>
          <p className="mt-2 text-sm text-muted-foreground">
            {i18n.t("common.pageErrorHint")}
          </p>
          <Button
            className="mt-6"
            onClick={() => {
              this.setState({ hasError: false });
              window.location.reload();
            }}
          >
            {i18n.t("common.retry")}
          </Button>
        </div>
      );
    }
    return this.props.children;
  }
}
