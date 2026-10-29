import { useQuery } from "@tanstack/react-query";
import { NavLink, Outlet } from "react-router-dom";
import { api } from "../api/client";

const navItems = [
  { to: "/", label: "总览", end: true },
  { to: "/requests", label: "请求", end: false },
  { to: "/pricing", label: "价格", end: false },
  { to: "/settings", label: "设置", end: false },
];

function navClass({ isActive }: { isActive: boolean }): string {
  const base =
    "rounded-md px-3 py-2 text-sm font-medium transition-colors min-w-16 text-center";
  return isActive ? `${base} bg-accent/15 text-accent` : `${base} text-muted hover:text-ink`;
}

export function Layout() {
  const bootstrap = useQuery({ queryKey: ["bootstrap"], queryFn: api.bootstrap });

  return (
    <div className="min-h-screen bg-surface text-ink md:flex">
      <aside className="sticky top-0 z-20 border-b border-line bg-surface-raised/90 backdrop-blur md:h-screen md:w-52 md:shrink-0 md:border-b-0 md:border-r">
        <div className="flex items-center justify-between px-4 py-3 md:block">
          <div>
            <div className="text-base font-semibold tracking-tight">llmmeter</div>
            <div className="text-xs text-muted">LLM 用量计量</div>
          </div>
          <div className="flex items-center gap-2 text-xs text-muted">
            <span
              className="inline-block h-2 w-2 rounded-full bg-warning"
              title="实时连接（Task 14 接入 SSE 后显示真实状态）"
            />
            未连接
          </div>
        </div>
        <nav className="flex gap-1 overflow-x-auto px-2 pb-2 md:mt-2 md:flex-col md:px-2">
          {navItems.map((item) => (
            <NavLink key={item.to} to={item.to} end={item.end} className={navClass}>
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="hidden border-t border-line px-4 py-3 text-xs text-muted md:block">
          <div>时区 {bootstrap.data?.timezone ?? "…"}</div>
          <div>版本 {bootstrap.data?.version ?? "…"}</div>
        </div>
      </aside>
      <main className="min-w-0 flex-1 p-4 pb-24 md:p-6 md:pb-6">
        <Outlet />
      </main>
    </div>
  );
}
