# llmmeter

本机运行的 OpenAI 兼容透传网关：除下文说明的流式 usage 注入外，所有请求原样转发到网关密钥所绑定的上游，逐条记录 token、花费与上下行字节，并提供一个可查看统计、日志与分析的管理面板。为 omp、codex、OpenAI SDK 这类客户端设计，上游通常是 DeepSeek 等 OpenAI 兼容服务。

- 单二进制内嵌前端与 SQLite，运行时不依赖 Node 或外部数据库。
- 可配置多个上游；每个网关密钥固定绑定一个上游，客户端用不同密钥即可切换上游。
- 上游、价格、时区、保留与访问控制保存在 YAML 配置文件（schema v2，默认 `~/.config/llmmeter/config.yaml`）中，重启后以 YAML 为准；首次启动会自动生成带注释的配置模板，面板的写操作也会即时落到该文件。
- 纯透传：除给缺少 `stream_options` 的流式 chat 请求注入 `include_usage` 外不改写任何请求，不做协议转换，不估算 token。

## 构建与运行

```bash
make build          # npm ci + 前端构建 + go build -o llmmeter
./llmmeter -config ~/.config/llmmeter/config.yaml
```

- 首次启动时，若配置文件不存在，会在该路径生成带注释的 YAML 模板（0600，目录 0700），然后正常启动。模板默认没有上游和本地 key；可以在面板添加，也可以直接编辑 YAML 后重启或发送 `SIGHUP`。
- `-config` 默认 `$XDG_CONFIG_HOME/llmmeter/config.yaml`（通常是 `~/.config/llmmeter/config.yaml`），文件格式为 YAML schema v2。
- `-db` 默认取配置里的 `server.database`，未配置时为 `$XDG_CACHE_HOME/llmmeter/llmmeter.db`；`-listen` 的优先级是 `-listen` > `server.listen` > `127.0.0.1:8100`。
- 首次构建前端需要 Node.js 与 npm；`make web` 只构建前端，`make test` 跑 Go 与前端测试。
- 打开 <http://127.0.0.1:8100/>；没有上游时页面照常打开，可在「设置 / 上游与密钥」直接添加。

## 界面与路由

单页应用的路由与用途：

| 路由 | 页面 |
| --- | --- |
| `/` | 总览：今日花费 / Tokens / 流量三张指标卡（累计、环比、sparkline），实时条（进行中、P95、错误率、缓存命中），用量与花费趋势（输入 / 缓存读 / 输出 + 花费线，范围写入 URL），模型排行、按小时花费与最近 8 条请求。 |
| `/logs` | 日志：按范围、上游、网关密钥、模型、路径类、状态类（2xx/4xx/5xx）、用量状态、流式、定价筛选；筛选汇总（请求 / 花费 / Tokens / 错误）覆盖全部匹配行；表格显示上游/密钥、流式徽标、输入/输出/缓存读、费用、首字/总耗时与 tokens/s；点击行打开请求抽屉，游标分页每次 12 条。 |
| `/analysis` | 分析：按模型堆叠的趋势（花费 / Tokens / 请求 / 流量）、Top 三组榜单（花费 / Tokens / 耗时）、异常与慢请求、缓存命中与估算节省；点击模型或路径跳到带筛选的日志。 |
| `/settings` | 设置 / 上游与密钥：上游的增删改、启停、连接测试与模型探测，API key 显隐；网关密钥的创建、轮换、显隐、绑定上游与启停。 |
| `/settings/pricing` | 设置 / 价格：按上游维护模型费率、峰谷窗口与模型目录导入。 |
| `/settings/access` | 设置 / 访问：时区、明细保留天数、管理密码与退出登录。 |
| `/settings/data` | 设置 / 数据：数据库路径与体积（含 WAL）、版本、保留天数、有效监听、服务端时区与写入队列。 |
| `/login` | 登录。 |

旧路由 `/requests` 重定向到 `/logs`，`/pricing` 重定向到 `/settings/pricing`（保留查询参数）。主题默认跟随系统，可在侧栏或登录页切换并记忆；窄屏下侧栏变为顶部横向导航，日志/价格表改为卡片列表，页面不横向溢出。

总览、日志和分析页顶部都有上游筛选；选择上游后统计、趋势、榜单和实时条都会随之收窄，筛选条件写入 URL。

## 首次配置

1. 直接运行 `llmmeter`。若配置文件不存在，程序会生成带注释的模板并启动，默认监听 `127.0.0.1:8100`。
2. 打开面板，在「设置 / 上游与密钥」添加上游（`name`、`base_url`、`api_key`），再创建绑定该上游的网关密钥。所有写操作会落到 `config.yaml`，保存后立即生效；也可以直接编辑 YAML 并发送 `SIGHUP`。
   **base URL 不要带 `/v1`**：客户端请求路径（含 `/v1/chat/completions`）会原样拼在其后，填 `https://api.deepseek.com/v1` 会变成 `/v1/v1/chat/completions`。
3. 要开放给其他机器，先在「设置 / 访问」设置管理密码，再用非回环地址启动（见“认证与远程访问”）。没有上游时网关返回 `503 not_configured`。

## 客户端接入

面板会给出 `base_url` 与 gateway key。网关认证与上游 key 分离：客户端只拿 gateway key；上游 key 与本地 gateway key 都保存在 `config.yaml`（权限 0600）中。列表接口只返回掩码，只有显式 reveal 接口（`/api/upstreams/{id}/secret`、`/api/keys/{keyId}/secret`）才返回明文。

OpenAI SDK：

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8100/v1",
    api_key="sk-local-……",  # gateway key
)
```

codex（`~/.codex/config.toml`）：

```toml
model_provider = "llmmeter"

[model_providers.llmmeter]
name = "llmmeter"
base_url = "http://127.0.0.1:8100/v1"
env_key = "LLMMETER_API_KEY"
wire_api = "chat"
```

然后 `export LLMMETER_API_KEY=sk-local-……`。`/v1/chat/completions` 与 `/v1/responses` 都会原样转发。

## 价格与峰谷窗口

价格在 `/settings/pricing` 按上游配置。右侧「模型目录」合并三类来源——已配置、上游 `GET /models` 可见（base URL 未以 `/v1` 结尾时回退 `GET /v1/models`）、日志已观测；可刷新目录并把未定价模型批量导入为 0 费率。上游失败只显示脱敏原因（不含 API key），不阻塞手工添加。

按**客户端请求体里的 `model` 字段**建模型（与上游返回的 `model` 无关；仅当请求体没有 `model` 时才回退到上游响应中的模型名）。费率单位是 **USD / 百万 token**：

- `input`、`output`、`cacheRead`、`cacheWrite`、`reasoning`；`reasoning` 留空时按 `output` 计价，`cacheWrite` 留空按 0。
- 可叠加时间窗：选择星期、起止时刻（按设置中的时区解释），并覆盖任意若干费率。
  - `end < start` 表示跨午夜，星期按窗口开始那天判断。
  - 多个窗口同时命中时，按**列表顺序**取第一个；价格页的时段编辑器提供上移/下移调整顺序。
  - 窗口内未写的费率回落到 base 价。
- 计价使用**请求完成时刻**所在窗口；命中窗口的费率快照会写入该条记录，之后改价不影响历史。
- 完全未配置价格的模型：token 照常记录，成本为 0，请求标记为 `unpriced`。
- 「导入未定价模型」把目录中未配置的模型批量创建为 0 费率条目（每批最多 100 个名字）。五项费率全为 0 的条目可按需补价；这类模型的请求成本为 $0，但计为已定价（`priced`），不再是 `unpriced`。

成本公式（负数分量按 0 计）：

```
(prompt − cacheRead) × input
+ cacheRead × cacheRead
+ cacheWrite × cacheWrite
+ (completion − reasoning) × output
+ reasoning × reasoning
```

## 流量口径

- **所有**被转发的路径都会落库（`chat`、`responses`、`models`、`audio`、`other`），不只是对话请求。
- 请求体字节 = 实际发往上游的字节数，流式 chat 缺少 `stream_options` 时注入的 `include_usage` 也计入。
- 响应体字节 = 网关从上游读到的字节数。请求上游时禁用了透明解压（`DisableCompression`），因此是上游实际发出的线上字节，而不是解压后的长度。
- 请求/响应头字节按 HTTP/1.1 文本格式估算（请求行/状态行 + 每个 header 的 `name: value` + 结尾空行）。HTTP/2 会压缩头部，所以这部分只是近似值。
- 客户端中途断开后，网关**不会主动取消**上游请求，会继续读取直到上游结束，以便补全 usage 与字节记账；该条记录标记为 `clientDisconnected`，字节数仍按上游实际收发计入。
- token usage 只解析 `/v1/chat/completions` 与 `/v1/responses`。拿不到 usage 时如实标记 `no_usage`、`parse_failed`、`too_large` 或 `error`，绝不估算。

## tokens/s 与缓存节省

- 日志与请求抽屉里的 `tokens/s` 只对流式请求显示：

  ```
  completionTokens / max((latencyMs − ttftMs) / 1000, 0.001)
  ```

  要求 `ttftMs` 存在、`latencyMs > ttftMs`、`completionTokens > 0`；否则显示 `—`。非流式请求不估算生成速度。
- 用量状态文案：`ok` 正常、`no_usage` 无用量、`parse_failed` 解析失败、`too_large` 未解析（响应过大）、`not_applicable` 不适用、`error` 错误。
- `too_large` 表示请求体、响应体或 SSE 单行超过解析上限；字节与转发正常，tokens 与成本未解析。日志徽标悬停和请求抽屉内都会显示这条说明。
- 分析页的缓存命中率 = `cacheReadTokens / promptTokens`（区间内总量）。
- 估算节省按请求明细逐条累加 `cacheReadTokens × (inputPerMTok − cacheReadPerMTok)`，只统计已定价、缓存读 > 0 的明细，单价差 ≤ 0 的行不计。它依赖明细保留期，只是估算值。
- 缓存节省、Top 榜单，以及健康区块里的未计量、未解析（too_large）、客户端断开、慢请求计数与 P95 都来自请求明细；区间起点早于明细保留期时（响应 `range.partial=true`），分析页将这些值置灰并标「仅保留期内」（缓存节省显示「超出明细保留期，无法估算」）。健康区块的请求数、错误数与错误率，以及趋势、模型/路径排名来自永久聚合，不受明细清理影响。

## 认证与远程访问

- 面板 API 默认只信任回环请求；`-listen` 为非回环地址时，若尚未设置管理密码，启动会直接失败（避免把无鉴权的面板暴露到网络）。
- 要远程访问：先在 `127.0.0.1` 启动，在「设置 / 访问」设置管理密码，再改为非回环地址启动。也可以直接把 Argon2id PHC 哈希写入 `config.yaml` 的 `auth.admin_password_hash`。`POST /api/auth/password` 用于设置/修改/清除密码。
- 管理会话使用 HttpOnly、SameSite=Strict 的签名 Cookie，有效期 12 小时；修改密码会使旧会话立即失效。
- 所有非 GET 的 `/api` 写操作要求 `X-Requested-With: llmmeter` 或同 Host 的 `Origin`（CSRF 防护）。
- 网关请求始终要求 `Authorization: Bearer <gateway key>`；轮换后旧 key 立即失效。
- 二进制不内置 TLS。对外暴露时建议放在 Caddy/Nginx 后面终止 TLS，并让 llmmeter 继续监听 `127.0.0.1`。反代需关闭 SSE 缓冲（如 `proxy_buffering off`）以保证实时面板正常。

## 面板 API

面板与网关共用同一监听；下列是面板新增或扩展的只读/管理接口（均遵循上面的回环与 CSRF 规则）：

| 方法与路径 | 说明 |
| --- | --- |
| `GET /api/bootstrap` | 返回 `{version, authRequired, listen, timezone, configPath, dbPath, earliestDay}`；`earliestDay` 为永久聚合中最早有流量的本地日期，用于总览“全部”范围，无流量时为空。 |
| `GET /api/overview?from=&to=&upstream=` | 区间汇总；`upstream` 可选，用于收窄到单个上游。 |
| `GET /api/series?…&interval=auto\|minute\|hour\|day` | 返回非空桶；`auto` 与分析页一致（≤2 天分钟、≤7 天小时、更久天），显式间隔超出上限返回 400；每个点含 `promptTokens`、`completionTokens`、`cacheReadTokens`、`costNanoUsd`、上下行字节。前端负责补齐空桶。 |
| `GET /api/breakdown?by=model\|pathClass` | 按模型或路径类聚合；每行带 `upstreamId`/`upstreamName`，支持 `upstream` 过滤。 |
| `GET /api/analysis?from=&to=&interval=auto` | 一次返回 `range`（含 `interval`、`partial`）、按上游/模型拆分的 `trend`、`models`、`paths`、`topRequests`、`health`、`cache`；支持 `upstream` 过滤。 |
| `GET /api/requests?…` | 支持 `upstream`、`key`、`model`、`pathClass`、`statusClass=2xx\|4xx\|5xx`、`usageStatus`、`stream`、`priced`、`before_id`、`limit`；响应含 `summary:{requests,costNanoUsd,totalTokens,errors}`，汇总覆盖筛选后的全部行并忽略游标/分页。 |
| `GET /api/upstreams` | 上游列表（密钥仅返回掩码）。`POST /api/upstreams` 创建；`PATCH/DELETE /api/upstreams/{id}` 修改/删除。 |
| `POST /api/upstreams/probe` | 用草稿 `{baseUrl, apiKey}` 拉取模型列表，不写配置。`POST /api/upstreams/{id}/test` 测试已保存上游。 |
| `GET /api/upstreams/{id}/secret` | 显式返回上游 API key 明文。 |
| `GET/PUT /api/upstreams/{id}/pricing` | 读取或整体替换该上游的价格表；`POST .../pricing/import` 批量导入未定价模型（每批最多 100 个名字）。 |
| `GET /api/upstreams/{id}/catalog` | 返回 `{fetchedAt, provider, presetSnapshot, configured, upstream, observed, presets, upstreamError}`；`?refresh=1` 跳过缓存强制刷新。上游失败时 HTTP 仍为 200，`upstream` 为空、`upstreamError` 为脱敏原因。 |
| `GET /api/keys` | 网关密钥列表（仅掩码）。`POST /api/keys` 创建；`PATCH/DELETE /api/keys/{keyId}` 修改/删除；`POST .../rotate` 轮换；`GET .../secret` 返回明文。 |
| `GET/PATCH /api/general` | 读取或修改时区与明细保留天数。 |
| `GET /api/data` | 返回数据库路径/体积/WAL、schema 版本、保留天数、有效监听、写入队列等运行信息。 |
| `POST /api/config/reload` | 重新读取 YAML 配置并发布新快照，失败时保留旧配置。 |

## 保留、备份与恢复

- 请求明细默认保留 **90 天**，可在 `general.retention_days` 调整为 7–3650 天；每天本地时间 03:00 清理过期明细。
- 按分钟聚合的流量（`traffic_minutes`）**永久保留**：明细被清理后，总览的请求数/token/字节/花费、序列与 breakdown 图表仍有数据；但 priced/unpriced/unmetered 计数与延迟分位数来自明细表，过期后不再可得，请求详情也不可再查。分析页的 Top 榜单、缓存节省金额、健康区块的未计量/未解析/断开/慢请求计数与 P95 同样依赖明细，区间超出保留期时置灰（`range.partial=true`）。
- 全部数据就在一个 SQLite 文件（默认 `llmmeter.db`）里。备份时停止进程后复制该文件即可；不停机时用 `sqlite3 llmmeter.db ".backup backup.db"`（WAL 模式下不要只拷主文件）。

## 限制

- 支持多个上游，但网关密钥只能绑定一个上游；不做自动重试、失败回退或多上游负载均衡。编辑上游 base URL/key 后即时生效，修改监听地址需重启。
- 纯透传，不做协议转换（不把 responses 请求翻译成 chat，反之亦然），只在流式 chat 缺少 `stream_options` 时注入 `include_usage`。
- 网关不设置上游请求超时，也不会在客户端断开后主动取消已开始的上游请求；请求会一直读到上游结束，使用方需自行处理上游长时间无响应的情况。
- 不估算 token 或成本；上游不返回 usage 的请求只记录字节与状态。
- 无多用户与细粒度权限，只有单个管理密码；不内置 TLS，需要自行用反向代理终止 HTTPS。
