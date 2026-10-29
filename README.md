# llmmeter

本机运行的 OpenAI 兼容透传网关：除下文说明的流式 usage 注入外，所有请求原样转发到唯一的上游，逐条记录 token、花费与上下行字节，并提供一个实时统计面板。为 omp、codex、OpenAI SDK 这类客户端设计，上游通常是 DeepSeek 等 OpenAI 兼容服务。

- 单二进制内嵌前端与 SQLite，运行时不依赖 Node 或外部数据库。
- 上游、价格、时区、保留与访问控制都在面板里配置并即时生效，没有 YAML 或配置文件。
- 纯透传：除给缺少 `stream_options` 的流式 chat 请求注入 `include_usage` 外不改写任何请求，不做协议转换，不估算 token。

## 构建与运行

```bash
make build          # npm ci + 前端构建 + go build -o llmmeter
./llmmeter serve -db llmmeter.db -listen 127.0.0.1:8100
```

- `serve` 可省略：`./llmmeter -db ... -listen ...` 等价。
- `-db` 默认 `llmmeter.db`，`-listen` 默认 `127.0.0.1:8100`。
- 首次构建前端需要 Node.js 与 npm；`make web` 只构建前端，`make test` 跑 Go 与前端测试。
- 打开 <http://127.0.0.1:8100/>；未配置时会跳到 `/setup`。

## 首次配置

1. 本机打开 `/setup`，填写上游 `baseUrl`（如 `https://api.deepseek.com`）、上游 API key、超时、时区，可选设置管理密码。
   **不要带 `/v1`**：客户端请求路径会原样拼在其后，填 `https://api.deepseek.com/v1` 会变成 `/v1/v1/chat/completions`。
2. 提交后生成 gateway key（`sk-local-…`）。**明文只显示这一次**，请立刻保存；之后可在设置页轮换。
3. Setup 只接受来自本机的请求；要开放给其他机器，先在本机设置管理密码，再用非回环地址启动（见“认证与远程访问”）。

## 客户端接入

面板会给出 `base_url` 与 gateway key。网关认证与上游 key 分离：客户端只拿 gateway key，上游 key 保存在 SQLite 中且不再回显。

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

在“价格”页按**客户端请求体里的 `model` 字段**建模型（与上游返回的 `model` 无关；仅当请求体没有 `model` 时才回退到上游响应中的模型名）。费率单位是 **USD / 百万 token**：

- `input`、`output`、`cacheRead`、`cacheWrite`、`reasoning`；`reasoning` 留空时按 `output` 计价。
- 可叠加时间窗：选择星期、起止时刻（按设置中的时区解释），并覆盖任意若干费率。
  - `end < start` 表示跨午夜，星期按窗口开始那天判断。
  - 多个窗口同时命中时，按**列表顺序**取第一个（即提交数组的顺序/sort_order，可在价格页用上移、下移调整），与创建时间无关。
  - 窗口内未写的费率回落到 base 价。
- 计价使用**请求完成时刻**所在窗口；命中窗口的费率快照会写入该条记录，之后改价不影响历史。
- 没有配置价格的模型：token 照常记录，成本为 0，请求标记为 `unpriced`。

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
- 客户端中途断开后，网关仍会继续读取上游响应最多 **3 秒 / 2 MiB**，以便补全 usage 与字节记账；该条记录标记为 `clientDisconnected`，字节数仍按上游实际收发计入。
- token usage 只解析 `/v1/chat/completions` 与 `/v1/responses`。拿不到 usage 时如实标记 `no_usage`、`parse_failed`、`too_large` 或 `error`，绝不估算。

## 认证与远程访问

- 面板 API 默认只信任回环请求；`-listen` 为非回环地址时，若尚未设置管理密码，启动会直接失败（避免把无鉴权的面板暴露到网络）。
- 要远程访问：先以 `127.0.0.1:8100` 启动并在面板设置管理密码，再改为 `-listen 0.0.0.0:8100` 重启。
- 管理会话使用 HttpOnly、SameSite=Strict 的签名 Cookie；修改密码会使旧会话立即失效。
- 所有非 GET 的 `/api` 写操作要求 `X-Requested-With: llmmeter` 或同 Host 的 `Origin`（CSRF 防护）。
- 网关请求始终要求 `Authorization: Bearer <gateway key>`；轮换后旧 key 立即失效。
- 二进制不内置 TLS。对外暴露时建议放在 Caddy/Nginx 后面终止 TLS，并让 llmmeter 继续监听 `127.0.0.1`。反代需关闭 SSE 缓冲（如 `proxy_buffering off`）以保证实时面板正常。

## 保留、备份与恢复

- 请求明细默认保留 **90 天**，可在设置页调整为 7–3650 天；每天本地时间 03:00 清理过期明细。
- 按分钟聚合的流量（`traffic_minutes`）**永久保留**：明细被清理后，总览的请求数/token/字节/花费、序列与 breakdown 图表仍有数据；但 priced/unpriced/unmetered 计数与延迟分位数来自明细表，过期后不再可得，请求详情也不可再查。
- 全部数据就在一个 SQLite 文件（默认 `llmmeter.db`）里。备份时停止进程后复制该文件即可；不停机时用 `sqlite3 llmmeter.db ".backup backup.db"`（WAL 模式下不要只拷主文件）。

## 限制

- 同时只支持一个上游；在设置页修改 base URL/key 会立即对后续请求生效，修改监听地址需重启。
- 不重试、不回退、不多上游负载均衡。
- 纯透传，不做协议转换（不把 responses 请求翻译成 chat，反之亦然），只在流式 chat 缺少 `stream_options` 时注入 `include_usage`。
- 不估算 token 或成本；上游不返回 usage 的请求只记录字节与状态。
- 无多用户与细粒度权限，只有单个管理密码；不内置 TLS，需要自行用反向代理终止 HTTPS。
