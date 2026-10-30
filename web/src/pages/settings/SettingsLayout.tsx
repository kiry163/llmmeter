import { NavLink, Outlet } from "react-router-dom";
import { PageHeader } from "../../components/PageHeader";

const tabs = [
  { to: "/settings", label: "上游与访问", end: true },
  { to: "/settings/pricing", label: "价格", end: false },
  { to: "/settings/data", label: "数据", end: false },
];

/** SettingsLayout is the settings page header plus sub-navigation. */
export function SettingsLayout() {
  return (
    <>
      <PageHeader eyebrow="settings" title="设置" subtitle="上游、价格与运行数据" />
      <nav className="settings-tabs" aria-label="设置分区">
        {tabs.map((tab) => (
          <NavLink
            key={tab.to}
            to={tab.to}
            end={tab.end}
            className={({ isActive }) => (isActive ? "active" : "")}
          >
            {tab.label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </>
  );
}
