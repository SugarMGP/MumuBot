import { handlers } from "./handlers";

export async function startMocks() {
  const { setupWorker } = await import("msw/browser");
  const worker = setupWorker(...handlers);
  await worker.start({
    serviceWorker: { url: "/mockServiceWorker.js" },
  });
  console.info("[mumu] mock endpoints enabled");
}
