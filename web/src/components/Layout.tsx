import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation } from "react-router-dom";
import { api } from "../api/client";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { useLive } from "../hooks/useLive";
import { useTheme } from "../lib/theme";
import { resolveTimezone } from "../lib/settings";
import { ThemeButton } from "./ThemeButton";

const navItems = [
  { to: "/", label: "总览", end: true, icon: "grid" },
  { to: "/logs", label: "日志", end: false, icon: "list" },
  { to: "/analysis", label: "分析", end: false, icon: "chart" },
  { to: "/settings", label: "设置", end: false, icon: "gear" },
] as const;

const liveLabels = { sse: "LIVE", poll: "POLL", down: "OFFLINE" } as const;

function NavIcon({ name }: { name: (typeof navItems)[number]["icon"] }) {
  if (name === "grid") {
    return (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
        <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" />
        <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
        <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" />
        <rect x="13.5" y="13.5" width="7" height="7" rx="1.5" />
      </svg>
    );
  }
  if (name === "list") {
    return (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" aria-hidden="true">
        <path d="M4 6h16M4 12h16M4 18h10" />
      </svg>
    );
  }
  if (name === "chart") {
    return (
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" aria-hidden="true">
        <path d="M4 20V10M10 20V4M16 20v-7M22 20H2" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" aria-hidden="true">
      <circle cx="12" cy="12" r="3.2" />
      <path d="M19.4 15a1.7 1.7 0 0 0 .34 1.87l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06A1.7 1.7 0 0 0 15 19.36a1.7 1.7 0 0 0-1 1.55V21a2 2 0 1 1-4 0v-.09a1.7 1.7 0 0 0-1.1-1.55 1.7 1.7 0 0 0-1.87.34l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.7 1.7 0 0 0 4.64 15a1.7 1.7 0 0 0-1.55-1H3a2 2 0 1 1 0-4h.09A1.7 1.7 0 0 0 4.64 9a1.7 1.7 0 0 0-.34-1.87l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.7 1.7 0 0 0 9 4.64a1.7 1.7 0 0 0 1-1.55V3a2 2 0 1 1 4 0v.09a1.7 1.7 0 0 0 1 1.55 1.7 1.7 0 0 0 1.87-.34l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.7 1.7 0 0 0 19.36 9c.22.6.8 1 1.55 1H21a2 2 0 1 1 0 4h-.09a1.7 1.7 0 0 0-1.51 1Z" />
    </svg>
  );
}

function useClock(timeZone: string | undefined): string {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  try {
    return new Intl.DateTimeFormat("en-GB", {
      timeZone,
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    }).format(now);
  } catch {
    return new Date().toLocaleTimeString("en-GB", { hour12: false });
  }
}

/** Layout is the desktop sidebar and mobile top bar shared by every app page. */
export function Layout() {
  const bootstrap = useQuery({ queryKey: ["bootstrap"], queryFn: api.bootstrap });
  const { mode } = useLive();
  const theme = useTheme();
  const location = useLocation();
  const clock = useClock(resolveTimezone(bootstrap.data?.timezone));
  const titles: Record<string, string> = {
    "/": "总览",
    "/logs": "日志",
    "/analysis": "分析",
    "/settings": "设置",
    "/settings/pricing": "价格",
    "/settings/data": "数据",
  };
  useDocumentTitle(titles[location.pathname] ?? "总览");

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="logo" aria-hidden="true" />
          <div className="brand-text">
            <span className="brand-name">llmmeter</span>
            <span className="brand-sub">usage deck</span>
          </div>
        </div>
        <div className="side-status">
          <span className="live" role="status" aria-live="polite">
            <span className="dot" aria-hidden="true" />
            <span>{liveLabels[mode]}</span>
          </span>
          <ThemeButton />
        </div>
        <nav className="side-nav" aria-label="主导航">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) => `side-link${isActive ? " active" : ""}`}
            >
              <NavIcon name={item.icon} />
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="side-foot">
          <div className="foot-meta">
            <span>{bootstrap.data?.timezone ?? "…"}</span>
            <span>v{bootstrap.data?.version ?? "…"}</span>
          </div>
          <span className="clock" aria-label={theme === "dark" ? "暗色主题" : "亮色主题"}>
            {clock}
          </span>
        </div>
      </aside>
      <div className="main">
        <main className="page">
          <Outlet />
          <footer className="footer">
            <span><b>本机运行</b> · SQLite 单文件 · 数据不出本机</span>
            <span>llmmeter usage deck · {clock}</span>
          </footer>
        </main>
      </div>
    </div>
  );
}
