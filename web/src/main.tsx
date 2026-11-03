import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router";
import { AppProviders } from "@/app/providers";
import { router } from "@/app/router";
import "@/app/i18n";
import "@/styles.css";

async function start() {
  if (import.meta.env.DEV && import.meta.env.VITE_USE_MOCK === "1") {
    const { startMocks } = await import("@/mocks/browser");
    await startMocks();
  }
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <AppProviders>
        <RouterProvider router={router} />
      </AppProviders>
    </StrictMode>,
  );
}

void start();
