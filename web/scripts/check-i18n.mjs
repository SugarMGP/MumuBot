#!/usr/bin/env bun
// 界面文案只允许出现在 src/locales 下；本脚本扫描其余源码中的中文字符
// 注释会被剥离，用户数据与日志内容不经过这里
import { readdir, readFile } from "node:fs/promises";
import { extname, join, relative } from "node:path";

const root = new URL("..", import.meta.url).pathname;
const sourceRoot = join(root, "src");
const allowedDirs = [join(sourceRoot, "locales"), join(sourceRoot, "mocks")];
const allowedFiles = new Set([join(sourceRoot, "lib", "persona-marker.ts")]);

const extensions = new Set([".ts", ".tsx"]);
const violations = [];

function stripComments(text) {
  let output = "";
  let index = 0;
  let mode = "code";
  while (index < text.length) {
    const two = text.slice(index, index + 2);
    if (mode === "code" && two === "//") {
      mode = "line";
      index += 2;
      continue;
    }
    if (mode === "code" && two === "/*") {
      mode = "block";
      index += 2;
      continue;
    }
    if (mode === "line") {
      if (text[index] === "\n") {
        mode = "code";
        output += "\n";
      }
      index += 1;
      continue;
    }
    if (mode === "block") {
      if (two === "*/") {
        mode = "code";
        index += 2;
        continue;
      }
      index += 1;
      continue;
    }
    if (text[index] === '"' || text[index] === "'" || text[index] === "`") {
      const quote = text[index];
      output += text[index];
      index += 1;
      while (index < text.length) {
        output += text[index];
        if (text[index] === "\\") {
          index += 1;
          if (index < text.length) output += text[index];
          index += 1;
          continue;
        }
        if (text[index] === quote) {
          index += 1;
          break;
        }
        index += 1;
      }
      continue;
    }
    output += text[index];
    index += 1;
  }
  return output;
}

async function walk(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (allowedDirs.includes(full)) continue;
      await walk(full);
      continue;
    }
    if (!extensions.has(extname(entry.name))) continue;
    if (allowedFiles.has(full)) continue;
    const text = stripComments(await readFile(full, "utf8"));
    const lines = text.split("\n");
    lines.forEach((line, index) => {
      if (/[\u4e00-\u9fff]/.test(line)) {
        violations.push(`${relative(root, full)}:${index + 1}: ${line.trim()}`);
      }
    });
  }
}

await walk(sourceRoot);

if (violations.length > 0) {
  console.error("发现硬编码中文文案，请移动到 src/locales：");
  for (const item of violations) console.error("  " + item);
  process.exit(1);
}
console.log("i18n 检查通过：源码中没有硬编码中文文案");
