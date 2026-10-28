# llmmeter

本机运行的 OpenAI 兼容透传网关，记录每次调用的 token 与花费，带一个只读统计页面。为 omp / codex 这类客户端设计，上游通常是 DeepSeek。

## 特性

- 纯透传：`/v1/chat/completions`、`/v1/responses` 及其它路径都原样转发，不做协议转换。
- 只在流式 chat 请求缺 `stream_options` 时注入 `include_usage`，其余请求字节不变。
- 记录 prompt / completion / cache read / reasoning tokens 与成本。
- 峰谷计价：base 价 + 时间窗覆盖（时区、星期、跨午夜）。
- 单二进制 + SQLite，无 Node、无外部数据库；页面由二进制内嵌。
- 拿不到 usage 时标为未计量，绝不估算。

## 构建

```bash
go build -o llmmeter .
```

## 运行

```bash
cp sample.config.yaml config.yaml
export DEEPSEEK_API_KEY=sk-...
./llmmeter serve -config config.yaml -db llmmeter.db
```

- 面板：<http://127.0.0.1:8100/>
- 修改配置后 `kill -HUP <pid>` 热加载（`listen` 变更需重启）。
- 数据就是单个 `llmmeter.db`，备份复制它即可。

## 客户端配置

OpenAI SDK：

```python
client = OpenAI(base_url="http://127.0.0.1:8100/v1", api_key="sk-local-change-me")
```

codex（`~/.codex/config.toml` 或等价配置）把 provider 的 `base_url` 指向 `http://127.0.0.1:8100/v1`，`env_key` 指向网关 key。

## 价格配置

```yaml
pricing:
  models:
    deepseek-chat:
      input_per_mtok: 0.28      # base = 平时价
      output_per_mtok: 0.42
      cache_read_per_mtok: 0.028
      windows:                   # 命中窗口则覆盖，文件顺序优先
        - label: peak
          days: [mon, tue, wed, thu, fri]
          start: "09:00"
          end: "12:00"
          input_per_mtok: 0.56
```

- 窗口时间按 `timezone` 解释（默认 `Asia/Shanghai`），`days` 省略表示每天。
- `end <= start` 表示跨午夜，起始日由 `days` 判断。
- 窗口内未写的费率回落 base。
- 成本按请求**完成时刻**判定窗口；费率快照写入每条记录，改价不影响历史。

## 口径

- `cost = (prompt − cache_read)×input + cache_read×cache_read + (completion − reasoning)×output + reasoning×reasoning`
- 未配置 reasoning 单价时，reasoning 按 output 计价。
- 未配置价格的模型：tokens 照记，成本 0，页面标 `unpriced`。
- 未拿到 usage：成本 0，`usage_status` 为 `no_usage` / `parse_failed` / `too_large`。

## 限制

- 只解析 `/v1/chat/completions` 与 `/v1/responses` 的 usage；其它路径只转发、不落库。
- 不支持多上游、重试、回退、协议转换。
- 面板无登录，只监听回环地址。
