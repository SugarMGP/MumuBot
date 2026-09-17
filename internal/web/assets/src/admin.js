import htmx from "htmx.org";
import Toastify from "toastify-js";
import * as echarts from "echarts/core";
import { PieChart, BarChart, LineChart, GraphChart } from "echarts/charts";
import { GridComponent, LegendComponent, TitleComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";

echarts.use([PieChart, BarChart, LineChart, GraphChart, GridComponent, LegendComponent, TitleComponent, TooltipComponent, CanvasRenderer]);

window.htmx = htmx;

const DEFAULT_TOAST_DELAY = 4200;
const adminLogPosition = { node: null, top: 0, atBottom: true, initialized: false, anchorKey: "", anchorOffset: 0 };

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
  const common = 'viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"';
  if (kind === "error") return `<svg class="size-4" ${common}><circle cx="12" cy="12" r="10"/><path d="m15 9-6 6"/><path d="m9 9 6 6"/></svg>`;
  if (kind === "warn") return `<svg class="size-4" ${common}><path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/></svg>`;
  return `<svg class="size-4" ${common}><circle cx="12" cy="12" r="10"/><path d="m9 12 2 2 4-4"/></svg>`;
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
  node.innerHTML = `<div class="admin-toast__icon">${toastIcon(kind)}</div><div class="admin-toast__content"><div data-toast-title class="font-semibold"></div><div data-toast-body class="mt-0.5 text-sm text-base-content/65"></div></div><button type="button" class="admin-toast__close" aria-label="关闭提示"><svg class="size-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg></button>`;
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

function bootAdminPage() {
  const detail = readInitialToast();
  if (detail) showAdminToast(detail);
  clearFlashQueryParams();
  syncAdminLogFormat();
  followAdminLogs();
  syncLogDownloadURL();
  renderModelStats();
  renderKnowledgeGraph();
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
  else if (shouldFollow) logs.scrollTo({ top: logs.scrollHeight, behavior: "smooth" });
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
      yAxis: [{ type: "value" }, { type: "value" }],
      series: [
        { name: "请求", type: "line", smooth: true, data: series.map((item) => item.request_count), itemStyle: { color: "#e85d75" } },
        { name: "Token", type: "bar", data: series.map((item) => item.total_tokens), itemStyle: { color: "#159a8c" } },
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
    tooltip: { trigger: "item" }, title: { text: title, left: "center", textStyle: { fontSize: 13 } },
    series: [{ type: "pie", radius: ["42%", "70%"], center: ["50%", "56%"], label: { show: false }, data: (Array.isArray(rows) ? rows : []).map((row) => ({ name: row.label, value: row.request_count })) }],
  });
}

function chartFor(target) {
  const existing = echarts.getInstanceByDom(target);
  if (existing) existing.dispose();
  return echarts.init(target);
}

function renderKnowledgeGraph() {
  document.querySelectorAll("[data-knowledge-graph]").forEach((target) => {
    if (echarts.getInstanceByDom(target)) return;
    const data = JSON.parse(target.dataset.knowledgeGraph);
    const dense = data.nodes.length > 30;
    const chart = chartFor(target);
    chart.setOption({ animation: !window.matchMedia("(prefers-reduced-motion: reduce)").matches,
      tooltip: { renderMode: "richText", formatter: (entry) => entry.dataType === "node" ? `${entry.data.name}\n${entry.data.status ?? ""}` : `${entry.data.name ?? "关联"}\n${entry.data.status ?? entry.data.value ?? ""}` },
      series: [{ type: "graph", left: 65, right: 65, top: 50, bottom: 60, layout: dense ? "circular" : "force", force: { repulsion: 520, edgeLength: [110, 190], gravity: 0.05, layoutAnimation: false }, draggable: true, roam: true, symbolSize: dense ? 12 : undefined,
        emphasis: { focus: "adjacency", scale: 1.18, label: { show: true, fontWeight: 600 } }, data: data.nodes, links: data.edges,
        label: { show: data.nodes.length <= 8, position: "bottom", width: 132, overflow: "truncate", color: "#49394d", fontSize: 12, fontWeight: 500, backgroundColor: "rgba(255,255,255,.86)", borderColor: "rgba(224,214,225,.9)", borderWidth: 1, borderRadius: 8, padding: [4, 7] },
        edgeSymbol: ["none", "none"], edgeSymbolSize: 8,
        itemStyle: { shadowBlur: 0 }, lineStyle: { color: "#159a8c", width: 2.5, curveness: 0.14, opacity: 0.8 },
        edgeLabel: { show: false, formatter: (edge) => edge.data.name ?? "", color: "#7a6578", fontSize: 11, backgroundColor: "rgba(255,255,255,.78)", padding: [3, 6], borderRadius: 8 } }],
    });
    syncGraphSelection();
    chart.on("click", (entry) => {
      if (!entry.data.url) return;
      const url = new URL(entry.data.url, window.location.origin);
      url.searchParams.set("return_to", target.dataset.graphReturn || window.location.pathname + window.location.search);
      htmx.ajax("GET", url.pathname + url.search, { target: target.dataset.graphPanel, swap: "innerHTML" });
    });
  });
}

function syncGraphSelection() {
  const panel = document.querySelector("#knowledge-graph-panel [data-selected-kind]");
  const target = document.querySelector("[data-graph-panel]");
  if (!panel || !target) return;
  const chart = echarts.getInstanceByDom(target);
  if (!chart) return;
  const data = JSON.parse(target.dataset.knowledgeGraph);
  const kind = panel.dataset.selectedKind;
  const related = panel.dataset.selectedRelated;
  const nodeID = kind === "knowledge" ? "k:" + panel.dataset.selectedId : kind === "topic" ? "t:" + panel.dataset.selectedId : "";
  const selectedEdge = kind === "relation" || related !== "0" ? data.edges.find(edge => {
    const query = new URL(edge.url, window.location.origin).searchParams;
    return query.get("kind") === kind && query.get("id") === panel.dataset.selectedId && (related === "0" || query.get("related") === related);
  }) : null;
  const visible = new Set(selectedEdge ? [selectedEdge.source, selectedEdge.target] : [nodeID]);
  for (const edge of data.edges) {
    if (edge.source === nodeID) visible.add(edge.target);
    if (edge.target === nodeID) visible.add(edge.source);
  }
  chart.setOption({ series: [{ links: data.edges.map(edge => ({ ...edge, lineStyle: { ...edge.lineStyle, width: edge === selectedEdge ? 4 : 2 } })), data: data.nodes.map(node => ({
    ...node, label: { show: visible.has(node.id) },
    itemStyle: { ...node.itemStyle, borderColor: node.id === nodeID ? "#e85d75" : node.itemStyle?.color, borderWidth: node.id === nodeID ? 4 : 2 }
  })) }] });
}

function syncGraphSelectionURL() {
  const panel = document.querySelector("#knowledge-graph-panel [data-selected-kind]");
  const target = document.querySelector("[data-graph-panel]");
  if (!panel || !target || !panel.dataset.selectedKind || panel.dataset.selectedId === "0") return;
  const url = new URL(window.location.href);
  url.searchParams.set("view", "graph");
  url.searchParams.set("selected_kind", panel.dataset.selectedKind);
  url.searchParams.set("selected_id", panel.dataset.selectedId);
  if (panel.dataset.selectedRelated === "0") url.searchParams.delete("selected_related");
  else url.searchParams.set("selected_related", panel.dataset.selectedRelated);
  window.history.replaceState({}, "", url);
  target.dataset.graphReturn = url.pathname + url.search;
}

document.addEventListener("htmx:afterSwap", (event) => {
  if (event.detail.target?.id === "knowledge-graph-panel") {
    syncGraphSelection();
    syncGraphSelectionURL();
  }
});

document.addEventListener("click", (event) => {
  const control = event.target instanceof Element ? event.target.closest("[data-graph-command]") : null;
  if (!(control instanceof HTMLElement)) return;
  const host = control.closest(".admin-graph-surface");
  const chartNode = host?.querySelector("[data-knowledge-graph]");
  const chart = chartNode ? echarts.getInstanceByDom(chartNode) : null;
  if (!chart || !(chartNode instanceof HTMLElement)) return;
  const current = Number(chartNode.dataset.graphZoom || 1);
  const command = control.dataset.graphCommand;
  const zoom = command === "zoom-in" ? Math.min(2, current + 0.2) : command === "zoom-out" ? Math.max(0.6, current - 0.2) : 1;
  chartNode.dataset.graphZoom = String(zoom);
  chart.setOption({ series: [{ zoom, center: ["50%", "50%"] }] });
});

document.addEventListener("htmx:beforeCleanupElement", (event) => {
  const element = event.detail.elt;
  if (element?.matches?.("[data-knowledge-graph]")) echarts.getInstanceByDom(element)?.dispose();
});
document.addEventListener("htmx:afterSwap", renderKnowledgeGraph);
window.addEventListener("resize", () => document.querySelectorAll("[data-knowledge-graph]").forEach((node) => echarts.getInstanceByDom(node)?.resize()));

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

document.addEventListener("htmx:afterRequest", (event) => resetFormSubmitting(formFromEvent(event)));
document.addEventListener("htmx:responseError", (event) => resetFormSubmitting(formFromEvent(event)));
document.addEventListener("htmx:sendError", (event) => resetFormSubmitting(formFromEvent(event)));
document.addEventListener("htmx:beforeSwap", prepareAdminLogSwap);
document.addEventListener("htmx:afterSwap", () => { syncAdminLogFormat(); followAdminLogs(); syncLogDownloadURL(); renderModelStats(); });
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
