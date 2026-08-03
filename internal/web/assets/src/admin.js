import htmx from "htmx.org";
import Toastify from "toastify-js";
import * as echarts from "echarts/core";
import { BarChart } from "echarts/charts";
import { DatasetComponent, GridComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";

echarts.use([BarChart, DatasetComponent, GridComponent, TooltipComponent, CanvasRenderer]);
window.htmx = htmx;

const DEFAULT_TOAST_DELAY = 4200;
const REDUCED_MOTION = window.matchMedia("(prefers-reduced-motion: reduce)");

function markFormSubmitting(form, activeButton) {
  if (!(form instanceof HTMLFormElement)) return;
  form.dataset.submitting = "true";
  form.querySelectorAll('button[type="submit"]').forEach((button) => {
    button.disabled = true;
    button.classList.add("cursor-wait");
  });
  if (activeButton instanceof HTMLButtonElement) {
    const label = activeButton.dataset.loadingLabel;
    if (label) {
      activeButton.dataset.originalLabel = activeButton.textContent || "";
      activeButton.textContent = label;
    }
  }
}

function resetFormSubmitting(form) {
  if (!(form instanceof HTMLFormElement)) return;
  delete form.dataset.submitting;
  form.querySelectorAll('button[type="submit"]').forEach((button) => {
    button.disabled = false;
    button.classList.remove("cursor-wait");
    if (button.dataset.originalLabel) {
      button.textContent = button.dataset.originalLabel;
      delete button.dataset.originalLabel;
    }
  });
}

function formFromEvent(event) {
  const elt = event?.detail?.elt;
  if (elt instanceof HTMLFormElement) return elt;
  if (elt && typeof elt.closest === "function") return elt.closest("form");
  return event?.target instanceof HTMLFormElement ? event.target : null;
}

function normalizeToastDetail(detail) {
  if (!detail || typeof detail !== "object") return null;
  const normalized = {
    kind: String(detail.kind || detail.Kind || "").trim(),
    title: String(detail.title || detail.Title || "").trim(),
    body: String(detail.body || detail.Body || "").trim(),
  };
  return normalized.title ? normalized : null;
}

function readInitialToast() {
  const payload = document.getElementById("admin-flash-payload");
  if (!(payload instanceof HTMLMetaElement)) return null;
  try {
    return normalizeToastDetail(JSON.parse(payload.content || "{}"));
  } catch {
    return null;
  } finally {
    payload.remove();
  }
}

function clearFlashQueryParams() {
  const url = new URL(window.location.href);
  let changed = false;
  ["flash_kind", "flash_title", "flash_body"].forEach((key) => {
    if (url.searchParams.has(key)) {
      url.searchParams.delete(key);
      changed = true;
    }
  });
  if (changed) window.history.replaceState({}, "", url.toString());
}

function toastKind(kind) {
  return kind === "error" || kind === "warn" ? kind : "success";
}

function toastIcon(kind) {
  if (kind === "error") return '<svg class="size-4" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8"><circle cx="10" cy="10" r="6.5"/><path d="M10 6.5v4.5M10 13.5h.01"/></svg>';
  if (kind === "warn") return '<svg class="size-4" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M10 3.5 17 16.5H3L10 3.5Z"/><path d="M10 7.5v4M10 13.5h.01"/></svg>';
  return '<svg class="size-4" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8"><path d="m5.5 10 3 3 6-6"/></svg>';
}

function showAdminToast(detail) {
  const normalized = normalizeToastDetail(detail);
  if (!normalized) return;
  const kind = toastKind(normalized.kind);
  const node = document.createElement("div");
  node.className = "admin-toast";
  node.dataset.kind = kind;
  node.setAttribute("role", kind === "success" ? "status" : "alert");
  node.setAttribute("aria-live", kind === "success" ? "polite" : "assertive");
  node.innerHTML = `<div class="admin-toast__icon">${toastIcon(kind)}</div><div class="admin-toast__content"><div data-toast-title class="font-semibold"></div><div data-toast-body class="mt-0.5 text-sm text-base-content/65"></div></div><button type="button" class="admin-toast__close" aria-label="关闭提示"><svg class="size-4" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8"><path d="m6 6 8 8M14 6l-8 8"/></svg></button>`;
  node.querySelector("[data-toast-title]").textContent = normalized.title;
  const body = node.querySelector("[data-toast-body]");
  body.textContent = normalized.body;
  body.hidden = !normalized.body;
  const toast = Toastify({
    node,
    gravity: "top",
    position: "right",
    offset: { x: 24, y: 88 },
    duration: DEFAULT_TOAST_DELAY,
    stopOnFocus: true,
    close: false,
    className: "admin-toastify",
  });
  toast.showToast();
  node.querySelector(".admin-toast__close")?.addEventListener("click", () => toast.hideToast());
}

function dialogFromSelector(selector) {
  const dialog = selector ? document.querySelector(selector) : null;
  return dialog instanceof HTMLDialogElement ? dialog : null;
}

function openDialog(selector) {
  const dialog = dialogFromSelector(selector);
  if (dialog && !dialog.open) dialog.showModal();
}

function closeDialog(selector) {
  const dialog = dialogFromSelector(selector);
  if (dialog?.open) dialog.close();
}

function togglePassword(button) {
  const selector = button.dataset.passwordTarget;
  const input = selector ? document.querySelector(selector) : null;
  if (!(input instanceof HTMLInputElement)) return;
  const visible = input.type === "password";
  input.type = visible ? "text" : "password";
  button.setAttribute("aria-pressed", String(visible));
  const label = button.querySelector("[data-password-label]");
  if (label) label.textContent = visible ? "隐藏" : "显示";
}

function moodPercent(kind, raw) {
  const value = Number(raw);
  if (!Number.isFinite(value)) return 0;
  const normalized = kind === "valence" ? (value + 1) / 2 : value;
  return Math.round(Math.min(1, Math.max(0, normalized)) * 100);
}

function initMoodCharts(root = document) {
  root.querySelectorAll?.("[data-admin-mood-chart]").forEach((element) => {
    if (!(element instanceof HTMLElement)) return;
    const existing = echarts.getInstanceByDom(element);
    if (existing) {
      existing.resize();
      return;
    }
    const chart = echarts.init(element, null, { renderer: "canvas" });
    const rows = [
      ["心情", moodPercent("valence", element.dataset.valence), "#e85d75"],
      ["精力", moodPercent("energy", element.dataset.energy), "#159a8c"],
      ["社交欲", moodPercent("sociability", element.dataset.sociability), "#d59a22"],
    ];
    chart.setOption({
      animation: !REDUCED_MOTION.matches,
      grid: { left: 58, right: 40, top: 12, bottom: 10 },
      tooltip: { trigger: "axis", axisPointer: { type: "shadow" }, formatter: (items) => `${items[0].name}：${items[0].value}%` },
      xAxis: { type: "value", min: 0, max: 100, show: false },
      yAxis: { type: "category", inverse: true, data: rows.map((row) => row[0]), axisLine: { show: false }, axisTick: { show: false }, axisLabel: { color: "#4b4c59", fontSize: 13 } },
      series: [{ type: "bar", data: rows.map((row) => ({ value: row[1], itemStyle: { color: row[2], borderRadius: 8 } })), barWidth: 8, showBackground: true, backgroundStyle: { color: "#eef0f4", borderRadius: 8 }, label: { show: true, position: "right", formatter: "{c}%", color: "#727381", fontSize: 12 } }],
    });
  });
}

function disposeMoodCharts(root) {
  root?.querySelectorAll?.("[data-admin-mood-chart]").forEach((element) => echarts.getInstanceByDom(element)?.dispose());
}

function bootAdminPage() {
  const detail = readInitialToast();
  if (detail) showAdminToast(detail);
  clearFlashQueryParams();
  initMoodCharts();
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", bootAdminPage, { once: true });
else queueMicrotask(bootAdminPage);

document.addEventListener("click", (event) => {
  const target = event.target instanceof Element ? event.target : null;
  const trigger = target?.closest("[data-admin-dialog]");
  if (trigger instanceof HTMLElement) openDialog(trigger.dataset.adminDialog);
  const closer = target?.closest("[data-admin-dialog-close]");
  if (closer instanceof HTMLElement) closeDialog(closer.dataset.adminDialogClose);
  const passwordButton = target?.closest("[data-password-target]");
  if (passwordButton instanceof HTMLButtonElement) togglePassword(passwordButton);
});

document.addEventListener("click", (event) => {
  if (event.target instanceof HTMLDialogElement && event.target.classList.contains("modal")) event.target.close();
});

document.addEventListener("submit", (event) => {
  const form = event.target;
  if (!(form instanceof HTMLFormElement)) return;
  if (form.dataset.submitting === "true") {
    event.preventDefault();
    return;
  }
  markFormSubmitting(form, event.submitter instanceof HTMLButtonElement ? event.submitter : null);
}, true);

document.addEventListener("htmx:beforeSwap", (event) => disposeMoodCharts(event.detail?.target));
document.addEventListener("htmx:afterSwap", (event) => initMoodCharts(event.detail?.target || document));
document.addEventListener("htmx:afterRequest", (event) => resetFormSubmitting(formFromEvent(event)));
document.addEventListener("htmx:responseError", (event) => resetFormSubmitting(formFromEvent(event)));
document.addEventListener("htmx:sendError", (event) => resetFormSubmitting(formFromEvent(event)));
document.addEventListener("admin:toast", (event) => {
  const detail = normalizeToastDetail(event.detail?.value || event.detail);
  if (detail) showAdminToast(detail);
});
document.addEventListener("admin:action-dialog-close", () => closeDialog("#admin-action-dialog"));
window.addEventListener("resize", () => document.querySelectorAll("[data-admin-mood-chart]").forEach((element) => echarts.getInstanceByDom(element)?.resize()));
