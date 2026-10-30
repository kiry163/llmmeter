import { useMemo } from "react";

function formatValue(value: number, decimals: number): string {
  const safe = Number.isFinite(value) ? value : 0;
  return safe.toLocaleString("en-US", {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  });
}

/**
 * Odometer rolls each decimal digit independently. It is deliberately small:
 * the API already returns the display value, so no tweening is needed.
 */
export function Odometer({
  value,
  decimals = 0,
  label,
}: {
  value: number;
  decimals?: number;
  label: string;
}) {
  const text = useMemo(() => formatValue(value, decimals), [value, decimals]);
  return (
    <span className="odo" aria-label={label} title={label}>
      {Array.from(text).map((char, index) => {
        if (char >= "0" && char <= "9") {
          const digit = Number(char);
          return (
            <span className="odo-digit" key={`${index}-${char}`} aria-hidden="true">
              <span
                className="odo-strip"
                style={{ transform: `translateY(-${digit * 1.12}em)` }}
              >
                {Array.from({ length: 10 }, (_, n) => (
                  <span key={n}>{n}</span>
                ))}
              </span>
            </span>
          );
        }
        return (
          <span className="odo-sep" key={`${index}-${char}`} aria-hidden="true">
            {char}
          </span>
        );
      })}
    </span>
  );
}
