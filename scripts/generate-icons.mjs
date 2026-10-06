#!/usr/bin/env bun
// 从 lucide-static 官方包生成 internal/web/views/icons.templ 的图标节点
// 用法：bun run gen:icons && templ generate ./internal/web/views
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const nodes = JSON.parse(readFileSync(join(root, "node_modules/lucide-static/icon-nodes.json"), "utf8"));

// AdminIcon 名称 → 官方 Lucide 图标名；新增图标必须在此登记，禁止手绘
const mapping = [
  [["overview"], "layout-dashboard"],
  [["style-cards"], "swatch-book"],
  [["topics"], "network"],
  [["jargons"], "message-square-text"],
  [["stickers"], "sticker"],
  [["memories", "memory-metric"], "book-open-text"],
  [["members", "member-metric"], "users-round"],
  [["group-config", "groups"], "users"],
  [["sessions"], "messages-square"],
  [["system"], "activity"],
  [["sort"], "arrow-up-down"],
  [["filter"], "list-filter"],
  [["sort-desc"], "arrow-down-wide-narrow"],
  [["sort-asc"], "arrow-up-narrow-wide"],
  [["connection"], "plug-zap"],
  [["learning", "style-metric"], "sparkles"],
  [["tools"], "wrench"],
  [["runtime-summary"], "chart-column"],
  [["mood"], "heart"],
  [["persona"], "user-round-cog"],
  [["behavior"], "gauge"],
  [["model"], "cpu"],
  [["storage"], "database"],
  [["backend"], "shield-check"],
  [["flash-success"], "circle-check"],
  [["flash-warn"], "triangle-alert"],
  [["flash-error"], "circle-x"],
  [["chevron-left"], "chevron-left"],
  [["chevron-right"], "chevron-right"],
  [["chevron-down"], "chevron-down"],
  [["x"], "x"],
  [["log-out"], "log-out"],
  [["arrow-right"], "arrow-right"],
  [["pencil"], "pencil"],
  [["lock"], "lock"],
];

function render(name) {
  const icon = nodes[name];
  if (!icon) throw new Error("lucide-static 缺少图标: " + name);
  return icon
    .map(([tag, attrs]) =>
      "\t\t\t<" + tag + " " + Object.entries(attrs).map(([k, v]) => k + '="' + v + '"').join(" ") + "></" + tag + ">")
    .join("\n");
}

let out = `package views

// 本文件由 scripts/generate-icons.mjs 依据 lucide-static 官方包生成，请勿手工修改；
// 需要新增图标时先在生成脚本的 mapping 中登记，再执行 bun run gen:icons && templ generate ./internal/web/views
templ AdminIcon(name string, className string, marker string) {
`;
let first = true;
for (const [names, lucideName] of mapping) {
  const cond = names.map((n) => 'name == "' + n + '"').join(" || ");
  out += (first ? "\tif " : "\t} else if ") + cond + " {\n";
  first = false;
  out += '\t\t<svg data-admin-icon={ marker } class={ className } viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">\n';
  out += render(lucideName) + "\n";
  out += "\t\t</svg>\n";
}
out += '\t} else {\n\t\t<svg data-admin-icon={ marker } class={ className } viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">\n';
out += render("circle-question-mark") + "\n";
out += "\t\t</svg>\n\t}\n}\n";

writeFileSync(join(root, "internal/web/views/icons.templ"), out, "utf8");

// 前端脚本使用的图标同样来自 lucide-static，避免在 JS 里手写 SVG 路径
const jsIcons = { "flash-success": "circle-check", "flash-warn": "triangle-alert", "flash-error": "circle-x", "x": "x" };
function renderSvg(name, className) {
  const icon = nodes[name];
  if (!icon) throw new Error("lucide-static 缺少图标: " + name);
  const body = icon
    .map(([tag, attrs]) => "<" + tag + " " + Object.entries(attrs).map(([k, v]) => k + '="' + v + '"').join(" ") + "></" + tag + ">")
    .join("");
  return '<svg class="' + className + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + body + "</svg>";
}
let jsOut = "// 本文件由 scripts/generate-icons.mjs 依据 lucide-static 官方包生成，请勿手工修改\n";
jsOut += "export const adminIconSvg = {\n";
for (const [key, lucideName] of Object.entries(jsIcons)) {
  jsOut += "  " + JSON.stringify(key) + ": " + JSON.stringify(renderSvg(lucideName, "size-4")) + ",\n";
}
jsOut += "};\n";
writeFileSync(join(root, "internal/web/assets/src/icons.generated.js"), jsOut, "utf8");

console.log("icons.templ 与 icons.generated.js 已由 lucide-static 生成，共 " + mapping.length + " 组图标");
