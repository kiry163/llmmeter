import { expect, it } from "vitest";
import {
  buildRequestsQuery,
  parseRequestsFilters,
  serializeRequestsFilters,
} from "./query";

it("omits empty filters and keeps cursor", () => {
  expect(buildRequestsQuery({ from: "2026-10-29", to: "2026-10-29" })).toBe(
    "from=2026-10-29&to=2026-10-29&limit=50",
  );
  expect(buildRequestsQuery({ from: "a", to: "b", model: "m", beforeId: 42 })).toBe(
    "from=a&to=b&model=m&before_id=42&limit=50",
  );
});

it("keeps false booleans and drops blank strings", () => {
  expect(
    buildRequestsQuery({
      from: "2026-10-01",
      to: "2026-10-29",
      model: "",
      pathClass: "chat",
      status: 429,
      usageStatus: "no_usage",
      priced: false,
      beforeId: 7,
      limit: 20,
    }),
  ).toBe(
    "from=2026-10-01&to=2026-10-29&pathClass=chat&status=429&usageStatus=no_usage&priced=false&before_id=7&limit=20",
  );
  expect(buildRequestsQuery()).toBe("limit=50");
});

it("encodes stream filter", () => {
  expect(buildRequestsQuery({ from: "a", to: "b", stream: "stream" })).toContain("stream=true");
  expect(buildRequestsQuery({ from: "a", to: "b", stream: "nonstream" })).toContain(
    "stream=false",
  );
  expect(buildRequestsQuery({ from: "a", to: "b", stream: "all" })).not.toContain("stream=");
});

it("parses defaults and rejects unknown option values", () => {
  const defaults = parseRequestsFilters(new URLSearchParams());
  expect(defaults.preset).toBe("7d");
  expect(defaults.from <= defaults.to).toBe(true);
  expect(defaults.model).toBe("");
  expect(defaults.pathClass).toBe("");
  expect(defaults.status).toBe("");
  expect(defaults.usageStatus).toBe("");
  expect(defaults.priced).toBe("all");
  expect(defaults.stream).toBe("all");

  const parsed = parseRequestsFilters(
    new URLSearchParams(
      "range=custom&from=2026-10-01&to=2026-10-02&model=deepseek-chat&pathClass=audio&status=200&usageStatus=ok&priced=unpriced&stream=nonstream",
    ),
  );
  expect(parsed).toMatchObject({
    preset: "custom",
    from: "2026-10-01",
    to: "2026-10-02",
    model: "deepseek-chat",
    pathClass: "audio",
    status: "200",
    usageStatus: "ok",
    priced: "unpriced",
    stream: "nonstream",
  });

  const invalid = parseRequestsFilters(
    new URLSearchParams(
      "range=custom&from=bad&to=2026-10-02&pathClass=bogus&status=abc&usageStatus=nope&priced=maybe&stream=bogus",
    ),
  );
  expect(invalid.preset).toBe("7d");
  expect(invalid.from <= invalid.to).toBe(true);
  expect(invalid.pathClass).toBe("");
  expect(invalid.status).toBe("");
  expect(invalid.usageStatus).toBe("");
  expect(invalid.priced).toBe("all");
  expect(invalid.stream).toBe("all");
});

it("serializes only active filters and round-trips", () => {
  const defaults = parseRequestsFilters(new URLSearchParams());
  expect(serializeRequestsFilters(defaults).toString()).toBe("range=7d");

  const filters = {
    ...defaults,
    preset: "custom" as const,
    from: "2026-10-01",
    to: "2026-10-02",
    model: "m",
    pathClass: "chat" as const,
    status: "500",
    usageStatus: "error" as const,
    priced: "priced" as const,
    stream: "stream" as const,
  };
  const params = serializeRequestsFilters(filters);
  expect([...params.keys()]).toEqual([
    "range",
    "from",
    "to",
    "model",
    "pathClass",
    "status",
    "usageStatus",
    "stream",
    "priced",
  ]);
  expect(parseRequestsFilters(params)).toEqual(filters);
});

it("omits the stream parameter when all traffic is selected", () => {
  const filters = { ...parseRequestsFilters(new URLSearchParams()), stream: "all" as const };
  expect(serializeRequestsFilters(filters).toString()).toBe("range=7d");
});
