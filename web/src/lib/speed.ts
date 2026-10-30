/**
 * tokensPerSecond measures stream generation speed: completion tokens over the
 * time between the first token and completion. Non-stream calls, missing TTFT,
 * and requests that completed before the first token have no rate.
 */
export function tokensPerSecond(
  completionTokens: number,
  latencyMs: number,
  ttftMs: number | null,
  stream: boolean,
): number | null {
  if (!stream || ttftMs === null || latencyMs <= 0 || completionTokens <= 0) return null;
  if (latencyMs <= ttftMs) return null;
  const generationMs = Math.max(latencyMs - ttftMs, 1);
  return (completionTokens * 1000) / generationMs;
}
