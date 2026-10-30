import { toggleTheme, useTheme } from "../lib/theme";

export function ThemeButton({ className = "" }: { className?: string }) {
  const theme = useTheme();
  return (
    <button
      type="button"
      className={`icon-btn ${className}`.trim()}
      aria-label={theme === "dark" ? "切换到亮色主题" : "切换到暗色主题"}
      title={theme === "dark" ? "切换到亮色主题" : "切换到暗色主题"}
      onClick={() => toggleTheme()}
    >
      {theme === "dark" ? (
        <svg className="i-sun" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" strokeLinecap="round" />
        </svg>
      ) : (
        <svg className="i-moon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
          <path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z" strokeLinejoin="round" />
        </svg>
      )}
    </button>
  );
}
