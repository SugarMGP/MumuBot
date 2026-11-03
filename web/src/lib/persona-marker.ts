// 人格预设的作用域标记是存储协议的一部分，必须保持原样写入
export type PersonaMode = "group" | "private" | "common";

export const personaMarkerToken: Record<PersonaMode, string> = {
  group: "群",
  private: "私",
  common: "通用",
};

const tokenMode: Record<string, PersonaMode> = {
  "群": "group",
  私: "private",
  通用: "common",
};

export const personaMarkerPattern = /^\[\s*(群|私|通用)\s*\]\s*([\s\S]*)$/;

export function parsePersonaMode(token: string): PersonaMode {
  return tokenMode[token] ?? "common";
}
