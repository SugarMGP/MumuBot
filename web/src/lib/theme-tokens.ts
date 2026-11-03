import type { ResolvedTheme } from "@/app/theme-provider";

export interface ChartPalette {
  text: string;
  axis: string;
  split: string;
  colors: string[];
  tooltipBg: string;
  tooltipText: string;
}

export function chartPalette(theme: ResolvedTheme): ChartPalette {
  if (theme === "dark") {
    return {
      text: "#a3a3b3",
      axis: "#3a3a46",
      split: "rgba(255,255,255,0.08)",
      colors: ["#ef7288", "#35b3a5", "#a294dd", "#e0b04a", "#7ba3e8", "#6fd6c9"],
      tooltipBg: "#23232c",
      tooltipText: "#ececf2",
    };
  }
  return {
    text: "#666673",
    axis: "#dce1ea",
    split: "rgba(32,33,45,0.08)",
    colors: ["#e85d75", "#159a8c", "#8b7bc7", "#d59a22", "#5b87d8", "#62c8bb"],
    tooltipBg: "#ffffff",
    tooltipText: "#20212d",
  };
}
