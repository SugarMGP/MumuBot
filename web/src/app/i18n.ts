import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import zhCN from "@/locales/zh-CN";
import enUS from "@/locales/en-US";

export const localeStorageKey = "mumu.locale";
export type AppLocale = "zh-CN" | "en-US";

export function isAppLocale(value: unknown): value is AppLocale {
  return value === "zh-CN" || value === "en-US";
}

export function detectLocale(): AppLocale {
  const stored = localStorage.getItem(localeStorageKey);
  if (isAppLocale(stored)) return stored;
  const language =
    (navigator.languages && navigator.languages[0]) ||
    navigator.language ||
    "zh-CN";
  return language.slice(0, 2).toLowerCase() === "zh" ? "zh-CN" : "en-US";
}

function applyDocumentLanguage(locale: string) {
  document.documentElement.lang = locale;
}

void i18n.use(initReactI18next).init({
  resources: {
    "zh-CN": { translation: zhCN },
    "en-US": { translation: enUS },
  },
  lng: detectLocale(),
  fallbackLng: "zh-CN",
  interpolation: { escapeValue: false },
  returnNull: false,
});

i18n.on("languageChanged", (locale) => {
  applyDocumentLanguage(locale);
  localStorage.setItem(localeStorageKey, locale);
});

applyDocumentLanguage(i18n.language);

export function setAppLocale(locale: AppLocale) {
  void i18n.changeLanguage(locale);
}

export default i18n;
