export function Sparkline({
  values,
  color = "var(--c1)",
  label,
}: {
  values: number[];
  color?: string;
  label?: string;
}) {
  let min = Math.min(...values);
  let max = Math.max(...values);
  if (!values.length) return <svg className="spark" viewBox="0 0 100 30" aria-hidden="true" />;
  if (max - min < 1e-9) {
    max = min + 1;
    min -= 1;
  }
  const width = 100;
  const height = 30;
  const top = 3;
  const bottom = 4;
  const points = values.map((value, index) => {
    const x = values.length === 1 ? width / 2 : 1 + (index / (values.length - 1)) * (width - 2);
    const y = top + (height - top - bottom) * (1 - (value - min) / (max - min));
    return [x, y] as const;
  });
  const line = points.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(2)},${y.toFixed(2)}`).join(" ");
  const area = `${line} L${points[points.length - 1][0].toFixed(2)},${height - bottom} L${points[0][0].toFixed(2)},${height - bottom} Z`;
  const last = points[points.length - 1];

  return (
    <svg
      className="spark"
      viewBox="0 0 100 30"
      preserveAspectRatio="none"
      role="img"
      aria-label={label}
    >
      <path d={area} fill={color} opacity="0.08" />
      <path
        d={line}
        fill="none"
        stroke={color}
        strokeWidth="1.4"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <circle cx={last[0]} cy={last[1]} r="2" fill={color} />
    </svg>
  );
}
