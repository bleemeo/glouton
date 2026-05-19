import { Area, AreaChart, ResponsiveContainer, YAxis } from "recharts";

type Sample = { t: number; v: number };

type Props = {
  data: Sample[];
  color: string;
  height?: number;
  yMax?: number;
};

/**
 * Sparkline renders a chart-less area for compact "trend over the
 * last N points" visuals — no axes, no grid, no tooltip. Used in
 * drawer cards where the focus is the current value plus a sense
 * of motion.
 */
export function Sparkline({ data, color, height = 56, yMax }: Props) {
  if (data.length === 0) {
    return null;
  }

  const id = `sparkline-${color}`;

  return (
    <ResponsiveContainer width="100%" height={height}>
      <AreaChart data={data} margin={{ top: 2, right: 0, bottom: 0, left: 0 }}>
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity={0.5} />
            <stop offset="100%" stopColor={color} stopOpacity={0.04} />
          </linearGradient>
        </defs>
        <YAxis hide domain={[0, yMax ?? "auto"]} />
        <Area
          type="monotone"
          dataKey="v"
          stroke={color}
          fill={`url(#${id})`}
          strokeWidth={1.5}
          dot={false}
          isAnimationActive={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}
