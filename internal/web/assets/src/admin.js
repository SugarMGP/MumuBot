import htmx from "htmx.org";
import Toastify from "toastify-js";

window.htmx = htmx;

const DEFAULT_TOAST_DELAY = 4200;

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
  const pageBody = document.getElementById("admin-page-body");
  if (pageBody) pageBody.classList.add("admin-page-enter");
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

document.addEventListener("htmx:afterRequest", (event) => resetFormSubmitting(formFromEvent(event)));
document.addEventListener("htmx:responseError", (event) => resetFormSubmitting(formFromEvent(event)));
document.addEventListener("htmx:sendError", (event) => resetFormSubmitting(formFromEvent(event)));
document.addEventListener("admin:toast", (event) => {
  const detail = normalizeToastDetail(event.detail?.value || event.detail);
  if (detail) showAdminToast(detail);
});
document.addEventListener("admin:action-dialog-close", () => closeDialog("#admin-action-dialog"));
