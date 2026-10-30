# llmmeter UI v3 设计文档

- 日期：2026-10-30
- 状态：待评审
- 模块：`github.com/kiry163/llmmeter`
- 前置：`docs/superpowers/specs/2026-10-29-llmmeter-v2-design.md`（后端契约）
- 范围：Web 管理界面重排 + 分析/价格目录/日志明细新增；不改变网关记账与鉴权语义

## 1. 目标与非目标

### 1.1 目标

把现有「把 API 铺满页面」的界面改成任务导向的本地用量面板：

- 首屏回答：现在忙不忙、花的钱涨在哪、哪几笔最贵、哪里异常。
- 常用任务 ≤2 次点击：看区间花费、筛出错/未解析请求、改模型价格、确认配置生效。
- 日志表按 newapi 使用日志的信息密度重做：流式、token 拆分、费用、首字/总耗时、tokens/s。
- 价格页从上游与历史流量自动发现模型，缓存写价不再强制填写。
- 视觉上总览/分析产品化，日志/表格高密度；跟随系统暗色。

### 1.2 非目标

- 多上游、模型别名映射、失败回退。
- 预算阈值、告警通知、CSV/JSON 导出、手动清理界面。
- 协议转换、鉴权模型改造、数据库 schema 迁移。
- 移动端独立 App；只保证响应式 Web。

## 2. 信息架构

一级导航 4 项，顺序固定：

| 路由 | 名称 | 内容 |
|---|---|---|
| `/` | 总览 | 实时条 + 区间统计 + 趋势 + 模型分布 |
| `/logs` | 日志 | 请求明细表 + 筛选 + 请求抽屉 |
| `/analysis` | 分析 | 趋势拆分、Top 榜单、异常、缓存 |
| `/settings` | 设置 | 子页：上游与访问、价格、数据 |

- 设置子路由：`/settings`（上游与访问）、`/settings/pricing`（价格）、`/settings/data`（数据与运行信息）。
- `/requests` 重定向到 `/logs`，保留查询参数；`/pricing` 重定向到 `/settings/pricing`。
- SPA 路由表：`/`、`/logs`、`/analysis`、`/settings`、`/settings/pricing`、`/settings/data`、`/setup`、`/login`；`/requests`、`/pricing` 也由 SPA 接管后重定向。
- 侧栏底部显示配置时区、版本；左上角常驻实时状态点（SSE/轮询/未连接）。
- 桌面侧栏 208px；≤768px 变顶栏，导航横向滚动。

## 3. 页面规格

### 3.1 总览 `/`

从上到下：

1. **实时条**：进行中、QPS、上传 B/s、下载 B/s、P95、错误数，附 60s sparkline。不含任何日期/范围控件。
2. **区间统计**：标题行右侧是范围选择（今天/24h/7d/30d/自定义）；自定义显示起止日期。范围写入 URL。
   - 4 张主 KPI：花费、Tokens、请求、流量。
   - 每张显示环比（上一等长区间）；`partial=true` 时显示「仅保留期内」。
   - 请求卡附错误数/未解析数；Tokens 卡附输入/输出；流量卡附上传/下载。
3. **用量趋势**：单图，可切花费/Tokens/流量/请求；间隔自动（≤2 天分钟、≤7 天小时、更久天）；换范围保留上一版数据避免闪烁。
4. **模型分布**：Top 5 模型，按花费降序，点击进入分析页对应模型筛选。

实时数据来自 `/api/live/stream`（命名事件 `tick`），断线指数退避，连续失败切 5s `/api/live/snapshot`；实时条不因范围切换而重连。

### 3.2 日志 `/logs`

顶部筛选条（全部进 URL）：

- 范围（与总览相同的预设/自定义）
- 模型、路径类（chat/responses/models/audio/other）
- 状态码（3 位数字）
- 用量状态（正常/无用量/解析失败/未解析（响应过大）/不适用/错误）
- 流式（全部/流式/非流式）
- 定价（全部/已定价/未定价）

表格列（桌面）：

| 列 | 内容 |
|---|---|
| 时间 | 本地时区；同日只显示时分秒 |
| 模型 | 模型名 + `流式`/`非流式` 徽标；上游模型不同则悬停显示 |
| Token | 第一行 `输入 / 输出`；第二行 `缓存↓ n` |
| 费用 | USD，4 位小数；未定价显示 `未定价` |
| 耗时 | `首字 0.5s` + `总 6.0s`；无 ttft 显示 `—` |
| 速度 | `t/s`，仅流式；否则 `—` |
| 状态 | HTTP 状态 + 用量状态徽标 |

- 行高 52–56px，表头吸顶，行点击打开右侧请求抽屉；窄屏改为卡片列表（模型+流式、token、费用、耗时、状态）。
- 分页沿用 `before_id` 游标，首屏 50 条，“加载更多”追加；筛选变化重置游标。
- `tokens/s = completionTokens / max((latencyMs − ttftMs)/1000, 0.001)`，仅当 `stream && ttftMs != null && latencyMs > ttftMs && completionTokens > 0`；否则 `—`。非流式不估算。
- 用量状态文案：`ok` 正常、`no_usage` 无用量、`parse_failed` 解析失败、`too_large` 未解析（响应过大）、`not_applicable` 不适用、`error` 错误。
- `too_large` 徽标悬停/抽屉内说明：请求体、响应体或 SSE 单行超过解析上限；字节与转发正常，tokens 与成本未解析。

请求抽屉分区：

1. 概要：ID、模型、上游模型、路径、流式、开始/完成时间、本地日期、总耗时、首字。
2. 流量：请求头（估算）、请求体、响应头（估算）、响应体、合计上行/下行。
3. Tokens：输入、输出、缓存读、缓存写、推理、总计、tokens/s。
4. 计费：成本、窗口、价格快照（原样键值）、定价状态。
5. 状态：HTTP 状态、用量状态、客户端断开、错误。

### 3.3 分析 `/analysis`

顶部范围选择；四个区块，桌面两列、窄屏单列：

1. **趋势**：按模型堆叠面积/柱，指标切换（花费/Tokens/流量/请求）；点击图例模型跳日志并带模型筛选。间隔规则与总览一致。
2. **Top 榜单**：三组各 10 条（花费、总 tokens、总耗时），行点击在分析页直接打开共享请求抽屉；不跳页。
3. **异常与慢请求**：请求数、错误数与错误率、未计量、未解析、客户端断开、慢请求数（>2000ms）与 P95；点击未解析/错误跳到日志对应筛选。
4. **缓存**：命中率（cacheRead / prompt）、缓存读 tokens、估算节省金额；`available=false` 时显示「超出明细保留期，无法估算」。

模型与路径排名表放在趋势区块下方，按当前指标排序；点击行跳日志（`model` 或 `pathClass` + 当前范围）。

### 3.4 设置

子导航：**上游与访问 / 价格 / 数据**。

**上游与访问**（`/settings`）：baseUrl、API Key（掩码 + 替换）、超时、测试连接；时区、明细保留天数；设置/修改/清除管理密码；gateway key 掩码 + 轮换（一次性明文）；有效监听 + 修改需重启提示。现有能力全部保留，只重排。

**价格**（`/settings/pricing`）：

- 顶部「刷新上游模型」按钮，显示上次拉取时间；上游失败显示脱敏提示，不阻塞手工添加。
- 模型目录合并三类：已配置、上游可见、日志已观测；来源徽标；未配置高亮。
- 添加价格：模型名 combobox（目录 + 手填），基础价输入/输出/缓存读/缓存写/reasoning；**缓存写留空按 0**，不显示必填错误；reasoning 留空继承 output。
- 窗口编辑器沿用现有规则（星期芯片、起止时间、覆盖价留空继承、上移/下移）；保存后显示「当前命中窗口」。
- 一键补录：对未配置模型批量创建 0 费率条目；五项费率全为 0 的模型在列表标 `0 价（待填）`，请求成本为 0 但计为已配置。
- 删除二次确认；保存即热生效。

**数据**（`/settings/data`）：DB 路径、体积、版本、明细保留天数（与上游页共用一个字段，保存后两处同步）、有效监听、服务端时间。无导出/清理按钮。

## 4. 后端增量契约

### 4.1 模型目录 `GET /api/model-catalog?refresh=1`

管理员接口。响应：

```json
{
  "fetchedAt": "2026-10-29T11:00:00Z",
  "upstream": [{"id": "deepseek-chat", "ownedBy": "deepseek"}],
  "observed": [{"model": "deepseek-chat", "requests": 128, "lastSeen": "2026-10-29T10:59:00Z"}],
  "upstreamError": ""
}
```

- 上游拉取：已存 API key，依次尝试 `base + "/models"`、`base + "/v1/models"`（base 已以 `/v1` 结尾时不重复），5s 超时；结果缓存 5 分钟，`refresh=1` 绕过缓存。上游设置保存成功后清空该缓存。
- `observed` 来自保留期内 `requests` 按模型聚合（空模型名排除）。
- 上游失败：`upstream` 为空、`upstreamError` 为脱敏原因、HTTP 仍为 200；已观测项照常返回。
- 不返回上游 key 或原始错误体。

### 4.2 分析聚合 `GET /api/analysis?from&to&interval=auto`

管理员接口。`from`/`to` 为配置时区本地日期，`to` 含当天；缺省最近 7 天；`to >= from`。`interval` 允许 `auto|minute|hour|day`：`auto` 与总览一致（≤2 天分钟、≤7 天小时、更久天）；显式 `minute` 限 ≤2 天、`hour` 限 ≤90 天，超出返回 400。响应字段：

```json
{
  "range": {"from":"2026-10-23","to":"2026-10-29","interval":"hour","partial":false},
  "trend": [{"day":"2026-10-29","minuteOfDay":600,
             "byModel":{"deepseek-chat":{"requests":12,"promptTokens":9000,"completionTokens":1200,
                                         "costNanoUsd":540000,"upBytes":4200,"downBytes":18000}}}],
  "models": [{"key":"deepseek-chat","requests":120,"errors":1,"upBytes":38000,"downBytes":160000,
              "promptTokens":90000,"completionTokens":12000,"cacheReadTokens":40000,
              "costNanoUsd":1200000,"unpriced":0}],
  "paths": [{"key":"chat","requests":110,"errors":0,"upBytes":38000,"downBytes":160000,
             "promptTokens":80000,"completionTokens":10000,"costNanoUsd":1000000,"unpriced":0}],
  "topRequests": {
    "cost":    [{"id":412,"startedAt":"2026-10-29T10:00:00Z","model":"deepseek-chat","pathClass":"chat",
                 "stream":true,"promptTokens":45070,"completionTokens":710,"cacheReadTokens":45184,
                 "totalTokens":45780,"costNanoUsd":8000000,"latencyMs":6000,"ttftMs":500,
                 "status":200,"usageStatus":"ok"}],
    "tokens":  [],
    "latency": []
  },
  "health": {"requests":120,"errors":1,"errorRate":0.0083,"unmetered":2,"tooLarge":1,
             "clientDisconnected":0,"slow":{"thresholdMs":2000,"p95Ms":1800,"countOver":4}},
  "cache": {"promptTokens":90000,"cacheReadTokens":40000,"hitRate":0.444,
            "savedNanoUsd":310000,"available":true}
}
```

规则：

- `trend` 来自 `traffic_minutes`，按 `(local_day, bucket, model)` 聚合，只返回非零桶；`minuteOfDay=0` 表示天桶。
- `models`/`paths` 来自 `traffic_minutes`；`unpriced` 来自 `requests`（仅保留期内；partial 时只统计保留期内并在 UI 标注）。
- `topRequests` 三组各 10 条，分别按 `cost_nano_usd DESC`、`total_tokens DESC`、`latency_ms DESC`；来自 `requests`。超出保留期时三组为空数组。
- `health.requests/errors` 来自汇总；`errorRate = errors/requests`（0 请求为 0）；`unmetered` 为 `usage_status NOT IN ('ok','not_applicable')`；`tooLarge` 为其中 `too_large` 的数量；`clientDisconnected` 单独计数；`slow.p95Ms` 用 100ms 桶近似，`countOver` 为 `latency_ms > 2000`。
- `cache`：`hitRate = cacheReadTokens/promptTokens`（0 → 0）；`savedNanoUsd = Σ cacheReadTokens × max(input − cacheRead, 0) × 1000`，逐条读取 `rates_json` 快照，未定价记录不计；区间早于保留期起点时 `available=false`、`savedNanoUsd=null`。
- `partial = from < today − retentionDays`；partial 时 Top 与缓存节省不可用，趋势/模型/路径/健康计数仍可用；`slow.p95Ms` 置 null。
- 单次查询超时 5 秒；超时返回 500 `internal`，不返回半成品。

### 4.3 日志过滤 `GET /api/requests`

在现有参数上增加可选 `stream=true|false`；其余字段不变。`stream` 非法值返回 400 `validation`。

### 4.4 批量补录 `POST /api/models/import`

管理员接口。请求 `{"names":["a","b"]}`（最多 100 个，去空白、去重、去空）。事务内只插入不存在的模型，费率全 0；返回：

```json
{"created":[{"id":12,"name":"a"}],"skipped":["b"]}
```

名字非法（空/超长）返回 400；已存在不算错误。0 费率模型可正常删除/编辑。

## 5. 视觉规范

| 项 | 规则 |
|---|---|
| 布局 | 4px 网格；侧栏 208px；内容最大 1440px；卡片间距 16px |
| 字体 | 正文 13–14px；关键数字 20–28px，tabular-nums；等宽仅用于 ID/路径 |
| 颜色 | 沿用现有 OKLCH 中性色与 `--accent`；新增图表序列 `--c1…--c6`；语义色 success/warning/danger；无渐变 |
| 形状 | 卡片圆角 10px；按钮 6px；表格行分隔 1px；浮层阴影 |
| 表格 | 行高 52–56px；表头吸顶；数字右对齐；边框弱化 |
| 暗色 | 跟随系统；所有颜色走变量 |
| 动效 | 120–160ms hover/focus 与抽屉滑入；`prefers-reduced-motion` 关闭位移动画 |
| 图表 | ECharts；坐标轴弱化，tooltip 跟随主题；空数据不画 0 轴 |

组件：`AppShell`、`PageHeader`、`RangeBar`、`DataTable`、`RequestDrawer`、`EmptyState`、`ErrorBanner`、`Skeleton`、`Badge`。共享逻辑：`useRange`（URL + 时区）、`useLive`、`lib/usage.ts`（状态文案）、`lib/speed.ts`（tokens/s）、`lib/metrics.ts`（格式化）。

## 6. 交互与状态

- 范围存 URL；切换页面保留各自范围；总览实时条不参与范围。
- 首次加载骨架；换范围 `keepPreviousData`；区块级错误 + 重试，不整页白屏。
- 分析页模型/路径点击 → `/logs?model=...` 或 `pathClass=...` 并带当前范围；Top 行直接开抽屉。
- 日志筛选变化重置游标；抽屉用 id 作为 query key，避免旧响应覆盖新选择。
- 价格保存后 invalidate `models`/`model-catalog` 与总览；显示「已生效」。
- 401 跳登录并保留内部 `next`；无密码远程访问显示「仅限本机」。
- 上游目录拉取失败：价格页顶部提示 + 重试；仍可从已观测/手填添加。

## 7. 测试与验收

Go：

- `/api/model-catalog`：上游成功、404 回退 `/v1/models`、上游失败仍返回 observed、缓存与 refresh、脱敏。
- `/api/analysis`：趋势按模型分桶、Top 三组排序、健康计数、缓存节省（含未定价、partial）、interval 校验、空区间。
- `/api/models/import`：去重/跳过/事务/上限/非法名。
- `/api/requests?stream=`：true/false/非法。
- 现有网关、记账、鉴权、SSE 测试不回归。

前端：

- Vitest：`tokensPerSecond`（流式/非流式/ttft=null/零耗时）、用量状态文案、范围/时区、价格校验（缓存写留空）。
- 构建：`npm test`、`npm run build`、`go build ./...`。

验收：

- 总览：实时条无范围控件；区间筛选在区间统计标题行；4 KPI + 趋势 + 模型分布；partial 标注。
- 日志：流式筛选与徽标；token 拆分；首字/总耗时；t/s 仅流式；`too_large` 文案与解释；行抽屉；窄屏卡片。
- 分析：四区块；Top 三组各 10 条并开抽屉；模型/路径跳日志带筛选；缓存节省与手算一致；partial 时 Top/节省置灰。
- 价格：目录三源合并；缓存写留空；一键补录；保存热生效。
- 设置：三子页，现有功能全部保留。
- 浏览器冒烟：真实上游或假上游，走 setup→发请求→总览→日志→分析→改价→再请求。

## 8. 风险

| 风险 | 对策 |
|---|---|
| 上游 `/models` 不可用/过大 | 缓存、超时、失败提示；已观测列表兜底 |
| 分析查询变慢 | 单聚合接口、区间限制、先测量；必要时加索引 |
| 缓存节省口径误解 | UI 标“估算”，文档写公式；partial 置灰 |
| 0 费率模型被误认为已定价 | 列表标 `0 价（待填）`，请求费用显示 $0 |
| 路由改名丢书签 | `/requests`、`/pricing` 重定向保留参数 |
| 前端包体继续增大 | 复用 ECharts，按页面拆分，不新增图表库 |

## 9. 决策记录

- 4 区导航，价格收进设置：减少一级页面，但保留价格深链。
- 日志即用量列表：不另设页面，避免重复。
- 分析单聚合端点：个人规模下减少往返与加载态；将来膨胀再拆。
- Top 行在分析页直接开抽屉：保持上下文，不强制跳页。
- tokens/s 前端计算：无需 schema/落库变更，口径可解释。
- 缓存节省按历史费率快照：只在明细保留期内可用，标注估算。
- 模型自动发现合并上游与已观测：任一来源失败都能继续配置。
- 0 费率补录：不伪造价格，UI 明确待填。
