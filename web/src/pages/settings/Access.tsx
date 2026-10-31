import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactElement } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, api } from "../../api/client";
import { Badge, Button, ErrorText, Field, Input, Select, Spinner, useToast } from "../../components/ui";
import { listenIsLoopback, timezoneOptions, validateRetention } from "../../lib/settings";

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

/** Access groups admin authentication and the general reporting settings. */
export function Access(): ReactElement {
  const queryClient = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const bootstrap = useQuery({ queryKey: ["bootstrap"], queryFn: api.bootstrap });
  const general = useQuery({ queryKey: ["general"], queryFn: api.getGeneral });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });

  const [timezone, setTimezone] = useState<string | null>(null);
  const [retention, setRetention] = useState<string | null>(null);
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  const saveGeneral = useMutation({
    mutationFn: () =>
      api.patchGeneral({
        timezone: timezone ?? undefined,
        retentionDays:
          retention === null ? undefined : Number.parseInt(retention.trim(), 10),
      }),
    onSuccess: (view) => {
      queryClient.setQueryData(["general"], view);
      void queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
      setTimezone(null);
      setRetention(null);
      toast("通用设置已保存", "success");
    },
    onError: (error) => toast(messageOf(error, "保存设置失败"), "error"),
  });

  const changePassword = useMutation({
    mutationFn: () => api.setPassword(current, next),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["me"] });
      void queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
      setCurrent("");
      setNext("");
      setConfirm("");
      setErrors({});
      toast(next === "" ? "管理密码已清除" : "管理密码已更新", "success");
    },
    onError: (error) => {
      if (error instanceof ApiError) setErrors(error.fields);
      toast(messageOf(error, "修改密码失败"), "error");
    },
  });

  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      navigate("/login", { replace: true });
    },
    onError: (error) => toast(messageOf(error, "退出失败"), "error"),
  });

  if (bootstrap.isPending || general.isPending) return <Spinner />;
  if (bootstrap.isError) return <ErrorText error={bootstrap.error} />;
  if (general.isError) return <ErrorText error={general.error} />;

  const tz = timezone ?? general.data.timezone;
  const retentionValue = retention ?? String(general.data.retentionDays);
  const passwordSet = me.data?.authRequired ?? bootstrap.data.authRequired;
  const loopback = listenIsLoopback(bootstrap.data.listen);

  const submitGeneral = () => {
    const nextErrors: Record<string, string> = {};
    const retentionError = validateRetention(retentionValue);
    if (retentionError) nextErrors.retentionDays = retentionError;
    if (!timezoneOptions(tz).includes(tz)) nextErrors.timezone = "请选择有效时区";
    setErrors((currentErrors) => ({ ...currentErrors, ...nextErrors }));
    if (Object.keys(nextErrors).length > 0) return;
    saveGeneral.mutate();
  };

  const submitPassword = () => {
    const nextErrors: Record<string, string> = {};
    if (passwordSet && current === "") nextErrors.current = "请输入当前密码";
    if (next !== "" && next.length < 8) nextErrors.newPassword = "至少 8 个字符";
    if (next !== confirm) nextErrors.confirmPassword = "两次输入不一致";
    if (!loopback && next === "") nextErrors.newPassword = "非回环监听必须设置密码";
    setErrors((currentErrors) => ({ ...currentErrors, ...nextErrors }));
    if (Object.keys(nextErrors).length > 0) return;
    changePassword.mutate();
  };

  return (
    <div className="settings-panel active">
      <div className="settings-grid">
        <article className="card settings-card">
          <div className="card-head">
            <div>
              <div className="card-title">通用</div>
              <div className="card-sub">时区与明细保留期</div>
            </div>
          </div>
          <div className="settings-body form-grid">
            <Field label="展示时区" hint="影响按天、按小时的聚合与日志时间">
              <Select
                value={tz}
                onChange={(event) => setTimezone(event.target.value)}
              >
                {timezoneOptions(tz).map((zone) => (
                  <option key={zone} value={zone}>
                    {zone}
                  </option>
                ))}
              </Select>
            </Field>
            <Field
              label="请求明细保留（天）"
              hint="7–3650 天；永久聚合不受影响"
              error={errors.retentionDays}
            >
              <Input
                inputMode="numeric"
                value={retentionValue}
                onChange={(event) => setRetention(event.target.value)}
              />
            </Field>
            <div className="form-actions">
              <Button disabled={saveGeneral.isPending} onClick={submitGeneral}>
                {saveGeneral.isPending ? "保存中…" : "保存通用设置"}
              </Button>
            </div>
          </div>
        </article>

        <article className="card settings-card">
          <div className="card-head">
            <div>
              <div className="card-title">管理访问</div>
              <div className="card-sub">
                当前监听 <span className="mono">{bootstrap.data.listen}</span>
              </div>
            </div>
            <Badge tone={passwordSet ? "success" : "warning"}>
              {passwordSet ? "已设密码" : "未设密码"}
            </Badge>
          </div>
          <div className="settings-body form-grid">
            <p className="settings-note">
              {loopback
                ? "面板仅监听回环地址。设置密码后仍可继续本机访问，并可安全改为对外监听。"
                : "面板正在非回环地址监听，必须设置管理密码。"}
            </p>
            <Field label="当前密码" error={errors.current}>
              <Input
                type="password"
                value={current}
                autoComplete="current-password"
                disabled={!passwordSet}
                placeholder={passwordSet ? "输入当前密码" : "尚未设置密码"}
                onChange={(event) => setCurrent(event.target.value)}
              />
            </Field>
            <Field
              label="新密码"
              hint={loopback ? "留空表示清除密码" : "不能留空"}
              error={errors.newPassword}
            >
              <Input
                type="password"
                value={next}
                autoComplete="new-password"
                placeholder="至少 8 个字符"
                onChange={(event) => setNext(event.target.value)}
              />
            </Field>
            <Field label="确认新密码" error={errors.confirmPassword}>
              <Input
                type="password"
                value={confirm}
                autoComplete="new-password"
                placeholder="再次输入"
                onChange={(event) => setConfirm(event.target.value)}
              />
            </Field>
            <div className="form-actions">
              <Button
                disabled={changePassword.isPending || (!passwordSet && next === "")}
                onClick={submitPassword}
              >
                {changePassword.isPending
                  ? "提交中…"
                  : !passwordSet
                    ? "设置密码"
                    : next === ""
                      ? "清除密码"
                      : "更新密码"}
              </Button>
              <Button variant="ghost" onClick={() => logout.mutate()}>
                退出登录
              </Button>
            </div>
            <p className="settings-note">
              修改密码会轮换会话签名，其他已登录设备上的旧会话立即失效。
            </p>
          </div>
        </article>
      </div>

      <p className="settings-hint">
        修改监听地址需要编辑配置文件并重启进程；二进制本身不终止 TLS，对外暴露时请放在反向代理之后。
      </p>
    </div>
  );
}
