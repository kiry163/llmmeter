import {
  MutationCache,
  QueryCache,
  QueryClient,
  QueryClientProvider,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, type ReactNode } from "react";
import {
  BrowserRouter,
  Navigate,
  Outlet,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";
import { UnauthorizedError, api } from "./api/client";
import type { Me } from "./api/types";
import { Layout } from "./components/Layout";
import { Button, ErrorText, Spinner, ToastProvider } from "./components/ui";
import {
  UNAUTHORIZED_EVENT,
  loggedOutMe,
  loginPathFor,
  notifyUnauthorized,
  safeNext,
} from "./lib/auth";
import { Analysis } from "./pages/Analysis";
import { Logs } from "./pages/Logs";
import { Login } from "./pages/Login";
import { Overview } from "./pages/Overview";
import { Setup } from "./pages/Setup";
import { Data } from "./pages/settings/Data";
import { Gateway } from "./pages/settings/Gateway";
import { Pricing } from "./pages/settings/Pricing";
import { SettingsLayout } from "./pages/settings/SettingsLayout";

const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError: notifyUnauthorized }),
  mutationCache: new MutationCache({ onError: notifyUnauthorized }),
  defaultOptions: {
    queries: {
      // A missing session is terminal; retrying only delays the login redirect.
      retry: (failureCount, error) =>
        !(error instanceof UnauthorizedError) && failureCount < 1,
      refetchOnWindowFocus: false,
      staleTime: 5_000,
    },
  },
});

function FullPage({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-surface p-6 text-ink">
      {children}
    </div>
  );
}

function LoadFailure({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return (
    <FullPage>
      <div className="w-full max-w-sm space-y-3">
        <h1 className="text-lg font-semibold">无法连接服务</h1>
        <ErrorText error={error} />
        <Button onClick={onRetry}>重试</Button>
      </div>
    </FullPage>
  );
}

/**
 * AuthGate redirects unauthenticated viewers to the login page, preserving
 * the current internal route as `next`.
 */
function AuthGate() {
  const location = useLocation();
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });

  if (me.isPending) {
    return (
      <FullPage>
        <Spinner label="检查登录状态…" />
      </FullPage>
    );
  }
  if (me.isError) {
    return <LoadFailure error={me.error} onRetry={() => void me.refetch()} />;
  }

  const authenticated = me.data.authenticated;
  if (authenticated) {
    if (location.pathname === "/login") {
      const next = safeNext(new URLSearchParams(location.search).get("next"));
      return <Navigate to={next} replace />;
    }
    return <Outlet />;
  }

  if (location.pathname === "/login") return <Outlet />;
  const next = `${location.pathname}${location.search}`;
  return <Navigate to={`/login?next=${encodeURIComponent(next)}`} replace />;
}

/**
 * LocalOnlyGate covers deployments without an admin password: the API only
 * trusts loopback requests, so remote viewers get an explanation instead of a
 * login page they could never pass.
 */
function LocalOnlyGate() {
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });

  if (me.isPending) {
    return (
      <FullPage>
        <Spinner label="检查访问权限…" />
      </FullPage>
    );
  }
  if (me.isError) {
    return <LoadFailure error={me.error} onRetry={() => void me.refetch()} />;
  }
  if (me.data.authenticated) return <Outlet />;

  return (
    <FullPage>
      <div className="w-full max-w-sm space-y-2">
        <h1 className="text-lg font-semibold">仅限本机访问</h1>
        <p className="text-sm text-muted">
          当前未设置管理密码，llmmeter 只允许从运行服务的机器访问。请在服务器本机打开此页面。
        </p>
      </div>
    </FullPage>
  );
}

/**
 * AppGuard enforces the first-run setup and the login requirement before any
 * application route renders.
 */
function AppGuard() {
  const location = useLocation();
  const bootstrap = useQuery({ queryKey: ["bootstrap"], queryFn: api.bootstrap });
  const isSetup = location.pathname === "/setup";
  const isLogin = location.pathname === "/login";

  if (bootstrap.isPending) {
    return (
      <FullPage>
        <Spinner label="加载配置…" />
      </FullPage>
    );
  }
  if (bootstrap.isError) {
    return <LoadFailure error={bootstrap.error} onRetry={() => void bootstrap.refetch()} />;
  }

  if (bootstrap.data.needsSetup) {
    return isSetup ? <Outlet /> : <Navigate to="/setup" replace />;
  }
  if (isSetup) return <Navigate to="/" replace />;
  if (!bootstrap.data.authRequired) {
    return isLogin ? <Navigate to="/" replace /> : <LocalOnlyGate />;
  }
  return <AuthGate />;
}

/** UnauthorizedRedirect catches expired sessions from any protected call. */
function UnauthorizedRedirect() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  useEffect(() => {
    const onUnauthorized = () => {
      const { pathname, search } = window.location;
      const target = loginPathFor(pathname, search);
      if (!target) return;
      // AuthGate reads this cache; leaving authenticated:true would bounce
      // /login straight back to the protected route that just failed.
      queryClient.setQueryData<Me>(["me"], loggedOutMe);
      navigate(target, { replace: true });
    };
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
  }, [navigate, queryClient]);

  return null;
}

/**
 * RedirectKeepSearch moves an old route to its replacement without dropping
 * the query string (range and filter state survive the migration).
 */
function RedirectKeepSearch({ to }: { to: string }) {
  const { search, hash } = useLocation();
  return <Navigate to={{ pathname: to, search, hash }} replace />;
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <BrowserRouter>
          <UnauthorizedRedirect />
          <Routes>
            <Route element={<AppGuard />}>
              <Route path="setup" element={<Setup />} />
              <Route path="login" element={<Login />} />
              <Route element={<Layout />}>
                <Route index element={<Overview />} />
                <Route path="logs" element={<Logs />} />
                <Route path="analysis" element={<Analysis />} />
                <Route path="requests" element={<RedirectKeepSearch to="/logs" />} />
                <Route path="pricing" element={<RedirectKeepSearch to="/settings/pricing" />} />
                <Route path="settings" element={<SettingsLayout />}>
                  <Route index element={<Gateway />} />
                  <Route path="pricing" element={<Pricing />} />
                  <Route path="data" element={<Data />} />
                </Route>
              </Route>
            </Route>
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
  );
}
