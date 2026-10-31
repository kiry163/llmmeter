import type { ReactNode } from "react";
import { ThemeButton } from "./ThemeButton";

export interface AuthLayoutProps {
  eyebrow: string;
  heading: ReactNode;
  lead: string;
  stats: { value: string; label: string }[];
  brandFoot?: string;
  children: ReactNode;
}

/** AuthLayout is the split-screen brand shell for the login page. */
export function AuthLayout({
  eyebrow,
  heading,
  lead,
  stats,
  brandFoot = "llmmeter · local first",
  children,
}: AuthLayoutProps) {
  return (
    <div className="auth-shell">
      <section className="auth-view active">
        <div className="auth-brand">
          <div className="brand">
            <span className="logo" aria-hidden="true" />
            <div className="brand-text">
              <span className="brand-name">llmmeter</span>
              <span className="brand-sub">usage deck</span>
            </div>
          </div>
          <div className="auth-hero">
            <div className="page-eyebrow">{eyebrow}</div>
            <h1>{heading}</h1>
            <p>{lead}</p>
            <div className="auth-stats">
              {stats.map((stat) => (
                <div key={stat.label}>
                  <b>{stat.value}</b>
                  <span>{stat.label}</span>
                </div>
              ))}
            </div>
          </div>
          <div className="auth-brand-foot">{brandFoot}</div>
        </div>
        <div className="auth-panel">
          <div className="auth-toolbar">
            <span className="auth-back">本机管理端</span>
            <span className="auth-word">llmmeter</span>
            <ThemeButton />
          </div>
          {children}
        </div>
      </section>
    </div>
  );
}
