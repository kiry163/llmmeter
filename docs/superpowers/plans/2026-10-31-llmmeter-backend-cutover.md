# llmmeter 后端切换实现计划（配置 + 多上游 + 计费）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 llmmeter 的配置从 SQLite 切到 YAML，把单上游网关改成“本地 key 可改绑”的多上游网关，并按上游独立计价；Go 单测与构建可独立通过。

**Architecture:** 新增 `internal/config` 持有严格校验的 YAML 配置与不可变快照；快照内编译上游表、本地 key 哈希索引和每个上游的 `pricing.Table`。网关每个请求只读一次快照，按 key 绑定上游，记录上游/key 快照并写入 schema v2。管理 API 全部改为配置作用域；数据库只保留请求明细与分钟聚合。

**Tech Stack:** Go 1.27、`gopkg.in/yaml.v3`、`modernc.org/sqlite`、现有 `internal/pricing` / `internal/usage` / `internal/metrics`。

**Spec:** `docs/superpowers/specs/2026-10-31-llmmeter-open-source-design.md`

## Global Constraints

- 不做旧数据库/旧 YAML 迁移；schema 版本不是 2 时启动失败。
- 默认配置 `$XDG_CONFIG_HOME/llmmeter/config.yaml` 或 `~/.config/llmmeter/config.yaml`；默认数据库 `$XDG_CACHE_HOME/llmmeter/llmmeter.db` 或 `~/.cache/llmmeter/llmmeter.db`。
- 配置文件与数据库文件权限 `0600`，目录 `0700`；日志与错误响应永不输出完整 key。
- 价格身份是 `(upstream_id, model_name)`；本地 key 不携带价格。
- 一个本地 key 有且只有一个 `upstream_id`；改绑立即影响后续请求，在途请求保持旧上游与旧费率快照。
- 网关自身错误使用 OpenAI 形状：`{"error":{"message":...,"type":"invalid_request_error","code":...}}`。
- 不新增浏览器端 e2e；本阶段只要求 `go vet ./...`、`go test ./...`、`make build` 通过。前端页面在后续 UI 计划里替换，本阶段允许旧页面运行时不可用。
- 每个任务结束提交一次；不要跨任务混合未完成的半成品。

## File Structure

```
internal/config/
  config.go          # YAML 类型、默认值、严格解析、校验
  config_test.go
  paths.go           # XDG/flag 路径解析
  store.go           # 原子写、快照、Update、Reload
  store_test.go
  snapshot.go        # 上游表、key 哈希索引、价格表编译
  snapshot_test.go
internal/secure/
  password.go        # Argon2id、session secret、gateway key（从 settings 迁出）
  password_test.go
internal/store/
  schema.go          # schema v2、版本检查
  db.go
  records.go         # Record / MinuteDelta 上游字段
  writer.go
  queries.go         # 日志筛选、汇总
  analysis.go        # 聚合查询上游维度
internal/metrics/
  hub.go             # 按上游环形缓冲
  snapshot.go        # byUpstream、缓存命中
internal/gateway/
  gateway.go         # key 路由、上游透传、记录
internal/api/
  server.go          # 依赖注入、路由
  auth.go            # config.Store 会话
  setup.go           # /api/setup
  bootstrap.go       # /api/bootstrap、/api/data、/api/general
  upstreams.go       # 上游 CRUD、probe、test
  keys.go            # 本地 key CRUD、改绑、reveal
  pricing.go         # 按上游价格整表替换、import
  catalog.go         # 按上游模型目录
  read.go            # 日志/总览筛选
  analysis.go        # 分析响应
internal/pricecatalog/
  presets.go         # 内嵌预设匹配（先放最小可用快照）
  presets_test.go
main.go              # 装配、SIGHUP、init 子命令
```

删除：`internal/settings/`、数据库中的 `settings` / `models` / `price_windows` 表。

---

### Task 1: YAML 配置类型、默认值与校验

**Files:**
- Modify: `go.mod`、`go.sum`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `type Config`, `type Upstream`, `type GatewayKey`, `type Model`, `type Window`
- Produces: `func Parse(data []byte) (*Config, error)`
- Produces: `func (c *Config) Validate() error`
- Produces: `func (u Upstream) Active() bool`、`func (k GatewayKey) Active() bool`

- [ ] **Step 1: 写失败测试**

```go
func TestParseRejectsUnknownField(t *testing.T) {
	_, err := Parse([]byte("version: 1\nserver:\n  listen: 127.0.0.1:8100\n  nope: true\n"))
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateRejectsKeyWithoutUpstream(t *testing.T) {
	c := minimalConfig()
	c.GatewayKeys = []GatewayKey{{ID: "mac", Name: "Mac", Key: "sk-local-1234567890", UpstreamID: "missing", Enabled: boolPtr(true)}}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "upstream_id") {
		t.Fatalf("err = %v", err)
	}
}

func TestActiveDefaultsTrue(t *testing.T) {
	u := Upstream{}
	if !u.Active() {
		t.Fatal("omitted enabled must default true")
	}
}

func boolPtr(b bool) *bool { return &b }

func minimalConfig() *Config {
	return &Config{
		Version: 1,
		Server:  Server{Listen: "127.0.0.1:8100", Database: "~/.cache/llmmeter/llmmeter.db"},
		General: General{Timezone: "UTC", RetentionDays: 90},
	}
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/config -run 'TestParseRejectsUnknownField|TestValidateRejectsKeyWithoutUpstream|TestActiveDefaultsTrue' -v`
Expected: FAIL，`undefined: Parse`。

- [ ] **Step 3: 实现最小类型与校验**

```go
package config

type Config struct {
	Version     int          `yaml:"version"`
	Server      Server       `yaml:"server"`
	General     General      `yaml:"general"`
	Auth        Auth         `yaml:"auth"`
	Upstreams   []Upstream   `yaml:"upstreams"`
	GatewayKeys []GatewayKey `yaml:"gateway_keys"`
}

type Server struct {
	Listen   string `yaml:"listen"`
	Database string `yaml:"database"`
}

type General struct {
	Timezone      string `yaml:"timezone"`
	RetentionDays int    `yaml:"retention_days"`
}

type Auth struct {
	AdminPasswordHash string `yaml:"admin_password_hash"`
	SessionSecret     string `yaml:"session_secret"`
}

type Upstream struct {
	ID      string  `yaml:"id"`
	Name    string  `yaml:"name"`
	BaseURL string  `yaml:"base_url"`
	APIKey  string  `yaml:"api_key"`
	Enabled *bool   `yaml:"enabled"`
	Models  []Model `yaml:"models"`
}

type GatewayKey struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	Key        string `yaml:"key"`
	UpstreamID string `yaml:"upstream_id"`
	Enabled    *bool  `yaml:"enabled"`
}

type Model struct {
	Name       string   `yaml:"name"`
	Input      float64  `yaml:"input"`
	Output     float64  `yaml:"output"`
	CacheRead  float64  `yaml:"cache_read"`
	CacheWrite float64  `yaml:"cache_write"`
	Reasoning  *float64 `yaml:"reasoning"`
	Windows    []Window `yaml:"windows"`
}

type Window struct {
	Label      string   `yaml:"label"`
	Days       []string `yaml:"days"`
	Start      string   `yaml:"start"`
	End        string   `yaml:"end"`
	Input      *float64 `yaml:"input"`
	Output     *float64 `yaml:"output"`
	CacheRead  *float64 `yaml:"cache_read"`
	CacheWrite *float64 `yaml:"cache_write"`
	Reasoning  *float64 `yaml:"reasoning"`
}
```

`Parse` 用 `yaml.NewDecoder(bytes.NewReader(data))`、`KnownFields(true)`；解析前用 `Default()` 填 `listen=127.0.0.1:8100`、`retention_days=90`、`timezone` 留空由调用方补。`Validate` 用 `map[string]string` 收集字段错误并返回 `*ValidationError`；检查 spec 4.3 的全部规则，包括：`version==1`、slug 正则、baseURL scheme/host、key 全局唯一且 `len>=16`、`upstream_id` 存在、模型名同上游唯一、费率有限且 ≥0、窗口 ≤32、`days` 合法无重复、`HH:MM` 且 start≠end。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/config -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add go.mod go.sum internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add strict YAML schema and validation"
```

---

### Task 2: 路径解析、原子写与配置快照 Store

**Files:**
- Create: `internal/config/paths.go`
- Create: `internal/config/store.go`
- Test: `internal/config/store_test.go`

**Interfaces:**
- Consumes: `Parse`、`Validate`、`Config`
- Produces: `type Paths struct { Config, Database, Listen string }`
- Produces: `func ResolvePaths(configFlag, dbFlag, listenFlag string) (Paths, error)`
- Produces: `type Store struct`、`func OpenStore(path string) (*Store, error)`
- Produces: `func (s *Store) Config() *Config`（深拷贝）、`func (s *Store) Update(fn func(*Config) error) error`、`func (s *Store) Reload() error`、`func (s *Store) Path() string`
- Task 3 会在此基础上增加编译快照 `Snapshot()`。
- Produces: `func (s *Store) Writable() bool`

- [ ] **Step 1: 写失败测试**

```go
func TestStoreUpdateIsAtomicAndPerm0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	st, err := OpenStore(path)
	if err != nil { t.Fatal(err) }
	if err := st.Update(func(c *Config) error {
		c.Upstreams = []Upstream{{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com", Models: nil}}
		return nil
	}); err != nil { t.Fatal(err) }
	info, err := os.Stat(path)
	if err != nil { t.Fatal(err) }
	if info.Mode().Perm() != 0o600 { t.Fatalf("mode = %o", info.Mode().Perm()) }
	got, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(got), "deepseek") { t.Fatalf("file = %s", got) }
}

func TestStoreReloadKeepsOldConfigOnInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	st, err := OpenStore(path)
	if err != nil { t.Fatal(err) }
	before := st.Config()
	if err := os.WriteFile(path, []byte("version: nope\n"), 0o600); err != nil { t.Fatal(err) }
	if err := st.Reload(); err == nil { t.Fatal("expected reload error") }
	if !reflect.DeepEqual(st.Config(), before) { t.Fatal("config must not change on failed reload") }
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/config -run 'TestStore' -v`
Expected: FAIL，`undefined: OpenStore`。

- [ ] **Step 3: 实现**

`ResolvePaths`：

- 配置：`-config` > `$XDG_CONFIG_HOME/llmmeter/config.yaml` > `~/.config/llmmeter/config.yaml`。
- 数据库：`-db` > 配置 `server.database`（展开 `~`，相对路径相对配置目录） > `$XDG_CACHE_HOME/llmmeter/llmmeter.db` > `~/.cache/llmmeter/llmmeter.db`。
- 监听：`-listen` > 配置 `server.listen` > `127.0.0.1:8100`。
- 创建目录 `0700`；配置/数据库文件 `0600`。

`Store` 用 `sync.Mutex` + `atomic.Pointer[Config]`。`Update` 流程：深拷贝（`yaml.Marshal` 后 `Parse`）→ `fn` → `Validate` → 序列化 → 临时文件 `0600` + `fsync` + `rename` + 目录 `fsync` → 交换快照。`Reload` 读取并解析；失败不交换。

配置文件不存在时 `OpenStore` 返回默认配置、`Writable=false`，`Update` 首次写入后变 `Writable=true`。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/config -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/config/paths.go internal/config/store.go internal/config/store_test.go
git commit -m "feat(config): add paths, atomic store and reload"
```

---

### Task 3: 快照编译、key 哈希索引与按上游价格表

**Files:**
- Create: `internal/config/snapshot.go`
- Test: `internal/config/snapshot_test.go`

**Interfaces:**
- Consumes: `Config`、`pricing.Model`、`pricing.Window`、`pricing.NewTable`
- Produces: `type KeyBinding struct { KeyID, KeyName, UpstreamID, UpstreamName string; Upstream *Upstream; Table *pricing.Table; Enabled bool }`
- Produces: `type Snapshot struct { Config *Config; Location *time.Location; Upstreams map[string]*Upstream; Keys map[[32]byte]KeyBinding; Tables map[string]*pricing.Table }`
- Produces: `func Build(cfg *Config) (*Snapshot, error)`
- Produces: `func (s *Snapshot) LookupKey(raw string) (KeyBinding, bool)`
- Produces: `func (s *Snapshot) Upstream(id string) (*Upstream, bool)`

- [ ] **Step 1: 写失败测试**

```go
func TestBuildIndexesKeysAndPerUpstreamTables(t *testing.T) {
	c := minimalConfig()
	c.Upstreams = []Upstream{
		{ID: "a", Name: "A", BaseURL: "https://a.example", Enabled: boolPtr(true), Models: []Model{{Name: "m", Input: 1, Output: 2}}},
		{ID: "b", Name: "B", BaseURL: "https://b.example", Enabled: boolPtr(true), Models: []Model{{Name: "m", Input: 3, Output: 4}}},
	}
	c.GatewayKeys = []GatewayKey{
		{ID: "ka", Name: "KA", Key: "sk-local-aaaaaaaaaaaa", UpstreamID: "a", Enabled: boolPtr(true)},
		{ID: "kb", Name: "KB", Key: "sk-local-bbbbbbbbbbbb", UpstreamID: "b", Enabled: boolPtr(true)},
	}
	snap, err := Build(c)
	if err != nil { t.Fatal(err) }
	ka, ok := snap.LookupKey("sk-local-aaaaaaaaaaaa")
	if !ok || ka.UpstreamID != "a" { t.Fatalf("ka = %+v", ka) }
	if snap.Tables["a"] == nil || snap.Tables["b"] == nil { t.Fatal("missing tables") }
	qa := snap.Tables["a"].Quote("m", time.Now(), time.UTC, usage.Usage{PromptTokens: 1_000_000})
	qb := snap.Tables["b"].Quote("m", time.Now(), time.UTC, usage.Usage{PromptTokens: 1_000_000})
	if qa.Rates.Input != 1 || qb.Rates.Input != 3 { t.Fatalf("rates: %v %v", qa.Rates.Input, qb.Rates.Input) }
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/config -run TestBuildIndexesKeysAndPerUpstreamTables -v`
Expected: FAIL，`undefined: Build`。

- [ ] **Step 3: 实现**

- `Build` 先 `cfg.Validate()`，再 `time.LoadLocation`。
- 上游表 `map[string]*Upstream` 用配置中的值指针；key 索引 `map[[32]byte]KeyBinding`，键为 `sha256.Sum256([]byte(raw))`。
- 每个上游把 `config.Model` 转成 `pricing.Model`：`Days` 转 `DaysMask`（mon=bit0），`HH:MM` 转分钟；`Reasoning` 原样。
- 价格表用 `pricing.NewTable` 编译，存入 `Tables[upstream.ID]`。
- `LookupKey` 返回绑定及 `ok`；禁用 key 也返回绑定，由网关判断 `Enabled`。
- 上游禁用不改索引；网关在路由时检查。
- 扩展 Task 2 的 `Store`：内部改为 `atomic.Pointer[Snapshot]`；`OpenStore`、`Update`、`Reload` 在写盘/交换前调用 `Build`，成功后交换编译快照。新增 `func (s *Store) Snapshot() *Snapshot`；`Config()` 仍返回配置深拷贝供 API 读取。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/config -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/config/snapshot.go internal/config/snapshot_test.go
git commit -m "feat(config): compile key index and per-upstream pricing"
```

---

### Task 4: Schema v2 与版本拒绝

**Files:**
- Modify: `internal/store/schema.go`
- Modify: `internal/store/db.go`
- Test: `internal/store/schema_test.go`（新建）

**Interfaces:**
- Produces: `const UserVersion = 2`
- Produces: schema v2 DDL（spec 6.1）
- Consumes: 无

- [ ] **Step 1: 写失败测试**

```go
func TestOpenDBRejectsV1(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil { t.Fatal(err) }
	if _, err := db.Exec(`CREATE TABLE requests (id INTEGER); PRAGMA user_version = 1;`); err != nil { t.Fatal(err) }
	db.Close()
	if _, err := OpenDB(path); err == nil || !strings.Contains(err.Error(), "incompatible database schema") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenDBCreatesV2WithoutSettingsTables(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "new.db"))
	if err != nil { t.Fatal(err) }
	defer st.Close()
	for _, name := range []string{"settings", "models", "price_windows"} {
		if ok, _ := tableExists(st.DB(), name); ok { t.Fatalf("table %s must not exist", name) }
	}
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/store -run 'TestOpenDB' -v`
Expected: FAIL，旧版本仍被接受或仍存在 settings 表。

- [ ] **Step 3: 实现**

- 用 spec 6.1 的完整 DDL 替换 `Schema`，新增 `upstream_id/upstream_name/key_id/key_name` 与 `idx_requests_upstream_started`、`idx_requests_upstream_key_started`。
- `traffic_minutes` 主键改为 `(local_day, minute_of_day, path_class, upstream_id, model)`。
- `ensureSchema`：`version == 0` 且无任何表 → 建 v2；`version != 2` → 返回 `incompatible database schema (found %d, need 2)`；不再只拒绝“更高版本”。
- 删除 `settings/models/price_windows` 的建表语句。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/store -run 'TestOpenDB' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/store/schema.go internal/store/db.go internal/store/schema_test.go
git commit -m "feat(store): switch to schema v2 without config tables"
```

---

### Task 5: Store 记录、分钟聚合与查询增加上游维度

**Files:**
- Modify: `internal/store/records.go`
- Modify: `internal/store/writer.go`
- Modify: `internal/store/queries.go`
- Modify: `internal/store/analysis.go`
- Test: `internal/store/records_test.go`、`internal/store/queries_test.go`、`internal/store/analysis_test.go`

**Interfaces:**
- Consumes: schema v2
- Produces: `Record{UpstreamID, UpstreamName, KeyID, KeyName string}`
- Produces: `MinuteDelta{UpstreamID string}`
- Produces: 查询参数 `RequestFilter{UpstreamID, KeyID string}`
- Produces: `RequestSummary` 覆盖筛选后的全部行

- [ ] **Step 1: 写失败测试**

```go
func TestRecordRoundTripCarriesUpstreamAndKey(t *testing.T) {
	st := openTestStore(t)
	rec := testRecord()
	rec.UpstreamID, rec.UpstreamName = "deepseek", "DeepSeek"
	rec.KeyID, rec.KeyName = "macbook", "MacBook"
	st.EnqueueRecord(rec)
	if err := st.Flush(); err != nil { t.Fatal(err) }
	rows, err := st.Requests(store.RequestFilter{Limit: 1})
	if err != nil { t.Fatal(err) }
	if len(rows) != 1 || rows[0].UpstreamID != "deepseek" || rows[0].KeyName != "MacBook" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestRequestsFilterByUpstreamAndKey(t *testing.T) {
	st := openTestStore(t)
	seedTwoUpstreams(t, st)
	rows, err := st.Requests(store.RequestFilter{UpstreamID: "a", KeyID: "ka", Limit: 50})
	if err != nil { t.Fatal(err) }
	sum, err := st.RequestsSummary(store.RequestFilter{UpstreamID: "a", KeyID: "ka"})
	if err != nil { t.Fatal(err) }
	if len(rows) != 1 || sum.Requests != 1 { t.Fatalf("rows=%d sum=%+v", len(rows), sum) }
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { st.Close() })
	return st
}

func testRecord() store.Record {
	base := time.Date(2026, 10, 31, 5, 0, 0, 0, time.UTC)
	return store.Record{
		StartedAt: base, CompletedAt: base.Add(time.Second),
		LocalDay: "2026-10-31", MinuteOfDay: 780,
		Path: "/v1/chat/completions", PathClass: "chat",
		Model: "m", Status: 200, LatencyMS: 1000,
		UsageStatus: usage.StatusOK,
	}
}

func seedTwoUpstreams(t *testing.T, st *store.Store) {
	t.Helper()
	for _, pair := range [][2]string{{"a", "ka"}, {"b", "kb"}} {
		r := testRecord()
		r.UpstreamID, r.UpstreamName = pair[0], strings.ToUpper(pair[0])
		r.KeyID, r.KeyName = pair[1], pair[1]
		st.EnqueueRecord(r)
	}
	if err := st.Flush(); err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/store -run 'TestRecordRoundTripCarriesUpstreamAndKey|TestRequestsFilterByUpstreamAndKey' -v`
Expected: FAIL，字段或过滤器不存在。

- [ ] **Step 3: 实现**

- `Record` 和 `MinuteDelta` 加字段；`insertRequestSQL` 增加 4 列与 4 个占位符；`insertArgs` 按列顺序追加。
- `traffic_minutes` upsert 的 `ON CONFLICT` 主键增加 `upstream_id`；`MinuteDelta.UpstreamID` 写入。
- `RequestFilter` 增加 `UpstreamID`、`KeyID`；SQL `WHERE` 追加；`key` 过滤必须同时有 `upstream`（API 层校验）。
- `analysis` 的 `models`/`paths` 查询增加 `upstream_id` 分组与返回字段；`trend` 返回 `series[]` 所需的上游字段。
- 不实现迁移；测试数据直接按 v2 插入。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/store -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/store
git commit -m "feat(store): persist upstream and key snapshots"
```

---

### Task 6: Metrics hub 增加上游维度与缓存命中

**Files:**
- Modify: `internal/metrics/hub.go`
- Modify: `internal/metrics/snapshot.go`
- Test: `internal/metrics/hub_test.go`

**Interfaces:**
- Consumes: `Observation`
- Produces: `Observation{UpstreamID string}`
- Produces: `Snapshot.ByUpstream map[string]UpstreamSnapshot`
- Produces: `Snapshot.PromptTokens/CacheReadTokens int64`、`CacheHitRate *float64`
- Produces: `ModelRate{UpstreamID string}`、`InflightInfo{UpstreamID string}`

- [ ] **Step 1: 写失败测试**

```go
func TestSnapshotSeparatesUpstreams(t *testing.T) {
	h := New(nil, nil)
	start := h.Started("a", "chat", "m1")
	h.Finished(start, Observation{UpstreamID: "a", Model: "m1", Status: 200, UsageStatus: "ok"})
	start = h.Started("b", "chat", "m2")
	h.Finished(start, Observation{UpstreamID: "b", Model: "m2", Status: 200, UsageStatus: "ok"})
	s := h.Snapshot(60 * time.Second)
	if s.ByUpstream["a"].Requests != 1 || s.ByUpstream["b"].Requests != 1 {
		t.Fatalf("byUpstream = %+v", s.ByUpstream)
	}
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/metrics -run TestSnapshotSeparatesUpstreams -v`
Expected: FAIL，字段不存在。

- [ ] **Step 3: 实现**

- `Started(pathClass, upstreamID, model)`；`Inflight` 保存 upstreamID。
- 每个上游维护独立秒级 slot；全局汇总由各上游相加，`spark` 仍返回全局。
- `Finished` 把 prompt/cache-read token 计入全局与对应上游。
- `Snapshot` 返回 `byUpstream`、`byModel`（带 upstreamId）、`inflightRequests`（带 upstreamId）、`promptTokens`、`cacheReadTokens`、`cacheHitRate`（prompt=0 时 nil）。
- `byUpstream` 删除后自然过期，不做主动裁剪。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/metrics -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/metrics
git commit -m "feat(metrics): track live traffic per upstream"
```

---

### Task 7: 迁移密码/密钥工具到 `internal/secure`

**Files:**
- Create: `internal/secure/password.go`
- Test: `internal/secure/password_test.go`
- Delete: `internal/settings/crypto.go`

**Interfaces:**
- Produces: `HashPassword(pw string) (string, error)`
- Produces: `VerifyPassword(encoded, pw string) bool`
- Produces: `NewGatewayKey() string`
- Produces: `RandomSecret() ([]byte, error)`

- [ ] **Step 1: 写失败测试**

```go
func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil { t.Fatal(err) }
	if !VerifyPassword(h, "correct horse") || VerifyPassword(h, "wrong") { t.Fatal("password mismatch") }
}

func TestGatewayKeyShape(t *testing.T) {
	k := NewGatewayKey()
	if !strings.HasPrefix(k, "sk-local-") || len(k) < 20 { t.Fatalf("key = %q", k) }
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/secure -v`
Expected: FAIL，包不存在。

- [ ] **Step 3: 实现**

把 `internal/settings/crypto.go` 的实现原样搬到 `internal/secure/password.go`，把错误前缀从 `settings:` 改为 `secure:`。所有调用方后续改为 `secure.HashPassword` 等。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/secure -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/secure internal/settings/crypto.go
git commit -m "refactor: move password and key helpers to secure package"
```

---

### Task 8: Gateway 改为 key 路由与按上游计价

**Files:**
- Modify: `internal/gateway/gateway.go`
- Modify: `internal/gateway/gateway_test.go`
- Modify: `internal/gateway/stream.go`（仅调用签名）
- Test: `internal/gateway/gateway_test.go`

**Interfaces:**
- Consumes: `config.Store`、`config.Snapshot`、`config.KeyBinding`
- Produces: `func New(store *config.Store, st *store.Store, hub *metrics.Hub, now func() time.Time) *Gateway`
- Produces: gateway 记录的 `store.Record` 带 upstream/key 字段

- [ ] **Step 1: 写失败测试**

```go
func TestGatewayRoutesKeyToBoundUpstream(t *testing.T) {
	// 两个 httptest upstream，分别返回不同 header/body
	// 配置两个上游、两个 key；请求 key A 命中 A，key B 命中 B
	// 断言响应体来自正确上游，且记录 upstream_id 正确
}

func TestGatewayRebindAffectsOnlyNewRequests(t *testing.T) {
	// 请求 1 用 key -> upstream A；改绑 key -> upstream B
	// 请求 2 命中 B；记录 1 仍为 A
}

func TestGatewayMissingOrDisabledKeyIs401(t *testing.T) {
	// 未知 key、disabled key 都返回 401 + invalid_api_key
}

func TestGatewayDisabledUpstreamIs503(t *testing.T) {
	// key 指向 disabled upstream -> 503 + upstream_disabled
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/gateway -run 'TestGatewayRoutesKeyToBoundUpstream|TestGatewayRebindAffectsOnlyNewRequests|TestGatewayMissingOrDisabledKeyIs401|TestGatewayDisabledUpstreamIs503' -v`
Expected: FAIL（旧 `New` 只接受 `settings.Repo`）。

- [ ] **Step 3: 实现**

- `ServeHTTP` 开头：`snap := g.store.Snapshot()`；无上游 → `503 setup_required`；上游存在但 key 索引为空 → 继续走 key 查找，返回 `401 invalid_api_key`。
- 取 Bearer 原值，`binding, ok := snap.LookupKey(raw)`；`!ok || !binding.Enabled` → `401 invalid_api_key`。
- `binding.Upstream == nil || !binding.Upstream.Active()` → `503 upstream_disabled`。
- 目标 URL 用 `binding.Upstream.BaseURL`；上游 key 用 `binding.Upstream.APIKey`。
- 计价：`binding.Table.Quote(model, g.now(), snap.Location, u)`。
- `hub.Started(class, binding.UpstreamID, model)`。
- `Record` 写入 `UpstreamID/Name`、`KeyID/Name`。
- `writeError` 改为 OpenAI 形状，保留 `upstream_error` 脱敏。
- 保持 spec 5.8 的透传行为不变。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/gateway -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/gateway
git commit -m "feat(gateway): route local keys to their upstream"
```

---

### Task 9: Auth 改为配置快照

**Files:**
- Modify: `internal/api/auth.go`
- Modify: `internal/api/auth_test.go`
- Modify: `internal/api/server.go`（构造参数）

**Interfaces:**
- Consumes: `config.Store`、`config.Snapshot`
- Produces: `func NewAuth(store *config.Store, now func() time.Time) *Auth`
- Produces: `Auth` 从快照读取 `AdminPasswordHash`、`SessionSecret`

- [ ] **Step 1: 写失败测试**

```go
func TestAuthUsesConfigSessionSecret(t *testing.T) {
	// 写临时配置，设置 admin_password_hash 与 session_secret
	// 登录成功后用新 secret 校验 cookie；重载配置改 secret 后旧 cookie 失效
}

func TestAuthLoopbackStillBypassesPassword(t *testing.T) {
	// RemoteAddr=127.0.0.1 且无 cookie 时 Require 放行
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/api -run 'TestAuthUsesConfigSessionSecret|TestAuthLoopbackStillBypassesPassword' -v`
Expected: FAIL（`settings.Repo`）。

- [ ] **Step 3: 实现**

- `Auth.repo` 替换为 `store *config.Store`；每次读取 `store.Snapshot()`。
- `Issue` 使用 `snap.Auth.SessionSecret`；空 secret 时拒绝签发并返回 500（正常启动会生成）。
- `Login` 用 `secure.VerifyPassword`。
- `passwordEpoch` 仍用密码哈希前 16 字符；密码变更轮换 secret 后旧会话失效。
- `RequireLoopback`、`CSRF`、HostGuard 行为不变。
- 实现 `POST /api/auth/password`：校验 `current`，`secure.HashPassword(newPassword)`，清除时校验非回环监听，轮换 `session_secret`，`Store.Update` 一次写入，当前请求重新签发 Cookie。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api -run 'TestAuth' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/api/auth.go internal/api/auth_test.go internal/api/server.go
git commit -m "feat(api): read sessions and passwords from YAML config"
```

---

### Task 10: bootstrap、setup、general、data 与配置重载 API

**Files:**
- Modify: `internal/api/server.go`
- Create: `internal/api/bootstrap.go`
- Create: `internal/api/setup.go`
- Create: `internal/api/general.go`
- Modify: `main.go`（SIGHUP 与装配）
- Test: `internal/api/setup_test.go`、`internal/api/bootstrap_test.go`

**Interfaces:**
- Consumes: `config.Store`、`config.Paths`、`secure`
- Produces: `GET /api/bootstrap`、`POST /api/setup`、`GET/PATCH /api/general`、`GET /api/data`、`POST /api/config/reload`

- [ ] **Step 1: 写失败测试**

```go
func TestSetupWritesConfigAndReturnsGatewayKey(t *testing.T) {
	// httptest 回环请求；POST /api/setup
	// 断言 200、gatewayKey 前缀、config.yaml 含 upstream 与 gateway_keys
}

func TestSetupRejectsWhenUpstreamsExist(t *testing.T) {
	// 已有 upstream 时 409
}

func TestBootstrapNeedsSetupReflectsConfig(t *testing.T) {
	// 无上游 -> needsSetup true；加一个上游 -> false
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/api -run 'TestSetup|TestBootstrap' -v`
Expected: FAIL（端点不存在或仍走 DB）。

- [ ] **Step 3: 实现**

- `api.Config` 改为 `{Version, Listen, DBPath, Paths, Store *config.Store, Data *store.Store, Hub *metrics.Hub, Now}`。
- `POST /api/setup`：只回环；body 按 spec 8.2；生成 slug/键/session secret；可选 `models`；`Store.Update` 一次写入；返回 `{upstreamId,keyId,gatewayKey,configPath}`。
- `GET /api/bootstrap`：返回 version、needsSetup、authRequired、listen、timezone、configPath、dbPath、earliestDay；不返回密钥。
- `GET /api/data`：路径、DB/WAL 体积、schema 版本、retention、timezone、队列、earliestDay。
- `PATCH /api/general`：校验 timezone/retention 后 `Store.Update`。
- `POST /api/config/reload`：调用 `Store.Reload()`；失败 500，成功 200。
- `main.go`：安装 `SIGHUP` handler 调用 `Store.Reload()`；`listen`/`database` 变更只提示重启。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api -run 'TestSetup|TestBootstrap' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add main.go internal/api/server.go internal/api/bootstrap.go internal/api/setup.go internal/api/general.go internal/api/setup_test.go internal/api/bootstrap_test.go
git commit -m "feat(api): bootstrap and setup from YAML config"
```

---

### Task 11: 上游与本地 key API

**Files:**
- Create: `internal/api/upstreams.go`
- Create: `internal/api/keys.go`
- Test: `internal/api/upstreams_test.go`、`internal/api/keys_test.go`

**Interfaces:**
- Consumes: `config.Store`、`config.Snapshot`、`secure`
- Produces: spec 8.3 的上游端点与 spec 8.4 的 key 端点

- [ ] **Step 1: 写失败测试**

```go
func TestDeleteUpstreamWithKeysIs409(t *testing.T) {
	// 上游 a 绑定 key k；DELETE /api/upstreams/a -> 409 upstream_in_use
}

func TestPatchKeyRebindsUpstream(t *testing.T) {
	// 上游 a/b + key k->a；PATCH /api/keys/k {"upstreamId":"b"}
	// 断言 200、配置文件 upstream_id=b、快照路由到 b
}

func TestKeySecretRequiresAdminAndNoStore(t *testing.T) {
	// GET /api/keys/k/secret -> 200、Cache-Control no-store、key 明文
}

func TestUpstreamProbeDoesNotWriteConfig(t *testing.T) {
	// probe 指向 httptest upstream；断言返回 models，配置文件不变
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/api -run 'TestDeleteUpstream|TestPatchKey|TestKeySecret|TestUpstreamProbe' -v`
Expected: FAIL（端点不存在）。

- [ ] **Step 3: 实现**

- 上游列表返回 `keyCount`、`lastUsedAt`（查询 `requests`）；不返回 key 列表。
- 新建上游：slug 生成（ASCII 转 `-`，无字符用 `upstream`，冲突追加 `-2`）；`enabled` 默认 true。
- 删除上游：先检查 `gateway_keys` 中是否有 `upstream_id`；有则 `409 upstream_in_use`，`fields.keys` 列出 id。
- key 列表：`GET /api/keys?upstream=`，返回 `upstreamId/upstreamName/lastUsedAt`。
- 新建 key：`secure.NewGatewayKey()`，id slug，写配置。
- `PATCH /api/keys/{id}`：改名、改绑、启停；改绑即更新 `upstream_id`。
- reveal：`GET /api/keys/{id}/secret`、`GET /api/upstreams/{id}/secret`，`Cache-Control: no-store`。
- probe：接受草稿 baseURL/apiKey，调用上游 `/models`，返回 provider/presets/models/error；不写配置。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api -run 'TestDeleteUpstream|TestPatchKey|TestKeySecret|TestUpstreamProbe' -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/api/upstreams.go internal/api/keys.go internal/api/upstreams_test.go internal/api/keys_test.go
git commit -m "feat(api): manage upstreams and rebindable local keys"
```

---

### Task 12: 按上游价格、模型目录与预设

**Files:**
- Create: `internal/api/pricing.go`
- Modify: `internal/api/catalog.go`
- Create: `internal/pricecatalog/presets.go`
- Test: `internal/api/pricing_test.go`、`internal/api/catalog_test.go`、`internal/pricecatalog/presets_test.go`

**Interfaces:**
- Consumes: `config.Store`、`config.Model`、`pricecatalog`
- Produces: `GET/PUT /api/upstreams/{id}/pricing`、`POST .../pricing/import`、`GET .../catalog`
- Produces: `pricecatalog.Match(baseURL string, models []string) []Preset`

- [ ] **Step 1: 写失败测试**

```go
func TestPutPricingReplacesOnlyTargetUpstream(t *testing.T) {
	// 上游 a/b 各有价格；PUT a 的新列表
	// 断言 a 更新、b 不变、a 的重复模型名被拒绝
}

func TestPricingImportSkipsExisting(t *testing.T) {
	// 已存在 m；import [m,n] -> created [n], skipped [m]
}

func TestPresetMatchDeepSeek(t *testing.T) {
	got := Match("https://api.deepseek.com", []string{"deepseek-chat"})
	if len(got) != 1 || got[0].Model != "deepseek-chat" { t.Fatalf("got = %+v", got) }
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/api -run 'TestPutPricing|TestPricingImport' ./internal/pricecatalog -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- `PUT /api/upstreams/{id}/pricing` 整体替换该上游 `Models`，校验后 `Store.Update`；返回规范化列表。
- `import` 只插入缺失模型，费率全 0，上限 100。
- `catalog` 按上游拉取 `/models`（base 或 `/v1/models`，5s 超时，5 分钟缓存），返回 configured/upstream/observed/presets/upstreamError；失败 HTTP 200。
- `pricecatalog.Match`：provider host 表 + 模型名精确匹配；内置快照覆盖 spec 7.4 的 provider 列表（DeepSeek、OpenAI、OpenRouter、Moonshot、Zhipu、DashScope、SiliconFlow、Groq、Together、Fireworks、xAI、Mistral、MiniMax、Ollama/LM Studio），文件头注释 `snapshot_date` 与来源。生成脚本可以后置，但本任务提交的静态快照必须包含这些 provider 的至少一个常用模型。
- 价格页未实现前，API 测试只覆盖 JSON 契约。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api ./internal/pricecatalog -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/api/pricing.go internal/api/catalog.go internal/api/pricing_test.go internal/api/catalog_test.go internal/pricecatalog
git commit -m "feat(api): per-upstream pricing and model catalog"
```

---

### Task 13: 读接口增加上游筛选与分析维度

**Files:**
- Modify: `internal/api/read.go`
- Modify: `internal/api/analysis.go`
- Modify: `internal/api/read_test.go`
- Modify: `internal/api/analysis_test.go`

**Interfaces:**
- Consumes: `store` 查询与聚合
- Produces: `/api/overview`、`/api/series`、`/api/breakdown`、`/api/analysis`、`/api/requests` 的 `upstream` 参数
- Produces: 分析趋势 `series[]`、模型/路径行带上游字段、Top 请求带上游/key

- [ ] **Step 1: 写失败测试**

```go
func TestRequestsKeyFilterRequiresUpstream(t *testing.T) {
	// /api/requests?key=ka 无 upstream -> 400
}

func TestAnalysisTrendReturnsSeriesPerUpstream(t *testing.T) {
	// 两个上游同名模型同分钟
	// 断言 trend[0].series 有两条，upstreamId 不同
}

func TestOverviewFiltersByUpstream(t *testing.T) {
	// 全部请求 vs ?upstream=a 的 total 不同
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test ./internal/api -run 'TestRequestsKeyFilter|TestAnalysisTrend|TestOverviewFilters' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- 所有读接口解析可选 `upstream`，传给 store filter。
- `key` 过滤必须同时有 `upstream`，否则 400 `validation`。
- `/api/breakdown` 行增加 `upstreamId/upstreamName`。
- `/api/analysis` 的 trend 改为 `series[]`；models/paths/topRequests 增加上游字段；health/cache 按过滤后计算。
- `/api/live` 直接返回 hub 的 `byUpstream`（已有）。

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/api -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/api/read.go internal/api/analysis.go internal/api/read_test.go internal/api/analysis_test.go
git commit -m "feat(api): filter reads and analysis by upstream"
```

---

### Task 14: `llmmeter init` 与 main 装配

**Files:**
- Modify: `main.go`
- Test: `main_test.go`

**Interfaces:**
- Consumes: `config.ResolvePaths`、`config.OpenStore`、`secure`
- Produces: `llmmeter init -config -listen -database -timezone -upstream-name -upstream-url -upstream-key -key-name -admin-password-stdin`
- Produces: startup resolves paths, loads config, validates non-loopback password rule, opens schema v2 DB

- [ ] **Step 1: 写失败测试**

```go
func TestInitWritesConfigAndPrintsKey(t *testing.T) {
	// 执行 runInit 到临时目录
	// 断言配置含 upstream、gateway_keys、session_secret；stdout 含 sk-local-
}

func TestInitRefusesExistingConfig(t *testing.T) {
	// 已存在 -> error
}

func TestValidateStartupUsesConfig(t *testing.T) {
	// 非回环 + 无密码 -> error；有密码 -> nil
}
```

- [ ] **Step 2: 运行并确认失败**

Run: `go test . -run 'TestInit|TestValidateStartupUsesConfig' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

- `run` 增加 `init` 子命令分支；`serve` 仍可省略。
- flags：`-config`、`-db`、`-listen`，以及 init 专用 flags。`-db`/`-listen` 默认空字符串，空值表示使用配置/默认值，避免 flag 默认值覆盖 YAML。
- init：解析、生成 secret/key、校验、写配置、打印 `gateway key: ...`；已存在拒绝。
- serve：`ResolvePaths` → `OpenStore` → `Build` 校验 → 非回环密码检查 → `store.Open`（schema v2）→ 装配 gateway/api。配置不存在时以默认值进入 setup。
- `pruneLoop` 改为读取 `config.Store` 快照的 `Location/RetentionDays`。
- `SIGHUP` 调 `Store.Reload()`；失败只记录日志。

- [ ] **Step 4: 运行测试**

Run: `go test . -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add main.go main_test.go
git commit -m "feat(cli): add init command and YAML startup"
```

---

### Task 15: 删除旧 settings 包并收口构建

**Files:**
- Delete: `internal/settings/`（除已迁出的 crypto 外全部）
- Modify: 所有 `settings.` 引用
- Modify: `internal/e2e/e2e_test.go`（与新架构冲突则删除）
- Modify: `README.md`（只更新构建/启动命令，完整文档在开源计划）

**Interfaces:**
- Consumes: 前 14 个任务
- Produces: 无 `internal/settings` 依赖；`go test ./...`、`make build` 通过

- [ ] **Step 1: 搜索残留引用**

Run: `grep -R "internal/settings\|settings\." -n --include='*.go' .`
Expected: 只剩待删除文件；记录每个调用点。

- [ ] **Step 2: 删除并修复编译**

- 删除 `internal/settings` 目录。
- 删除或改写 `internal/e2e/e2e_test.go`，不新增 e2e。
- 更新 README 的启动命令为 `./llmmeter -config ~/.config/llmmeter/config.yaml`，删除“SQLite 是唯一事实来源”的旧句子。

- [ ] **Step 3: 运行全量单测**

Run: `go vet ./... && go test ./...`
Expected: PASS。

- [ ] **Step 4: 构建**

Run: `make build`
Expected: 退出码 0，生成 `./llmmeter`。

- [ ] **Step 5: 提交**

```bash
git add -A
git commit -m "refactor: remove SQLite settings package"
```

---

## Phase Boundary

本计划完成后：

- YAML 配置、key 改绑、多上游路由、按上游计价、schema v2、管理 API 均已具备单测。
- 旧 Web UI 尚未改版，运行时可能调用已删除的接口；这是下一份 UI 计划的起点，不在本计划验收范围。
- 不运行产品 e2e；由维护者在本阶段之后或全部计划完成后验收。

## Subsequent Plans

- `docs/superpowers/plans/2026-11-01-llmmeter-ui.md`：spec 第 9 节的页面改造、上游筛选、设置四个子页、setup 向导、key 改绑 UI、浅色主题与空状态。
- `docs/superpowers/plans/2026-11-01-llmmeter-open-source.md`：spec 第 10 节的 README、截图、LICENSE、CI、goreleaser、Docker、Homebrew、`llmmeter init` 文档与安全说明。

这两份计划在本计划验收后分别编写；不要把 UI 或发布工作混进后端任务。
