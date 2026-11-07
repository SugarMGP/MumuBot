import * as echarts from "echarts/core";
import { BarChart, LineChart, PieChart } from "echarts/charts";
import {
  GridComponent,
  LegendComponent,
  TitleComponent,
  TooltipComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import type { EChartsCoreOption } from "echarts/core";
import { useReducedMotion } from "motion/react";
import { useEffect, useRef } from "react";

echarts.use([
  BarChart,
  LineChart,
  PieChart,
  GridComponent,
  LegendComponent,
  TitleComponent,
  TooltipComponent,
  CanvasRenderer,
]);

export function Chart({
  option,
  className,
}: {
  option: EChartsCoreOption;
  className?: string;
}) {
  const reducedMotion = useReducedMotion();
  const container = useRef<HTMLDivElement>(null);
  const chart = useRef<ReturnType<typeof echarts.init> | null>(null);

  useEffect(() => {
    const element = container.current;
    if (!element) return;
    const instance = echarts.init(element);
    chart.current = instance;
    const observer = new ResizeObserver(() => instance.resize());
    observer.observe(element);
    return () => {
      observer.disconnect();
      instance.dispose();
      chart.current = null;
    };
  }, []);

  useEffect(() => {
    chart.current?.setOption(
      { ...option, animation: !reducedMotion },
      { notMerge: true, lazyUpdate: true },
    );
  }, [option, reducedMotion]);

  return <div ref={container} className={className} />;
}
