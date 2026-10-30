# llmmeter UI v3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把现有 4 页只读面板重排为「总览 / 日志 / 分析 / 设置」四区，并新增模型目录、分析聚合、newapi 风格日志与可选缓存写价。

**Architecture:** 后端在现有 store 查询层新增分析聚合，在 API 层新增 `/api/model-catalog`、`/api/analysis`、`/api/models/import` 与 `stream` 过滤；前端保留 React/Vite/ECharts，重建 AppShell 与页面，抽出共享请求抽屉，价格收进设置子页。无 schema 迁移。

**Tech Stack:** Go 1.27.1、modernc SQLite、React 19、Vite 7、TypeScript、Tailwind 4、ECharts 6、TanStack Query。

**Spec:** `docs/superpowers/specs/2026-10-30-llmmeter-ui-redesign-design.md`

## Global Constraints

- 导航固定 4 项：总览 `/`、日志 `/logs`、分析 `/analysis`、设置 `/settings`；设置子页 `/settings/pricing`、`/settings/data`。
- `/requests` 重定向 `/logs`，`/pricing` 重定向 `/settings/pricing`，保留查询参数。
- 总览实时条不含范围控件；区间筛选只在「区间统计」标题行。
- 日志 `tokens/s` 仅当 `stream && ttftMs != null && latencyMs > ttftMs && completionTokens > 0`，公式 `completionTokens / max((latencyMs − ttftMs)/1000, 0.001)`；否则 `—`。
- `too_large` 文案固定为「未解析（响应过大）」；说明包含“字节与转发正常，tokens/成本未解析”。
- 缓存节省 `Σ cacheReadTokens × max(input − cacheRead, 0) × 1000` nano-USD，只算有 `rates_json` 的记录；区间早于保留期时不可用。
- 模型目录缓存 5 分钟，`refresh=1` 绕过；上游依次尝试 `<base>/models`、`<base>/v1/models`；失败 HTTP 仍 200，`upstreamError` 脱敏。
- 批量导入最多 100 个名字，事务内只建缺失模型，费率全 0；重复跳过；0 费率模型标「0 价（待填）」。
- API JSON camelCase；错误体 `{"error":{"code","message","fields"}}`，code 取 `validation|unauthorized|forbidden|not_found|conflict|internal`。
- 中文 UI；TS strict；不新增图表库；不改网关记账/鉴权语义；无数据库 schema 变更。
- 现有能力（setup、登录、改密/清密、轮换、测试连接、时区、保留期、运行信息）全部保留。
- 每个任务结束：`go test ./internal/<pkg> -race` 或 `npm test` 通过；最终 `go test ./... -race`、`npm test`、`npm run build`、`go build ./...` 全绿。

## Review Focus

| 输入/条件 | 期望行为 | 固定该行为的任务 |
|---|---|---|
| 上游 `/models` 超时、404 或返回巨量列表 | 目录接口仍 200；已观测模型可用；UI 可手填；错误脱敏 | Task 3 |
| 查询区间早于明细保留期 | `partial=true`；Top/缓存节省置空或置灰；趋势/模型/路径/健康计数仍可用 | Task 1、Task 4 |
| 缓存节省遇到未定价/混合费率记录 | 只累计有费率快照的记录；金额与手算一致；不可用时返回 null | Task 1、Task 4 |
| 流式但 ttft 缺失、非流式、生成时间为 0 | t/s 显示 `—` 或使用 1ms 下限，不出现 NaN/Infinity | Task 5、Task 8 |
| 批量导入含重复/已存在/空名/超限 | 跳过重复与已存在；非法输入 400；事务不留下半成品 | Task 2 |

---

### Task 1: Store 分析查询与日志流式过滤

**Files:**
- Create: `internal/store/analysis.go`
- Create: `internal/store/analysis_test.go`
- Modify: `internal/store/queries.go`（`RequestFilter` 增加 `Stream *bool`，`Requests` 拼接过滤）
- Modify: `internal/api/read.go`（解析 `stream`）
- Modify: `internal/api/read_test.go`

**Interfaces:**
- Consumes: `Store`、`RequestFilter`、`Interval`、`usage` 状态常量
- Produces:

```go
type TrendModel struct {
	Requests, PromptTokens, CompletionTokens int64
	CostNanoUSD, UpBytes, DownBytes          int64
}
type TrendBucket struct {
	Day         string
	MinuteOfDay int
	ByModel     map[string]TrendModel
}
type TopRequest struct {
	ID                                             int64
	StartedAt                                      time.Time
	Model, PathClass                               string
	Stream                                         bool
	PromptTokens, CompletionTokens, CacheReadTokens int64
	TotalTokens, CostNanoUSD, LatencyMS            int64
	TTFTMS                                         *int64
	Status                                         int
	UsageStatus                                    string
}
type AnalysisHealth struct {
	Requests, Errors, Unmetered, TooLarge, ClientDisconnected int64
	ErrorRate                                                 float64
	P95MS                                                     *int64
	CountOver, ThresholdMS                                    int64
}
type CacheStats struct {
	PromptTokens, CacheReadTokens int64
	HitRate                       float64
	SavedNanoUSD                  *int64
}
type ObservedModel struct {
	Model    string
	Requests int64
	LastSeen time.Time
}
func (s *Store) TrendByModel(ctx context.Context, from, to string, iv Interval) ([]TrendBucket, error)
func (s *Store) TopRequests(ctx context.Context, from, to, by string, limit int) ([]TopRequest, error)
func (s *Store) AnalysisHealth(ctx context.Context, from, to string) (AnalysisHealth, error)
func (s *Store) CacheStats(ctx context.Context, from, to string) (CacheStats, error)
func (s *Store) ObservedModels(ctx context.Context, sinceDay string) ([]ObservedModel, error)
```

- [ ] **Step 1: 写失败的测试**

```go
func TestTrendByModelAndTop(t *testing.T) {
	st := seedAnalysisStore(t) // 两个模型、两个小时桶、含 priced/unpriced
	ctx := context.Background()
	trend, err := st.TrendByModel(ctx, "2026-10-29", "2026-10-29", store.IntervalHour)
	if err != nil { t.Fatal(err) }
	if len(trend) != 2 { t.Fatalf("buckets = %+v", trend) }
	if trend[1].MinuteOfDay != 10 || trend[1].ByModel["deepseek-chat"].CostNanoUSD != 1_200_000 {
		t.Fatalf("bucket = %+v", trend[1])
	}

	top, err := st.TopRequests(ctx, "2026-10-29", "2026-10-29", "cost", 10)
	if err != nil || len(top) != 2 || top[0].CostNanoUSD < top[1].CostNanoUSD {
		t.Fatalf("top cost = %+v err=%v", top, err)
	}
	byTokens, err := st.TopRequests(ctx, "2026-10-29", "2026-10-29", "tokens", 1)
	if err != nil || len(byTokens) != 1 || byTokens[0].TotalTokens != 300 { t.Fatalf("top tokens=%+v", byTokens) }
	if _, err := st.TopRequests(ctx, "2026-10-29", "2026-10-29", "bogus", 10); err == nil {
		t.Fatal("invalid by must error")
	}
}

func TestAnalysisHealthAndCache(t *testing.T) {
	st := seedAnalysisStore(t)
	ctx := context.Background()
	h, err := st.AnalysisHealth(ctx, "2026-10-29", "2026-10-29")
	if err != nil { t.Fatal(err) }
	if h.Requests != 3 || h.Errors != 1 || h.Unmetered != 1 || h.TooLarge != 1 ||
		h.ClientDisconnected != 1 || h.P95MS == nil || h.CountOver != 0 {
		t.Fatalf("health = %+v", h)
	}
	c, err := st.CacheStats(ctx, "2026-10-29", "2026-10-29")
	if err != nil { t.Fatal(err) }
	// 1000 cached tokens, rates input 0.28 / cache 0.028 => 252000 nano
	if c.SavedNanoUSD == nil || *c.SavedNanoUSD != 252_000 || c.CacheReadTokens != 1000 {
		t.Fatalf("cache = %+v", c)
	}
	obs, err := st.ObservedModels(ctx, "2026-10-29")
	if err != nil || len(obs) != 2 || obs[0].Model == "" { t.Fatalf("observed=%+v err=%v", obs, err) }
}

func TestRequestsStreamFilter(t *testing.T) {
	st := seedAnalysisStore(t)
	yes, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Stream: ptr(true), Limit: 10})
	if err != nil || len(yes) != 1 || !yes[0].Stream { t.Fatalf("stream=true = %+v", yes) }
	no, err := st.Requests(store.RequestFilter{From: "2026-10-29", To: "2026-10-29", Stream: ptr(false), Limit: 10})
	if err != nil || len(no) != 2 { t.Fatalf("stream=false = %+v", no) }
}
```

> `seedAnalysisStore` 写入 3 条明细：流式 ok（带 `rates_json`，cacheRead=1000）、非流式 ok（带 `rates_json`）、非流式 status 500 + `usage_status=too_large` + `client_disconnected=1`（无 `rates_json`）；对应 `traffic_minutes` 两个小时桶。`rates_json` 字符串示例：
> `{"model":"m","window":"base","input_per_mtok":0.28,"output_per_mtok":0.42,"cache_read_per_mtok":0.028,"cache_write_per_mtok":0,"reasoning_per_mtok":0.42}`。
> `ptr` 用泛型 helper 或 `func ptr[T any](v T) *T`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/store -run 'TestTrendByModelAndTop|TestAnalysisHealthAndCache|TestRequestsStreamFilter' -v`
Expected: FAIL（类型/方法未定义）

- [ ] **Step 3: 实现**

- `TrendByModel`：
  - minute：`bucket = minute_of_day`；hour：`bucket = minute_of_day / 60`；day：`bucket = 0`，只按 `local_day` 分组。
  - SQL 与 `Series` 一样从 `traffic_minutes` 聚合，额外 `GROUP BY model`；结果按 `(day,bucket)` 分组，`ByModel` 非 nil。
- `TopRequests`：`by` 白名单 → `cost_nano_usd`、`total_tokens`、`latency_ms`；`ORDER BY <col> DESC, id DESC LIMIT ?`；扫描字段与 DTO 一致。
- `AnalysisHealth`：
  - `requests/errors` 来自 `traffic_minutes`；`unmetered/tooLarge/clientDisconnected/countOver` 来自 `requests`（`usage_status NOT IN ('ok','not_applicable')`、`usage_status='too_large'`、`client_disconnected=1`、`latency_ms > 2000`）。
  - `P95MS` 复用 `latencyBuckets` 的累计桶；无明细返回 nil；`ErrorRate = errors/requests`。
- `CacheStats`：`prompt/cacheRead` 来自 `traffic_minutes`；`saved` 扫描 `requests.cache_read_tokens, rates_json`，解析 `input_per_mtok`/`cache_read_per_mtok`，累加 `cacheRead × max(input−cacheRead,0) × 1000`，最后 `math.Round`；无 priced 记录时 `SavedNanoUSD` 指向 0。
- `ObservedModels`：`SELECT model, COUNT(*), MAX(started_at) FROM requests WHERE model<>'' AND local_day>=? GROUP BY model ORDER BY COUNT(*) DESC`；`started_at` 为 RFC3339 文本，扫描后 `time.Parse(time.RFC3339, ...)`。
- `TopRequests` 的 `started_at` 同样按 RFC3339 解析；`ttft_ms` 为 NULL 时指针为 nil。
- `RequestFilter.Stream *bool`；SQL 追加 `stream = ?`；API 解析 `stream`（`strconv.ParseBool`，非法 400）。
- 新增查询全部用 `QueryContext`，错误用 `%w` 包装。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/store ./internal/api -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store internal/api/read.go internal/api/read_test.go
git commit -m "feat(store): analysis queries and stream filter"
```

---

### Task 2: 模型批量导入（Repo + API）

**Files:**
- Modify: `internal/settings/models.go`
- Modify: `internal/settings/settings_test.go`
- Modify: `internal/api/config.go`
- Modify: `internal/api/config_test.go`

**Interfaces:**
- Consumes: `Repo`、`pricing.Model`
- Produces:

```go
func (r *Repo) ImportModels(ctx context.Context, names []string) (created []pricing.Model, skipped []string, err error)
```

- [ ] **Step 1: 写失败的 Repo 测试**

```go
func TestImportModelsCreatesMissingZeroRates(t *testing.T) {
	repo := newTestRepo(t)
	if _, err := repo.CreateModel(context.Background(), settings.ModelInput{Name: "existing", Input: 1}); err != nil {
		t.Fatal(err)
	}
	created, skipped, err := repo.ImportModels(context.Background(),
		[]string{" new-a ", "existing", "new-a", "new-b"})
	if err != nil { t.Fatal(err) }
	if len(created) != 2 || created[0].Name != "new-a" || skipped[0] != "existing" {
		t.Fatalf("created=%+v skipped=%v", created, skipped)
	}
	if created[0].Input != 0 || created[0].Output != 0 || created[0].Reasoning != nil {
		t.Fatalf("rates = %+v", created[0])
	}
	if _, _, err := repo.ImportModels(context.Background(), make([]string, 101)); !errors.As(err, new(*settings.ValidationError)) {
		t.Fatalf("limit err = %v", err)
	}
	if _, _, err := repo.ImportModels(context.Background(), []string{"   ", ""}); err == nil {
		t.Fatal("empty names must error")
	}
}
```

- [ ] **Step 2: 写失败的 API 测试**

```go
func TestImportModelsEndpoint(t *testing.T) {
	srv := newAPIServer(t, true)
	rec := doPOST(t, srv, "/api/models/import", `{"names":["m1","m1"]}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"created":[`) {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	rec = doPOST(t, srv, "/api/models/import", `{"names":[]}`)
	if rec.Code != 400 { t.Fatalf("empty = %d", rec.Code) }
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/settings ./internal/api -run 'TestImportModels' -v`
Expected: FAIL

- [ ] **Step 4: 实现**

- `ImportModels`：`strings.TrimSpace`，丢弃空项；去重（保留首次顺序）；`len(names) > 100` 或去重后为空 → `*ValidationError{Fields:{"names":...}}`；名字长度 >200 → validation。
- 事务内查询已存在名字（`WHERE name IN (...)` 分批或逐条），只插入缺失项，费率 0、`reasoning=NULL`；插入后 `Refresh()`；返回 created（`pricing.Model`）与 skipped（输入顺序）。
- API（`config.go`）：`POST /api/models/import` 注册在 `server.go` 受保护区；请求 `{"names":[...]}`；调用 Repo；200 `{"created":[{"id","name"}],"skipped":[...]}`；`*ValidationError` → 400 fields，其他 → 500。响应不返回费率快照细节。
- 在 `server.go` 增加路由：`mux.Handle("POST /api/models/import", require(s.importModels))`。

- [ ] **Step 5: 运行测试**

Run: `go test ./internal/settings ./internal/api -race -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/settings internal/api
git commit -m "feat: batch import zero-rate models"
```

---

### Task 3: 模型目录服务与接口

**Files:**
- Create: `internal/api/catalog.go`
- Create: `internal/api/catalog_test.go`
- Modify: `internal/api/server.go`（`catalog` 字段、`New` 初始化、路由）
- Modify: `internal/api/config.go`（setup / patch settings 成功后 `s.catalog.invalidate()`）

**Interfaces:**
- Consumes: `settings.Repo`、`store.Store`、`server.now`、`store.ObservedModels`
- Produces:

```go
type catalogModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"ownedBy,omitempty"`
}
type catalogObserved struct {
	Model    string    `json:"model"`
	Requests int64     `json:"requests"`
	LastSeen time.Time `json:"lastSeen"`
}
type catalogBody struct {
	FetchedAt     *time.Time         `json:"fetchedAt"`
	Upstream      []catalogModel     `json:"upstream"`
	Observed      []catalogObserved  `json:"observed"`
	UpstreamError string             `json:"upstreamError"`
}
func (s *Server) modelCatalog(w http.ResponseWriter, r *http.Request)
```

- [ ] **Step 1: 写失败的测试**

```go
func TestModelCatalogFallsBackAndCaches(t *testing.T) {
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path == "/models" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" { http.NotFound(w, r); return }
		if r.Header.Get("Authorization") != "Bearer sk-up" { t.Errorf("key") }
		io.WriteString(w, `{"data":[{"id":"deepseek-chat","owned_by":"deepseek"},{"id":"m2"}]}`)
	}))
	defer upstream.Close()

	srv := newAPIServerWithUpstream(t, upstream.URL) // Setup + 一条 observed 明细
	rec := doGET(t, srv, "/api/model-catalog")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deepseek-chat"`) {
		t.Fatalf("catalog = %d %s", rec.Code, rec.Body.String())
	}
	doGET(t, srv, "/api/model-catalog")
	if atomic.LoadInt32(&hits) != 2 { t.Fatalf("cache miss: hits=%d", hits) } // 404 + 200 一次
	doGET(t, srv, "/api/model-catalog?refresh=1")
	if atomic.LoadInt32(&hits) != 4 { t.Fatalf("refresh hits=%d", hits) }
}

func TestModelCatalogFailureStillReturnsObserved(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom: sk-up", 500)
	}))
	defer upstream.Close()
	srv := newAPIServerWithUpstream(t, upstream.URL)
	rec := doGET(t, srv, "/api/model-catalog")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "sk-up") ||
		!strings.Contains(rec.Body.String(), `"upstreamError"`) {
		t.Fatalf("failure = %d %s", rec.Code, rec.Body.String())
	}
}
```

> 测试 helper 需允许设置上游 URL 并写入一条 observed 请求；缓存断言依赖 5 分钟 TTL 与 `refresh`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api -run 'TestModelCatalog' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

- `catalog` 结构：`repo`、`store`、`now`、`mu`、`cached catalogBody`、`fetchedAt time.Time`、`client *http.Client`（5s timeout）。
- `fetch(ctx)`：
  - snapshot → baseURL/key；空则 `upstreamError = "upstream not configured"`。
  - endpoints：`base+"/models"`；若 base 不以 `/v1` 结尾，追加 `base+"/v1/models"`。
  - GET，`Authorization: Bearer <key>`；404/405 尝试下一个；其他错误停止。
  - 解析 `{"data":[{"id","owned_by"}]}`；id 为空跳过；错误文本经 `sanitizeLogError` 类似处理，绝不包含 key。
  - 成功 `FetchedAt = now()`；失败 `FetchedAt` 为零、`Upstream` 空。
- `get(ctx, refresh)`：缓存 5 分钟内直接返回；否则 fetch；observed 每次从 `store.ObservedModels(ctx, cutoffDay)` 读取（cutoff = 今天 − retentionDays）。
- `invalidate()`：清空缓存（加锁）。
- handler：`refresh` 解析 bool；`get`；`writeJSON(200, body)`；observed 为空返回 `[]` 不是 null；upstream 为空返回 `[]`。
- 注册路由：`GET /api/model-catalog` 在 `require` 下。
- setup 与 `patchSettings` 成功写入后调用 `s.catalog.invalidate()`；测试可选断言。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "feat(api): model catalog from upstream and observed traffic"
```

---

### Task 4: 分析聚合接口

**Files:**
- Create: `internal/api/analysis.go`
- Create: `internal/api/analysis_test.go`
- Modify: `internal/api/server.go`（路由）

**Interfaces:**
- Consumes: `Store.TrendByModel/TopRequests/AnalysisHealth/CacheStats`、`rangeQuery`、`daySpan`、`partialRange`、`Interval`
- Produces: `GET /api/analysis` 响应，字段严格按 spec §4.2

- [ ] **Step 1: 写失败的测试**

```go
func TestAnalysisEndpoint(t *testing.T) {
	srv := newAPIServer(t, true) // 含 Task 1 的种子数据
	rec := doGET(t, srv, "/api/analysis?from=2026-10-29&to=2026-10-29")
	if rec.Code != 200 { t.Fatalf("analysis = %d %s", rec.Code, rec.Body.String()) }
	var body struct {
		Range  struct{ Interval string; Partial bool } `json:"range"`
		Trend  []map[string]any `json:"trend"`
		Top    struct{ Cost []map[string]any `json:"cost"` } `json:"topRequests"`
		Health struct{ Requests int64; TooLarge int64 } `json:"health"`
		Cache  struct{ Available bool; SavedNanoUSD *int64 `json:"savedNanoUsd"` } `json:"cache"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil { t.Fatal(err) }
	if body.Range.Interval != "minute" || len(body.Trend) == 0 || len(body.Top.Cost) == 0 {
		t.Fatalf("body = %+v", body)
	}
	if body.Health.Requests != 3 || body.Cache.SavedNanoUSD == nil || *body.Cache.SavedNanoUSD != 252_000 {
		t.Fatalf("health/cache = %+v", body)
	}
}

func TestAnalysisPartialDisablesDetailViews(t *testing.T) {
	srv := newAPIServer(t, true)
	rec := doGET(t, srv, "/api/analysis?from=2020-01-01&to=2020-01-02&interval=day")
	if rec.Code != 200 { t.Fatalf("code = %d", rec.Code) }
	var body struct {
		Range struct{ Partial bool } `json:"range"`
		Top   struct{ Cost []any `json:"cost"` } `json:"topRequests"`
		Cache struct{ Available bool; SavedNanoUSD *int64 `json:"savedNanoUsd"` } `json:"cache"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if !body.Range.Partial || len(body.Top.Cost) != 0 || body.Cache.Available || body.Cache.SavedNanoUSD != nil {
		t.Fatalf("partial = %+v", body)
	}
}

func TestAnalysisIntervalValidation(t *testing.T) {
	srv := newAPIServer(t, true)
	rec := doGET(t, srv, "/api/analysis?from=2026-01-01&to=2026-10-29&interval=minute")
	if rec.Code != 400 { t.Fatalf("code = %d", rec.Code) }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/api -run 'TestAnalysis' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

- 解析 `from/to` 用 `rangeQuery`；`interval`：`auto|minute|hour|day`，auto 规则（≤2 天 minute、≤7 天 hour、否则 day）；显式 minute ≤2 天、hour ≤90 天，否则 400。
- `detailOK := !s.partialRange(from)`。
- 5 秒 `context.WithTimeout`；所有新 store 方法带 ctx；超时 → 500 internal。
- 组装：
  - `trend`：`TrendByModel` 转 DTO；`byModel` 非 nil。
  - `models`/`paths`：调用 `Breakdown`（无 ctx；若需要可复用现有方法）并附 `unpriced`；partial 时 UI 标注，值本身来自保留期。
  - `topRequests`：detailOK 时分别调用 `TopRequests(ctx, from,to,"cost"/"tokens"/"latency",10)`；否则三个空数组。
  - `health`：`AnalysisHealth`；partial 时 `slow.p95Ms=null`（保留 countOver 来自保留期）。
  - `cache`：partial 时仍调用 `CacheStats` 取 `promptTokens/cacheReadTokens/hitRate`（来自永久聚合），但把 `available=false`、`savedNanoUsd=null`；非 partial 返回完整结果并 `available=true`。
  - `range.partial`。
- 注册 `GET /api/analysis` 在 `require` 下；错误用 `writeInternal`。
- DTO 字段名与 spec 完全一致（`topRequests`, `savedNanoUsd`, `latencyMs`, `ttftMs`, `usageStatus`）。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "feat(api): analysis aggregate endpoint"
```

---

### Task 5: 前端类型、客户端与纯函数

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/api/client.ts`
- Create: `web/src/lib/speed.ts`
- Create: `web/src/lib/speed.test.ts`
- Create: `web/src/lib/usage.ts`
- Create: `web/src/lib/usage.test.ts`
- Modify: `web/src/lib/format.ts`（新增 `formatRate`）

**Interfaces:**
- Consumes: 现有 `api` 模式
- Produces:

```ts
export interface CatalogModel { id: string; ownedBy?: string }
export interface CatalogObserved { model: string; requests: number; lastSeen: string }
export interface ModelCatalog { fetchedAt: string | null; upstream: CatalogModel[]; observed: CatalogObserved[]; upstreamError: string }
export interface TrendModel { requests: number; promptTokens: number; completionTokens: number; costNanoUsd: number; upBytes: number; downBytes: number }
export interface AnalysisRow { key: string; requests: number; errors: number; upBytes: number; downBytes: number; promptTokens: number; completionTokens: number; cacheReadTokens: number; costNanoUsd: number; unpriced: number }
export interface AnalysisTrendBucket { day: string; minuteOfDay: number; byModel: Record<string, TrendModel> }
export interface AnalysisTopRequest { id: number; startedAt: string; model: string; pathClass: string; stream: boolean; promptTokens: number; completionTokens: number; cacheReadTokens: number; totalTokens: number; costNanoUsd: number; latencyMs: number; ttftMs: number | null; status: number; usageStatus: string }
export interface AnalysisBody { range: { from: string; to: string; interval: "minute"|"hour"|"day"; partial: boolean }; trend: AnalysisTrendBucket[]; models: AnalysisRow[]; paths: AnalysisRow[]; topRequests: { cost: AnalysisTopRequest[]; tokens: AnalysisTopRequest[]; latency: AnalysisTopRequest[] }; health: { requests: number; errors: number; errorRate: number; unmetered: number; tooLarge: number; clientDisconnected: number; slow: { thresholdMs: number; p95Ms: number | null; countOver: number } }; cache: { promptTokens: number; cacheReadTokens: number; hitRate: number; savedNanoUsd: number | null; available: boolean } }
export function tokensPerSecond(completionTokens: number, latencyMs: number, ttftMs: number | null, stream: boolean): number | null
export function usageLabel(status: string): string
export function usageTone(status: string): "success" | "neutral" | "info" | "warning" | "danger"
// lib/format.ts
export function formatRate(value: number | null): string
```

- `api` 新增：`getModelCatalog(refresh?)`、`getAnalysis({from,to,interval})`、`importModels(names)`；`getRequests` 参数增加 `stream?: boolean`。

- [ ] **Step 1: 写失败的纯函数测试**

```ts
import { describe, expect, it } from "vitest";
import { tokensPerSecond } from "./speed";
import { usageLabel } from "./usage";
import { formatRate } from "./format";

describe("tokensPerSecond", () => {
  it("computes stream generation speed", () => {
    expect(tokensPerSecond(710, 6000, 500, true)).toBeCloseTo(129.09, 1);
  });
  it("returns null for non-stream, missing ttft, or non-positive generation", () => {
    expect(tokensPerSecond(710, 6000, 500, false)).toBeNull();
    expect(tokensPerSecond(710, 6000, null, true)).toBeNull();
    expect(tokensPerSecond(710, 500, 500, true)).toBeNull();
    expect(tokensPerSecond(0, 6000, 500, true)).toBeNull();
  });
  it("clamps generation to 1ms", () => {
    expect(tokensPerSecond(1, 10, 9.5, true)).toBeCloseTo(1000, 1);
  });
});

describe("usageLabel", () => {
  it("maps too_large and unknown", () => {
    expect(usageLabel("too_large")).toBe("未解析（响应过大）");
    expect(usageLabel("weird")).toBe("weird");
  });
});

describe("formatRate", () => {
  it("formats and handles null", () => {
    expect(formatRate(null)).toBe("—");
    expect(formatRate(129.09)).toBe("129.1 t/s");
  });
});
```

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/lib/speed.test.ts src/lib/usage.test.ts`
Expected: FAIL

- [ ] **Step 3: 实现**

- `speed.ts`：条件与公式严格按 Global Constraints；返回原始 number。`format.ts` 新增 `formatRate(value)`：null → `—`，否则 `value.toFixed(1) + " t/s"`；补一条 format 测试。
- `usage.ts`：六种状态文案与 tone；`too_large` → warning，`not_applicable` → info，`no_usage` → neutral，`parse_failed/error` → danger。
- `client.ts`：新增方法，URL 参数用 `URLSearchParams`；`stream` 只在非 undefined 时追加；`refresh` 用 `refresh=1`。
- `types.ts`：补齐 Catalog/Analysis 类型；`RequestsFilters` 增加可选 `stream?: "all"|"stream"|"nonstream"`，Task 8 的 `buildRequestsQuery` 使用它。

- [ ] **Step 4: 运行测试与类型检查**

Run: `cd web && npm test && npx tsc -b`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/api web/src/lib
git commit -m "feat(web): analysis and catalog types, speed helpers"
```

---

### Task 6: AppShell、路由与共享组件

**Files:**
- Rewrite: `web/src/components/Layout.tsx` → 保留文件名，导出 `Layout`
- Create: `web/src/components/PageHeader.tsx`
- Create: `web/src/components/RangeBar.tsx`
- Create: `web/src/components/RequestDrawer.tsx`
- Create: `web/src/components/EmptyState.tsx`
- Create: `web/src/components/ErrorBanner.tsx`
- Create: `web/src/components/Skeleton.tsx`
- Create: `web/src/components/DataTable.tsx`
- Modify: `web/src/App.tsx`（路由、重定向）
- Modify: `internal/api/routing.go`（SPA 路由表）
- Modify: `internal/api/routing_test.go`

**Interfaces:**
- Consumes: `lib/range`、`api.getRequest`、`ui.tsx`、`useLive`
- Produces:

```tsx
export function PageHeader(props: { title: string; description?: string; actions?: ReactNode }): ReactElement
export function RangeBar(props: { value: RangeSelection; onChange: (next: RangeSelection) => void }): ReactElement
export function RequestDrawer(props: { requestId: number | null; onClose: () => void }): ReactElement
export function DataTable<T>(props: { columns: Column<T>[]; rows: T[]; rowKey: (row: T) => string; onRowClick?: (row: T) => void; empty?: ReactNode }): ReactElement
```

- [ ] **Step 1: 写失败的路由测试**

在 `internal/api/routing_test.go` 的用例表中增加：

```go
{"/logs", "spa"},
{"/requests", "spa"},
```

Run: `go test ./internal/api -run TestRootRouting -v`
Expected: FAIL（`/logs` 落到 gateway）

- [ ] **Step 2: 实现路由表与 App 路由**

- `routing.go` 的 SPA 列表只加入 `/logs`、`/requests`；`/analysis` 留到 Task 9，`/settings/pricing`、`/settings/data` 留到 Task 10。
- `App.tsx`：
  - `/` → Overview；`/logs` 暂时指向现有 `Requests` 页面（Task 8 替换为 `Logs`）；
  - `/settings` 暂时保持现有 `Settings` 页面，Task 10 再改为嵌套子页；
  - `/requests` → `<Navigate to="/logs" replace />` 并保留 search；`/pricing` 暂时重定向 `/settings`（Task 10 改为 `/settings/pricing`）；
  - 不新增 `/analysis` 路由；Task 9 再接入。
- 路由结构不要嵌套出双 Layout。

- [ ] **Step 3: 实现 AppShell 与共享组件**

- `Layout`：4 个 NavLink；实时点；时区/版本；移动端顶栏；内容区 `max-w-[1440px]`。
- `RangeBar`：调用现有 `presetRange`/`parseRangeSelection` 逻辑；按钮组 + 自定义日期输入；不自带数据请求。
- `PageHeader`：标题、描述、右侧 actions。
- `RequestDrawer`：从现有 `Requests.tsx` 抽出 `RequestDetail`；`useQuery({queryKey:["request",id], queryFn: id ? () => api.getRequest(id) : skipToken})`；展示 spec §3.2 五个分区，头部/响应头标「估算」，含 tokens/s。
- `DataTable`：泛型列定义 `{key, header, className?, render(row)}`；表头吸顶、数字右对齐由 className 控制；空态插槽。
- `EmptyState`/`ErrorBanner`/`Skeleton`：统一空/错/加载样式。
- 所有按钮/输入复用 `ui.tsx`，不新增第三方依赖。

- [ ] **Step 4: 运行测试与构建**

Run: `go test ./internal/api -run TestRootRouting -v && cd web && npm run build`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api/routing.go internal/api/routing_test.go web/src
git commit -m "feat(web): app shell, routes and shared request drawer"
```

---

### Task 7: 总览页重排

**Files:**
- Rewrite: `web/src/pages/Overview.tsx`
- Create: `web/src/components/LiveStrip.tsx`
- Modify: `web/src/components/StatCard.tsx`（如需要密度/环比样式）
- Test: `web/src/lib/range.test.ts`（补充自动间隔边界）

**Interfaces:**
- Consumes: `api.getOverview/getSeries/getBreakdown`、`useLive`、`RangeBar`、`PageHeader`、`StatCard`、ECharts wrapper
- Produces: 无新导出

- [ ] **Step 1: 补充失败的 range 测试**

```ts
it("auto interval boundaries", () => {
  expect(autoInterval(2)).toBe("minute");
  expect(autoInterval(3)).toBe("hour");
  expect(autoInterval(7)).toBe("hour");
  expect(autoInterval(8)).toBe("day");
});
```

Run: `cd web && npx vitest run src/lib/range.test.ts`
Expected: PASS（现有实现）或 FAIL（若边界不一致，先修 helper）

- [ ] **Step 2: 实现实时条与区间统计分离**

- `LiveStrip`：6 个实时指标 + 60s sparkline（复用现有 `buildSparkOption`）；不渲染 `RangeBar`；只在 snapshot 有数据时更新，不闪骨架。
- 总览结构：`PageHeader` → `LiveStrip` → section header「区间统计」+ `RangeBar` → 4 KPI → 趋势图 → 模型分布 Top 5。
- 4 KPI：花费、Tokens、请求、流量；请求卡显示错误/未解析；`partial` 显示「仅保留期内」。
- 趋势：现有 series 查询与 metric 切换；保持 URL `range`/`metric`。
- 模型分布：breakdown by model 排序取前 5；点击行 `navigate("/analysis?model=...&from=...&to=...")`。
- 删除总览里重复的旧卡片（P95/上下行独立卡）或合并进流量/请求卡；不再显示旧「区间统计」上方的筛选。

- [ ] **Step 3: 运行前端测试与构建**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/Overview.tsx web/src/components
git commit -m "feat(web): rework overview live and range sections"
```

---

### Task 8: 日志页（newapi 风格）

**Files:**
- Create: `web/src/pages/Logs.tsx`
- Rewrite: `web/src/components/Filters.tsx`（增加流式筛选；输出 URL 参数）
- Create: `web/src/lib/query.ts` 更新（`stream` 参数；`buildRequestsQuery` 测试）
- Delete: `web/src/pages/Requests.tsx`（抽屉已抽到 Task 6）
- Modify: `web/src/lib/query.test.ts`

**Interfaces:**
- Consumes: `api.getRequests`、`RequestDrawer`、`DataTable`、`RangeBar`、`tokensPerSecond`、`usageLabel`
- Produces: `RequestsFilters` 增加 `stream: "all"|"stream"|"nonstream"`

- [ ] **Step 1: 写失败的查询串测试**

```ts
it("encodes stream filter", () => {
  expect(buildRequestsQuery({ from: "a", to: "b", stream: "stream" }))
    .toContain("stream=true");
  expect(buildRequestsQuery({ from: "a", to: "b", stream: "nonstream" }))
    .toContain("stream=false");
  expect(buildRequestsQuery({ from: "a", to: "b", stream: "all" }))
    .not.toContain("stream=");
});
```

- [ ] **Step 2: 实现 Logs 页**

- 筛选：范围、模型、路径类、状态码、用量状态、流式、定价；全部写 URL；变化重置 `before_id`。
- 表格列按 spec §3.2；模型列含 `流式`/`非流式` 徽标；Token 两行；耗时两行；速度列用 `tokensPerSecond` + `formatRate`；状态列状态码 + usage 徽标。
- 行点击设 `selectedId`，渲染共享 `RequestDrawer`；抽屉关闭清除。
- 窄屏（≤768px）改为卡片列表，字段同表格。
- 空态、错误态、加载骨架；`too_large` 徽标带 title/说明。
- 删除旧 `Requests.tsx`；`App.tsx` 的 `/logs` 从旧 `Requests` 改为新的 `Logs`。

- [ ] **Step 3: 运行测试与构建**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add web/src
git commit -m "feat(web): newapi-style logs with stream and speed"
```

---

### Task 9: 分析页

**Files:**
- Create: `web/src/pages/Analysis.tsx`
- Create: `web/src/components/charts/StackedTrend.tsx`（或扩展现有 charts.tsx）
- Modify: `web/src/lib/format.ts`（如需 `formatPercent`）
- Test: `web/src/lib/range.test.ts` / `format.test.ts`（百分比与间隔）

**Interfaces:**
- Consumes: `api.getAnalysis`、`RangeBar`、`DataTable`、`RequestDrawer`、ECharts
- Produces: 无；`App.tsx` 增加 `/analysis` 路由，`internal/api/routing.go` 增加 `/analysis` SPA 路由

- [ ] **Step 1: 写失败的百分比测试**

```ts
it("formats error rate and cache hit", () => {
  expect(formatPercent(0.0083)).toBe("0.83%");
  expect(formatPercent(0.444)).toBe("44.4%");
  expect(formatPercent(0)).toBe("0%");
});
```

Run: `cd web && npx vitest run src/lib/format.test.ts`
Expected: FAIL

- [ ] **Step 2: 实现分析页**

- 路由：`App.tsx` 注册 `/analysis` → `Analysis`；`routing.go` SPA 列表加入 `/analysis`，`routing_test.go` 增加用例。
- 查询：`api.getAnalysis({from,to,interval:auto})`；键含范围；`keepPreviousData`。
- 趋势：ECharts 堆叠柱/面积，x 轴桶，series 每模型；指标切换（花费/Tokens/流量/请求）；图例点击高亮，点击系列跳 `/logs?model=...&from&to`。
- Top：三 tab（花费/tokens/耗时）；表格列时间、模型、流式、tokens、费用、总耗时；行点击开 `RequestDrawer`。
- 模型/路径排名：DataTable，可排序；点击跳日志加筛选。
- 异常：请求/错误率/未计量/未解析/断连/慢请求/P95；点击未解析跳日志 `usageStatus=too_large`。
- 缓存：命中率、缓存读 tokens、估算节省；`available=false` 显示不可用说明；`savedNanoUsd` 用 `formatCost`。
- partial：Top/缓存区块显示「超出明细保留期」禁用态；趋势/排名仍展示。
- 空态与错误态。

- [ ] **Step 3: 运行测试与构建**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add web/src
git commit -m "feat(web): analysis trends, top lists and cache stats"
```

---

### Task 10: 设置壳、上游与数据页

**Files:**
- Create: `web/src/pages/settings/SettingsLayout.tsx`
- Create: `web/src/pages/settings/Gateway.tsx`
- Create: `web/src/pages/settings/Data.tsx`
- Delete: `web/src/pages/Settings.tsx`
- Modify: `web/src/App.tsx`
- Test: `web/src/lib/settings.test.ts`（保留/补充清除密码与保留期校验）

**Interfaces:**
- Consumes: `api.getSettings/patchSettings/rotateKey/testUpstream/logout`、`maskValue`
- Produces: 子页路由 `/settings`、`/settings/pricing`、`/settings/data`

- [ ] **Step 1: 实现设置壳**

- `SettingsLayout`：`PageHeader` + 子导航（NavLink）+ `<Outlet />`；移动端横向滚动。
- `App.tsx` 设置路由改为嵌套：index `Gateway`、`pricing` 暂时指向旧 `Pricing` 页面（Task 11 替换为 `settings/Pricing`）、`data` → `Data`。
- `routing.go` SPA 列表加入 `/settings/pricing`、`/settings/data`；`routing_test.go` 增加这两条用例。
- `/pricing` 重定向改为 `/settings/pricing`。
- 运行 `go test ./internal/api -run TestRootRouting -v` 确认三条设置路由都由 SPA 接管。

- [ ] **Step 2: Gateway 页**

- 上游表单（baseUrl、key 掩码/替换、timeout、测试连接）。
- 时区 select、明细保留天数（与 Data 页共享字段与校验）。
- 管理密码：设置/修改、清除（当前密码、回环提示）、退出登录。
- Gateway Key：掩码、轮换二次确认、一次性明文展示。
- 有效监听 + 重启提示。
- 保存：`patchSettings`；失败显示 `ApiError.fields`；成功 toast + invalidate `settings/bootstrap`。
- 保留现有密码清空后的登录失效行为与提示。

- [ ] **Step 3: Data 页**

- 只读：DB 路径、体积、版本、有效监听、服务端时区。
- 明细保留天数编辑（保存后 invalidate settings），并提示聚合永久保留。
- 不提供导出/清理按钮。

- [ ] **Step 4: 运行测试与构建**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src
git commit -m "feat(web): settings shell, gateway and data pages"
```

---

### Task 11: 价格页（目录 + 可选缓存写价）

**Files:**
- Create: `web/src/pages/settings/Pricing.tsx`
- Delete: `web/src/pages/Pricing.tsx`
- Modify: `web/src/lib/pricing.ts`
- Modify: `web/src/lib/pricing.test.ts`
- Modify: `web/src/App.tsx`（导入新页面）

**Interfaces:**
- Consumes: `api.getModels/getModelCatalog/createModel/updateModel/deleteModel/putWindows/importModels`、`validateWindows`
- Produces: `parseRateInput(input: string, opts: { required: boolean }): number | null`（定义在 `web/src/lib/pricing.ts`）

- [ ] **Step 1: 写失败的缓存写价测试**

```ts
it("treats blank cache write as zero", () => {
  expect(parseRateInput("", { required: false })).toBe(0);
  expect(parseRateInput("0", { required: false })).toBe(0);
  expect(parseRateInput("0.02", { required: false })).toBe(0.02);
  expect(parseRateInput("", { required: true })).toBeNull();
});
```

- [ ] **Step 2: 实现价格页**

- 顶部：刷新上游按钮 + 上次拉取时间 + `upstreamError` 提示；查询 `getModelCatalog`，`refresh` 触发 `?refresh=1`。
- 目录合并：`getModels` + catalog；每行标记来源（已配置/上游/已观测），未配置高亮；0 费率模型标 `0 价（待填）`。
- `lib/pricing.ts` 新增 `parseRateInput`：`trim()` 为空时 `required ? null : 0`；非空 `Number()`，`NaN/负/Infinity` 返回 null。
- 添加/编辑抽屉：
  - 模型名 combobox：datalist 来自目录；允许手填。
  - 基础价：输入、输出、缓存读必填；缓存写留空 → 0；reasoning 留空 → null。
  - 窗口编辑器保留现有规则与上移/下移；保存前 `validateWindows`。
  - 保存：新模型 `createModel`（含 cacheWrite 0）+ `putWindows`；编辑 `updateModel` + `putWindows`；失败按字段显示。
- 一键补录：对未配置模型调用 `importModels(names)`；确认后刷新列表；显示 created/skipped。
- 删除二次确认；保存后 invalidate `models`/`model-catalog`，toast「已生效」。
- 空目录 / 上游失败 / 无窗口状态。

- [ ] **Step 3: 运行测试与构建**

Run: `cd web && npm test && npm run build`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add web/src
git commit -m "feat(web): pricing catalog and optional cache-write rate"
```

---

### Task 12: 文档、端到端与冒烟

**Files:**
- Modify: `README.md`
- Modify: `internal/e2e/e2e_test.go`
- Create: `docs/superpowers/specs` 无（spec 已存在）
- Test: 全量

**Interfaces:**
- Consumes: 全部
- Produces: 可复现的验收记录

- [ ] **Step 1: 扩展 e2e**

在现有 e2e 中追加：

```go
// 目录接口：上游失败也必须返回 observed
rec = httptest.NewRecorder()
root.ServeHTTP(rec, httptest.NewRequest("GET", "/api/model-catalog", nil))
if rec.Code != 200 { t.Fatalf("catalog = %d %s", rec.Code, rec.Body.String()) }

// 分析接口：请求完成后应有 trend/top
rec = httptest.NewRecorder()
root.ServeHTTP(rec, httptest.NewRequest("GET", "/api/analysis?from=2026-10-29&to=2026-10-29", nil))
if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"trend"`) {
	t.Fatalf("analysis = %d %s", rec.Code, rec.Body.String())
}
```

- [ ] **Step 2: README 更新**

- 导航/页面截图描述（文字）与路由：`/logs`、`/analysis`、`/settings/pricing`。
- 新 API：`/api/model-catalog`、`/api/analysis`、`/api/models/import`、`?stream=`。
- `tokens/s` 公式与只限流式的说明；缓存节省公式与保留期限制；`too_large` 文案。
- 价格目录来源、缓存写价可留空、0 价补录语义。
- 保留现有网关/鉴权/字节/保留期/限制章节，删除过期 YAML 引用。

- [ ] **Step 3: 全量验证**

```bash
cd web && npm ci && npm test && npm run build
cd .. && go vet ./... && go test ./... -race && go build -o /tmp/llmmeter-ui .
```

Expected: 全绿。

- [ ] **Step 4: 浏览器冒烟**

用假上游与临时 DB 启动，完成 setup，发送流式/非流式请求；核对：

- 总览实时条更新、区间筛选只在区间统计内；
- 日志表显示流式徽标、token 拆分、首字/总耗时、t/s；
- 分析四区块有数据，Top 行开抽屉，模型点击跳日志；
- 价格目录能刷新上游、缓存写留空可保存、补录 0 价后列表标记；
- 设置子页、改密/清密、轮换、测试连接；
- 窄屏（≤480px）无横向溢出。

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "docs: document UI v3 and extend end-to-end coverage"
```

---

## 自审记录

- 5 秒超时覆盖新增的 Trend/Top/Health/Cache 查询；`models`/`paths` 复用现有 `Breakdown`（无 ctx），受 90 天区间上限约束，若后续需要严格取消再加 ctx 变体。
- Spec 覆盖：导航/路由（Task 6、10）、总览（Task 7）、日志（Task 8）、分析（Task 1、4、9）、价格（Task 2、3、11）、视觉（Task 6、7、8）、设置（Task 10）、验收（Task 12）。
- 已知取舍：分析 `models/paths` 复用现有 `Breakdown`（无 ctx），趋势/Top/健康用新 ctx 方法；价格创建仍是「create + putWindows」两次请求，Task 11 保留该行为并显示失败可重试。
- 类型一致性：`TopRequest`、`AnalysisBody`、`Catalog`、`RequestsFilters.stream` 在 Task 1/4/5/8 的签名一致；`RequestDrawer` 的 `requestId` 在 Task 6/8/9 一致。
- 无 schema 变更；无新依赖。
