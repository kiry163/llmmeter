import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { Badge, Card, ErrorText, Spinner } from "../components/ui";

function perMtok(value: number | null): string {
  if (value == null) return "继承";
  return `$${value.toFixed(4)}/M`;
}

export function Pricing() {
  const models = useQuery({ queryKey: ["models"], queryFn: api.getModels });

  return (
    <div className="space-y-4">
      <header>
        <h1 className="text-xl font-semibold">价格</h1>
        <p className="text-sm text-muted">模型基础价与时段窗口，编辑能力由后续迭代补齐。</p>
      </header>

      <Card title="模型列表">
        {models.isPending && <Spinner />}
        {models.isError && <ErrorText error={models.error} />}
        {models.data && models.data.models.length === 0 && (
          <p className="text-sm text-muted">尚未配置价格模型。</p>
        )}
        {models.data && models.data.models.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[680px] text-left text-sm">
              <thead className="text-xs text-muted">
                <tr className="border-b border-line">
                  <th className="py-2 pr-3 font-medium">模型</th>
                  <th className="py-2 pr-3 font-medium">输入</th>
                  <th className="py-2 pr-3 font-medium">输出</th>
                  <th className="py-2 pr-3 font-medium">缓存读</th>
                  <th className="py-2 pr-3 font-medium">缓存写</th>
                  <th className="py-2 pr-3 font-medium">推理</th>
                  <th className="py-2 pr-3 font-medium">窗口</th>
                </tr>
              </thead>
              <tbody>
                {models.data.models.map((model) => (
                  <tr key={model.id} className="border-b border-line/60">
                    <td className="py-2 pr-3 font-medium">{model.name}</td>
                    <td className="py-2 pr-3">{perMtok(model.inputPerMTok)}</td>
                    <td className="py-2 pr-3">{perMtok(model.outputPerMTok)}</td>
                    <td className="py-2 pr-3">{perMtok(model.cacheReadPerMTok)}</td>
                    <td className="py-2 pr-3">{perMtok(model.cacheWritePerMTok)}</td>
                    <td className="py-2 pr-3">{perMtok(model.reasoningPerMTok)}</td>
                    <td className="py-2 pr-3">
                      <Badge tone="info">{model.windows.length} 个</Badge>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}
