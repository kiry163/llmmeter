import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
} from "react";

type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";

const buttonClass: Record<ButtonVariant, string> = {
  primary: "btn btn-primary",
  secondary: "btn",
  ghost: "ghost-btn",
  danger: "btn btn-danger",
};

export function Button({
  variant = "primary",
  className = "",
  type = "button",
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant }) {
  return (
    <button
      type={type}
      className={`${buttonClass[variant]} ${className}`.trim()}
      {...rest}
    />
  );
}

export function Input({ className = "", ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={`ui-input ${className}`.trim()} {...rest} />;
}

export function Select({ className = "", ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return <select className={`ui-input ${className}`.trim()} {...rest} />;
}

/** Field labels one form control and renders an optional hint and error. */
export function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string;
  hint?: string;
  error?: string;
  children: ReactNode;
}) {
  return (
    <label className="form-field">
      <span>{label}</span>
      {children}
      {hint && <small>{hint}</small>}
      {error && (
        <small className="form-error" role="alert">
          {error}
        </small>
      )}
    </label>
  );
}

export function Card({
  title,
  actions,
  children,
  className = "",
}: {
  title?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`card ${className}`.trim()}>
      {(title || actions) && (
        <header className="card-head">
          {title && <div className="card-title">{title}</div>}
          {actions}
        </header>
      )}
      {children}
    </section>
  );
}

type BadgeTone = "neutral" | "info" | "success" | "warning" | "danger";

const badgeClass: Record<BadgeTone, string> = {
  neutral: "status neutral",
  info: "status info",
  success: "status ok",
  warning: "status warn",
  danger: "status err",
};

export function Badge({ tone = "neutral", children }: { tone?: BadgeTone; children: ReactNode }) {
  return <span className={badgeClass[tone]}>{children}</span>;
}

export function Spinner({ label = "加载中…" }: { label?: string }) {
  return (
    <div className="spinner" role="status">
      <span className="spinner-ring" aria-hidden="true" />
      {label}
    </div>
  );
}

export function ErrorText({ error }: { error: unknown }) {
  const message = error instanceof Error ? error.message : String(error);
  return (
    <p className="form-error" role="alert">
      {message}
    </p>
  );
}

export function Drawer({
  open,
  title,
  onClose,
  children,
}: {
  open: boolean;
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
}) {
  useEffect(() => {
    if (!open) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => {
      document.body.style.overflow = previous;
      window.removeEventListener("keydown", onKey);
    };
  }, [open, onClose]);

  return (
    <>
      <div
        className={`drawer-overlay${open ? " open" : ""}`}
        onClick={onClose}
        aria-hidden="true"
      />
      <aside className={`request-drawer${open ? " open" : ""}`} aria-hidden={!open}>
        <header className="drawer-head">
          <h2 className="drawer-title">{title}</h2>
          <button type="button" className="btn" onClick={onClose}>
            关闭
          </button>
        </header>
        <div className="drawer-body">{children}</div>
      </aside>
    </>
  );
}

type ToastTone = "info" | "success" | "error";

interface ToastItem {
  id: number;
  text: string;
  tone: ToastTone;
}

type ShowToast = (text: string, tone?: ToastTone) => void;

const ToastContext = createContext<ShowToast>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<ToastItem | null>(null);
  const nextId = useRef(1);
  const timer = useRef<number | undefined>(undefined);

  const showToast = useCallback<ShowToast>((text, tone = "info") => {
    const id = nextId.current++;
    setToast({ id, text, tone });
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      setToast((current) => (current?.id === id ? null : current));
    }, 3500);
  }, []);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  return (
    <ToastContext.Provider value={showToast}>
      {children}
      {toast && (
        <div
          className={`toast show${toast.tone === "error" ? " err" : ""}`}
          role="status"
        >
          {toast.text}
        </div>
      )}
    </ToastContext.Provider>
  );
}

export function useToast(): ShowToast {
  return useContext(ToastContext);
}
