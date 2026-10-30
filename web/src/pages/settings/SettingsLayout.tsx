import { NavLink, Outlet } from "react-router-dom";
import { PageHeader } from "../../components/PageHeader";

const tabs = [
  { to: "/settings", label: "网关", end: true },
  { to: "/settings/pricing", label: "价格", end: false },
  { to: "/settings/data", label: "数据", end: false },
];

function tabClass({ isActive }: { isActive: boolean }): string {
  const base = "-mb-px whitespace-nowrap border-b-2 px-3 py-2 text-sm font-medium transition-colors";
  return isActive
    ? `${base} border-accent text-ink`
    : `${base} border-transparent text-muted hover:text-ink`;
}

/**
 * SettingsLayout is the settings shell: a page header, a sub-navigation that
 * scrolls horizontally on narrow screens, and the active subpage.
 */
export function SettingsLayout() {
  return (
    <div className="mx-auto w-full max-w-4xl space-y-4">
      <PageHeader
        title="设置"
        description="上游与访问、价格目录、数据与运行信息。"
      />
      <nav
        className="flex overflow-x-auto border-b border-line"
        aria-label="设置子导航"
      >
        {tabs.map((tab) => (
          <NavLink key={tab.to} to={tab.to} end={tab.end} className={tabClass}>
            {tab.label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}
