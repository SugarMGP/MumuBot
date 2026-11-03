import type { TFunction } from "i18next";

export function enumLabel(
  t: TFunction,
  group: string,
  value: string,
): string {
  if (!value) return t("common.none");
  return t(`enums.${group}.${value}`, { defaultValue: value });
}

export function subjectText(
  t: TFunction,
  subjectType: string,
  subjectTarget: string,
): string {
  return t(`enums.subjectText.${subjectType}`, {
    defaultValue: subjectTarget,
    target: subjectTarget,
  });
}

export function conversationText(
  t: TFunction,
  kind: string,
  targetID: number,
): string {
  return t(`enums.conversationWithId.${kind}`, {
    defaultValue: String(targetID),
    id: targetID,
  });
}

export function conversationLabel(
  t: TFunction,
  kind: string,
  targetID: number,
  name: string,
): string {
  const trimmed = name.trim();
  if (!trimmed) return conversationText(t, kind, targetID);
  return t(`enums.conversationWithName.${kind}`, {
    defaultValue: String(targetID),
    id: targetID,
    name: trimmed,
  });
}

export function taskLabel(t: TFunction, key: string): string {
  if (!key) return t("common.none");
  return t(`system.stats.tasks.${key}`, { defaultValue: key });
}
