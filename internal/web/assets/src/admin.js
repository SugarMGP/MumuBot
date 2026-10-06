import htmx from "htmx.org";
import Toastify from "toastify-js";
import { adminIconSvg } from "./icons.generated.js";
import * as echarts from "echarts/core";
import { PieChart, BarChart, LineChart } from "echarts/charts";
import { GridComponent, LegendComponent, TitleComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";

echarts.use([PieChart, BarChart, LineChart, GridComponent, LegendComponent, TitleComponent, TooltipComponent, CanvasRenderer]);

window.htmx = htmx;

const DEFAULT_TOAST_DELAY = 4200;
const adminLogPosition = { node: null, top: 0, atBottom: true, initialized: false, anchorKey: "", anchorOffset: 0 };

function prefersReducedMotion() {
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

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
      activeButton.dataset.originalHTML = activeButton.innerHTML;
      activeButton.textContent = "";
      const spinner = document.createElement("span");
      spinner.className = "loading loading-spinner loading-xs";
      activeButton.appendChild(spinner);
      activeButton.appendChild(document.createTextNode(" " + label));
    }
  }
}

function resetFormSubmitting(form) {
  if (!(form instanceof HTMLFormElement)) return;
  delete form.dataset.submitting;
  form.querySelectorAll("[data-admin-submitter-field]").forEach((field) => field.remove());
  form.querySelectorAll('button[type="submit"]').forEach((button) => {
    button.disabled = false;
    button.classList.remove("cursor-wait");
    if (button.dataset.originalHTML !== undefined) {
      button.innerHTML = button.dataset.originalHTML;
      delete button.dataset.originalHTML;
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
  if (kind === "error") return adminIconSvg["flash-error"];
  if (kind === "warn") return adminIconSvg["flash-warn"];
  return adminIconSvg["flash-success"];
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
  node.innerHTML = `<div class="admin-toast__icon">${toastIcon(kind)}</div><div class="admin-toast__content"><div data-toast-title class="font-semibold"></div><div data-toast-body class="mt-0.5 text-sm text-base-content/60"></div></div><button type="button" class="admin-toast__close" aria-label="关闭提示">${adminIconSvg.x}</button>`;
  const title = node.querySelector("[data-toast-title]");
  const body = node.querySelector("[data-toast-body]");
  if (!title || !body) return;
  title.textContent = normalized.title;
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

const HOVER_POPOVER_DELAY = 420;

function bindHoverPopovers() {
  document.querySelectorAll("[data-admin-hover-popover]").forEach((node) => {
    if (!(node instanceof HTMLElement) || node.dataset.hoverBound === "true") return;
    node.dataset.hoverBound = "true";
    let timer = 0;
    const open = (byHover) => {
      if (node instanceof HTMLDetailsElement) {
        node.dataset.hoverOpened = byHover ? "true" : "false";
        node.open = true;
      } else {
        node.classList.add("is-open");
      }
    };
    const close = () => {
      delete node.dataset.hoverOpened;
      if (node instanceof HTMLDetailsElement) node.open = false;
      else node.classList.remove("is-open");
    };
    node.addEventListener("pointerenter", () => {
      window.clearTimeout(timer);
      if (node instanceof HTMLDetailsElement && node.open) return;
      timer = window.setTimeout(() => open(true), HOVER_POPOVER_DELAY);
    });
    node.addEventListener("pointerleave", () => {
      window.clearTimeout(timer);
      if (node.dataset.hoverOpened === "true") close();
    });
    node.addEventListener("click", (event) => {
      if (node instanceof HTMLDetailsElement && node.dataset.hoverOpened === "true" && node.open) {
        // 悬停展开后点击表示固定展开：取消默认收起，转为常驻
        event.preventDefault();
        delete node.dataset.hoverOpened;
        return;
      }
      delete node.dataset.hoverOpened;
    });
    node.addEventListener("keydown", (event) => {
      if (event.key === "Escape") close();
    });
  });
}

function bindPromptEditors() {
  document.querySelectorAll("[data-prompt-editor]").forEach((node) => {
    if (!(node instanceof HTMLElement) || node.dataset.promptBound === "true") return;
    const button = node.querySelector("[data-prompt-edit]");
    const form = node.querySelector("[data-prompt-form]");
    const input = node.querySelector("[data-prompt-input]");
    if (!(button instanceof HTMLElement) || !(form instanceof HTMLFormElement) || !(input instanceof HTMLInputElement)) return;
    node.dataset.promptBound = "true";
    let original = input.value;
    let submitted = original;
    let submitting = false;
    const openEditor = () => {
      button.hidden = true;
      form.hidden = false;
      input.focus();
      input.setSelectionRange(input.value.length, input.value.length);
    };
    const closeEditor = () => {
      form.hidden = true;
      button.hidden = false;
    };
    const autoSave = () => {
      if (submitting || form.hidden) return;
      if (input.value === original) {
        closeEditor();
        return;
      }
      submitting = true;
      form.requestSubmit();
    };
    form.addEventListener("htmx:beforeRequest", () => {
      submitted = input.value;
      submitting = true;
    });
    form.addEventListener("htmx:afterRequest", (event) => {
      if (event.detail?.successful) original = submitted;
      submitting = false;
    });
    button.addEventListener("click", openEditor);
    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        autoSave();
        return;
      }
      if (event.key === "Escape") {
        input.value = original;
        closeEditor();
      }
    });
    input.addEventListener("blur", autoSave);
  });
}

function bootAdminPage() {
  const detail = readInitialToast();
  if (detail) showAdminToast(detail);
  clearFlashQueryParams();
  syncAdminLogFormat();
  followAdminLogs();
  syncLogDownloadURL();
  renderModelStats();
  syncStickerPageSize();
  bindHoverPopovers();
  bindPromptEditors();
}

function followAdminLogs() {
  const logs = document.querySelector("[data-admin-log-view]");
  if (!(logs instanceof HTMLElement)) return;
  if (adminLogPosition.node !== logs) {
    adminLogPosition.node = logs;
    adminLogPosition.top = 0;
    adminLogPosition.atBottom = true;
    adminLogPosition.initialized = false;
    adminLogPosition.anchorKey = "";
    adminLogPosition.anchorOffset = 0;
  }
  const shouldFollow = !adminLogPosition.initialized || adminLogPosition.atBottom;
  if (!adminLogPosition.initialized) logs.scrollTop = logs.scrollHeight;
  else if (shouldFollow) logs.scrollTo({ top: logs.scrollHeight, behavior: prefersReducedMotion() ? "auto" : "smooth" });
  else {
    const anchor = Array.from(logs.querySelectorAll("[data-admin-log-line]")).find((line) => line.dataset.logKey === adminLogPosition.anchorKey);
    logs.scrollTop = anchor instanceof HTMLElement ? Math.max(0, anchor.offsetTop - adminLogPosition.anchorOffset) : Math.min(adminLogPosition.top, logs.scrollHeight);
  }
  if (shouldFollow) {
    adminLogPosition.top = logs.scrollHeight;
    adminLogPosition.atBottom = true;
  } else {
    captureAdminLogPosition();
  }
  adminLogPosition.initialized = true;
}

function syncAdminLogFormat() {
  const logs = document.querySelector("[data-admin-log-view]");
  const enabled = logs instanceof HTMLElement && document.getElementById("admin-log-format")?.checked === true;
  document.documentElement.classList.toggle("admin-log-formatted", enabled);
}

function syncLogDownloadURL() {
  const form = document.getElementById("admin-log-filter-form");
  const link = document.querySelector("[data-admin-log-download]");
  if (!(form instanceof HTMLFormElement) || !(link instanceof HTMLAnchorElement)) return;
  const params = new URLSearchParams(new FormData(form));
  params.delete("view");
  params.delete("fragment");
  link.href = `/admin/system/logs/download?${params.toString()}`;
}

function captureAdminLogPosition() {
  const logs = document.querySelector("[data-admin-log-view]");
  if (!(logs instanceof HTMLElement)) return;
  adminLogPosition.top = logs.scrollTop;
  adminLogPosition.atBottom = logs.scrollHeight - logs.scrollTop - logs.clientHeight < 24;
  const anchor = Array.from(logs.querySelectorAll("[data-admin-log-line]")).find((line) => line instanceof HTMLElement && line.offsetTop + line.offsetHeight > logs.scrollTop);
  adminLogPosition.anchorKey = anchor instanceof HTMLElement ? anchor.dataset.logKey || "" : "";
  adminLogPosition.anchorOffset = anchor instanceof HTMLElement ? anchor.offsetTop - logs.scrollTop : 0;
}

function prepareAdminLogSwap(event) {
  const target = event.detail?.target;
  if (!(target instanceof HTMLElement) || !target.matches("[data-admin-log-view]")) {
    captureAdminLogPosition();
    return;
  }
  const selection = window.getSelection();
  if (selection && !selection.isCollapsed && (target.contains(selection.anchorNode) || target.contains(selection.focusNode))) {
    event.detail.shouldSwap = false;
    return;
  }
  captureAdminLogPosition();
}

// 表情包列表按实际列数保持整三行，窗口宽度变化时重新计算每页数量
let stickerPageSizeTimer = 0;
function syncStickerPageSize() {
  const grid = document.querySelector("[data-sticker-grid]");
  if (!(grid instanceof HTMLElement)) return;
  const pageSize = Number(grid.dataset.pageSize);
  const columns = (getComputedStyle(grid).gridTemplateColumns || "").split(" ").filter(Boolean).length;
  if (!pageSize || !columns) return;
  const target = Math.min(100, columns * 3);
  if (target === pageSize) return;
  const url = new URL(window.location.href);
  url.searchParams.set("page_size", String(target));
  url.searchParams.set("page", "1");
  htmx.ajax("GET", url.pathname + url.search, { target: "#admin-page-body", swap: "outerHTML" });
  window.history.replaceState({}, "", url.pathname + url.search);
}

function renderModelStats() {
  const root = document.querySelector("[data-model-stats]");
  if (!(root instanceof HTMLElement)) return;
  let data;
  try { data = JSON.parse(root.dataset.modelStats || "{}"); } catch { return; }
  const series = Array.isArray(data.series) ? data.series : [];
  const statsRange = root.dataset.modelStatsRange;
  const labelOptions = statsRange === "all" ? { year: "numeric", month: "2-digit" } : statsRange === "30d" ? { month: "2-digit", day: "2-digit" } : { month: "2-digit", day: "2-digit", hour: "2-digit" };
  const labels = series.map((item) => new Date(item.bucket_start).toLocaleString("zh-CN", labelOptions));
  const trend = document.getElementById("model-stats-trend");
  if (trend instanceof HTMLElement) {
    const chart = chartFor(trend);
    chart.setOption({
      tooltip: { trigger: "axis" }, legend: { top: 4, data: ["请求", "Token", "平均耗时"] },
      grid: { left: 48, right: 48, top: 52, bottom: 44 }, xAxis: { type: "category", data: labels },
      yAxis: [{ type: "value" }, { type: "value" }, { type: "value", show: false }],
      series: [
        { name: "请求", type: "line", yAxisIndex: 0, smooth: true, data: series.map((item) => item.request_count), itemStyle: { color: "#e85d75" } },
        { name: "Token", type: "bar", yAxisIndex: 2, data: series.map((item) => item.total_tokens), itemStyle: { color: "#159a8c", opacity: 0.45 } },
        { name: "平均耗时", type: "line", yAxisIndex: 1, smooth: true, data: series.map((item) => item.request_count ? Math.round(item.latency_ms_sum / item.request_count) : 0), itemStyle: { color: "#6f65a8" } },
      ],
    });
  }
  renderDistributionChart("model-stats-task", "任务", data.by_task);
  renderDistributionChart("model-stats-model", "模型", data.by_model);
}

function renderDistributionChart(id, title, rows) {
  const target = document.getElementById(id);
  if (!(target instanceof HTMLElement)) return;
  chartFor(target).setOption({
    color: ["#e85d75", "#159a8c", "#8b7bc7", "#d59a22", "#5b87d8", "#62c8bb"],
    tooltip: { trigger: "item" }, title: { text: title, left: "center", textStyle: { fontSize: 13 } },
    legend: { bottom: 0, itemWidth: 10, itemHeight: 10, itemGap: 12, textStyle: { fontSize: 11, color: "#666673" } },
    series: [{ type: "pie", radius: ["40%", "64%"], center: ["50%", "48%"], label: { show: false }, data: (Array.isArray(rows) ? rows : []).map((row) => ({ name: row.label, value: row.request_count })) }],
  });
}

function chartFor(target) {
  return echarts.getInstanceByDom(target) || echarts.init(target);
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
  form.querySelectorAll("[data-admin-submitter-field]").forEach((field) => field.remove());
  if (event.submitter?.name) {
    const field = document.createElement("input");
    field.type = "hidden";
    field.name = event.submitter.name;
    field.value = event.submitter.value;
    field.dataset.adminSubmitterField = "true";
    form.appendChild(field);
  }
  markFormSubmitting(form, event.submitter instanceof HTMLButtonElement ? event.submitter : null);
}, true);

document.addEventListener("htmx:beforeRequest", (event) => markNavigationPending(event.detail?.elt));

function markNavigationPending(elt) {
  const control = elt instanceof Element ? elt : null;
  if (!(control instanceof HTMLElement) || control.closest("form")) return;
  control.style.cursor = "wait";
  control.setAttribute("aria-busy", "true");
}

function clearNavigationPending(elt) {
  const control = elt instanceof Element ? elt : null;
  if (!(control instanceof HTMLElement)) return;
  control.style.removeProperty("cursor");
  control.removeAttribute("aria-busy");
}

document.addEventListener("htmx:afterRequest", (event) => {
  resetFormSubmitting(formFromEvent(event));
  clearNavigationPending(event.detail?.elt);
});
document.addEventListener("htmx:responseError", (event) => {
  resetFormSubmitting(formFromEvent(event));
  clearNavigationPending(event.detail?.elt);
  // 服务端已通过 HX-Trigger 给出具体失败提示时，不再叠加通用错误提示
  const hasServerToast = /\bHX-Trigger(-After-Swap|-After-Settle)?:/i.test(event.detail?.xhr?.getAllResponseHeaders?.() || "");
  if (!hasServerToast && event.detail?.xhr?.status) {
    showAdminToast({ kind: "error", title: "操作失败", body: `请求未完成（HTTP ${event.detail.xhr.status}），请稍后重试。` });
  }
});
document.addEventListener("htmx:sendError", (event) => {
  resetFormSubmitting(formFromEvent(event));
  clearNavigationPending(event.detail?.elt);
  showAdminToast({ kind: "error", title: "连接失败", body: "网络请求未送达，请检查连接后重试。" });
});
document.addEventListener("htmx:beforeSwap", prepareAdminLogSwap);
document.addEventListener("htmx:beforeCleanupElement", (event) => {
  if (event.detail?.elt instanceof HTMLElement) echarts.getInstanceByDom(event.detail.elt)?.dispose();
});
document.addEventListener("htmx:afterSwap", () => { syncAdminLogFormat(); followAdminLogs(); syncLogDownloadURL(); renderModelStats(); syncStickerPageSize(); bindHoverPopovers(); bindPromptEditors(); });
window.addEventListener("resize", () => {
  ["model-stats-trend", "model-stats-task", "model-stats-model"].forEach((id) => {
    const target = document.getElementById(id);
    if (target) echarts.getInstanceByDom(target)?.resize();
  });
  window.clearTimeout(stickerPageSizeTimer);
  stickerPageSizeTimer = window.setTimeout(syncStickerPageSize, 250);
});
document.addEventListener("input", syncLogDownloadURL);
document.addEventListener("change", (event) => {
  syncLogDownloadURL();
  if (event.target instanceof HTMLInputElement && event.target.id === "admin-log-format") {
    captureAdminLogPosition();
    syncAdminLogFormat();
    followAdminLogs();
  }
});
document.addEventListener("scroll", (event) => {
  if (event.target instanceof HTMLElement && event.target.matches("[data-admin-log-view]")) captureAdminLogPosition();
}, true);
document.addEventListener("admin:toast", (event) => {
  const detail = normalizeToastDetail(event.detail?.value || event.detail);
  if (detail) showAdminToast(detail);
});
document.addEventListener("admin:action-dialog-close", () => closeDialog("#admin-action-dialog"));
