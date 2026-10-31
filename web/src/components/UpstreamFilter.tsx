import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";

/** UpstreamFilter scopes a page to one upstream; empty means all upstreams. */
export function UpstreamFilter({
  value,
  onChange,
  ariaLabel = "上游",
}: {
  value: string;
  onChange: (value: string) => void;
  ariaLabel?: string;
}) {
  const upstreams = useQuery({ queryKey: ["upstreams"], queryFn: api.getUpstreams });
  return (
    <label className="filter page-filter">
      <span>{ariaLabel}</span>
      <select value={value} onChange={(event) => onChange(event.target.value)}>
        <option value="">全部上游</option>
        {upstreams.data?.upstreams.map((upstream) => (
          <option key={upstream.id} value={upstream.id}>
            {upstream.name}
          </option>
        ))}
      </select>
    </label>
  );
}
