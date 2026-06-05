# Reasonix-AHE v0.1 本地启动计划

> **项目性质**：DeepSeek-Reasonix-AHE 是基于 [esengine/DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix) 的实验性二次开发项目，围绕论文 [Agentic Harness Engineering: Observability-Driven Automatic Evolution of Coding-Agent Harnesses](https://arxiv.org/abs/2604.25850) 进行设计：Reasonix 作为 coding-agent 基座，AHE 作为升级框架，不代表上游官方发布版本。

本计划记录 Reasonix-AHE 的第一版路线：先在本地建立可观测、缓存契约、snapshot、eval、evidence、proposal、GC / quota 等实验底座，不急着做自动 self-evolution。

## 0. 项目一句话目标

> **Reasonix-AHE v0.1：在不破坏 Reasonix prefix-cache 稳定性的前提下，为 Reasonix 增加本地可观测、可评测、可回滚、可逐步演化的 AHE 实验底座。**

第一版不是要立刻让 agent 自动修改自己。
第一版要先做到：

```text
看得见：trace
守得住：cache contract
分得清：harness snapshot vs session transcript vs eval artifact
测得准：smoke/canary eval
说得明：evidence report + proposal manifest
删得稳：GC / quota
```

这正好对应 AHE 论文的三个核心观察维度：component observability、experience observability、decision observability；论文也强调，真正有效的 harness 增益主要来自 tools、middleware、long-term memory，而不是单纯 system prompt。([arXiv][3])

---

# 1. 本地项目初始化

## 1.1 创建工作目录

建议不要直接在平时项目目录里乱试，单独建一个实验根目录：

```bash
mkdir -p ~/dev/agent-lab
cd ~/dev/agent-lab
```

## 1.2 克隆 Reasonix 源码为本地项目 `Reasonix-AHE`

```bash
git clone --branch main-v2 --single-branch https://github.com/esengine/DeepSeek-Reasonix.git Reasonix-AHE
cd Reasonix-AHE
```

这里项目目录叫 `Reasonix-AHE`，但第一版**不要急着改 Go module path**。
原因很简单：一改 module path，import 会大面积变动，噪声会淹没真正的 AHE 改造。第一版保持原 module path，只把它当作本地实验 fork。

## 1.3 把官方 remote 改成只读 upstream

因为你现在不打算上传到官方 GitHub 仓库，所以建议把 `origin` 改名为 `upstream`，并禁用 push：

```bash
git remote rename origin upstream
git remote set-url --push upstream DISABLED
git remote -v
```

预期看到类似：

```text
upstream  https://github.com/esengine/DeepSeek-Reasonix.git (fetch)
upstream  DISABLED (push)
```

再加一个本地 pre-push 保险：

```bash
cat > .git/hooks/pre-push <<'EOF'
#!/usr/bin/env bash
remote="$1"

if [ "$remote" = "upstream" ]; then
  echo "Refusing to push to upstream. This is a local Reasonix-AHE experiment."
  exit 1
fi
EOF

chmod +x .git/hooks/pre-push
```

这一步就像给剑鞘加锁：不是不能出剑，而是防止误伤正主。

---

# 2. 建立本地实验分支

## 2.1 记录官方基线

```bash
git status
git rev-parse HEAD
git log -1 --oneline
```

建议把当前官方基线 tag 一下：

```bash
BASE_DATE=$(date +%Y%m%d)
git tag "local/baseline-main-v2-${BASE_DATE}"
```

## 2.2 创建 AHE 实验分支

```bash
git checkout -b ahe/local-v0.1
```

后续所有本地改造都在这个分支上做。

推荐分支策略：

```text
main-v2                    # 只跟踪 upstream/main-v2，不直接开发
ahe/local-v0.1             # 你的本地第一版主线
ahe/trace                  # trace 子任务
ahe/cache-contract         # cache contract 子任务
ahe/eval-runner            # eval runner 子任务
ahe/harness-snapshot       # snapshot 子任务
```

如果后续要和 owner 交流，最方便的是：

```bash
git diff local/baseline-main-v2-YYYYMMDD..ahe/local-v0.1
```

这样能清楚展示你到底做了什么。

---

# 3. 本地构建与基线验证

## 3.1 准备环境

官方 README 里写了源码构建使用 `make build`，会输出 `bin/reasonix`。([GitHub][1]) 你先跑一遍，不急着改代码：

```bash
go version
make build
./bin/reasonix --help
```

如果有测试：

```bash
go test ./...
```

如果 `go test ./...` 因 desktop、环境、外部依赖等失败，不要立刻修；先记录下来：

```bash
mkdir -p .reasonix-ahe/bootstrap
go test ./... 2>&1 | tee .reasonix-ahe/bootstrap/go-test-baseline.log
```

## 3.2 创建本地忽略目录

新增或修改 `.git/info/exclude`，先不要污染项目 `.gitignore`：

```bash
cat >> .git/info/exclude <<'EOF'

# Reasonix-AHE local artifacts
.reasonix-ahe/
.reasonix-harness/snapshots/
.reasonix-harness/active
.reasonix-harness/proposals/
*.trace.jsonl
*.trace.jsonl.zst
*.eval.json
*.secret
.env.local
EOF
```

说明：

```text
.git/info/exclude 只影响你的本地仓库；
不会出现在 git diff 里；
适合本地实验阶段使用。
```

---

# 4. 第一版目录设计

第一版建议新增这些目录：

```text
Reasonix-AHE/
├── docs/
│   ├── AHE.md                  # 新增：AHE 设计边界
│   └── LAB.md                  # 新增：Reasonix Lab 使用说明
├── internal/
│   ├── trace/                  # 新增：typed JSONL trace
│   ├── cachecontract/          # 新增：stable prefix contract
│   ├── harness/                # 新增：snapshot / lock / component loader
│   └── lab/                    # 新增：eval / distill / manifest / gc
├── benchmarks/
│   └── ahe/
│       ├── smoke/
│       └── canary/
├── .reasonix-ahe/              # 本地生成物，不提交
│   ├── traces/
│   ├── evals/
│   ├── reports/
│   ├── proposals/
│   └── gc/
└── .reasonix-harness/          # 本地 harness workspace
    ├── source/
    ├── snapshots/
    ├── manifests/
    └── active
```

第一版可以先把 `.reasonix-harness/source/` 也作为本地实验目录，不急着决定未来是否提交。
等你和 owner 交流后，再讨论它应该叫 `.reasonix-harness`、`.reasonix-lab`，还是放进 `reasonix.toml` 管理。

---

# 5. v0.1 的核心不变量

这几条是第一版“宪法”，后面所有代码都要服从它：

```text
1. 不允许 live session 中途修改 system prompt / tool schema / skill index / middleware policy。
2. AHE 只能生成下一版 harness snapshot，不能热更新当前 session。
3. trace / evidence / manifest 不进入 live agent prefix。
4. tool schema 必须 canonical serialization。
5. 每次 DeepSeek model response 都记录 cache hit / miss tokens。
6. eval 不只看 pass/fail，还必须看 cache_hit_ratio 和 contract violations。
7. v0.1 不允许 evolve agent 自动修改 Reasonix core Go runtime。
8. 所有 raw trace / eval artifact 必须可 GC。
```

可以把这段直接写进 `docs/AHE.md`。

---

# 6. 第一版任务拆解

## P0：项目基线与文档

### P0.1 创建 `docs/AHE.md`

内容建议：

```markdown
# Reasonix-AHE Design

## Goal

Build a cache-preserving AHE substrate for Reasonix.

## Non-goals for v0.1

- No automatic self-modification of Reasonix core.
- No automatic merge of evolution proposals.
- No live-session harness mutation.
- No dynamic injection of eval evidence into the live model prefix.

## Invariants

- Cache-first.
- Snapshot-based harness evolution.
- Append-only live sessions.
- Stable tool schema.
- Out-of-band trace and evidence.

## Editable harness components

- prompts
- tool descriptions
- skills
- middleware config
- model routing
- long-term memory
```

提交：

```bash
git add docs/AHE.md
git commit -m "docs(ahe): add local Reasonix-AHE design RFC"
```

---

### P0.2 创建 `docs/LAB.md`

内容建议：

```markdown
# Reasonix Lab

Reasonix Lab is a local/offline subsystem for:

- trace collection
- cache contract verification
- harness snapshot management
- smoke/canary evaluation
- evidence distillation
- proposal manifest checking
- artifact garbage collection
```

提交：

```bash
git add docs/LAB.md
git commit -m "docs(lab): introduce Reasonix Lab local workflow"
```

---

# 7. P1：Trace 系统

第一版先做最小 trace，不要贪全。

## P1.1 新增 `internal/trace`

目标：

```text
Reasonix 每次 model/tool/session 事件都能写 JSONL。
```

建议文件：

```text
internal/trace/
├── event.go
├── writer.go
├── noop.go
├── redact.go
└── schema.go
```

核心类型：

```go
package trace

import "time"

type EventType string

const (
	EventSessionStart    EventType = "session_start"
	EventSessionEnd      EventType = "session_end"
	EventModelRequest    EventType = "model_request"
	EventModelResponse   EventType = "model_response"
	EventToolCall        EventType = "tool_call"
	EventToolResult      EventType = "tool_result"
	EventPermissionCheck EventType = "permission_check"
	EventCacheStats      EventType = "cache_stats"
	EventFileDiff        EventType = "file_diff"
	EventCompaction      EventType = "compaction"
	EventContractCheck   EventType = "cache_contract_check"
)

type Event struct {
	Version   string         `json:"version"`
	RunID     string         `json:"run_id"`
	SessionID string         `json:"session_id"`
	Turn      int            `json:"turn"`
	Type      EventType      `json:"type"`
	Time      time.Time      `json:"time"`
	Data      map[string]any `json:"data,omitempty"`
}
```

验收标准：

```text
1. 有 NoopWriter，不开启 trace 时零侵入。
2. 有 JSONL Writer，开启 trace 时一行一个事件。
3. writer 默认做基础 redaction。
4. 单元测试覆盖 JSONL 写入。
```

提交：

```bash
git add internal/trace
git commit -m "trace: add typed JSONL event sink"
```

---

## P1.2 接入 agent loop

目标：

```text
reasonix run/chat 能通过 flag 或 env 打开 trace。
```

建议先支持环境变量，侵入最小：

```bash
REASONIX_TRACE=.reasonix-ahe/traces/manual.trace.jsonl ./bin/reasonix run "explain this repo"
```

后续再加 CLI flag：

```bash
./bin/reasonix run --trace .reasonix-ahe/traces/manual.trace.jsonl "explain this repo"
```

最小事件：

```text
session_start
model_request
model_response
tool_call
tool_result
session_end
```

验收标准：

```text
1. 本地运行一次 reasonix run 能生成 trace JSONL。
2. trace 不包含明文 API key。
3. trace 中记录 model name、tool name、duration、exit code。
```

提交：

```bash
git add .
git commit -m "trace: record session model and tool events"
```

---

# 8. P2：Cache Contract

这是 Reasonix-AHE 的命门。DeepSeek 的 Reasonix 集成文档明确说 Reasonix 是 cache-first loop；Reasonix README 也强调其围绕 prefix cache 调优。([GitHub][1]) 所以第一版必须把 cache 变成硬约束，不是事后指标。

## P2.1 新增 `internal/cachecontract`

建议文件：

```text
internal/cachecontract/
├── contract.go
├── hash.go
├── canonical.go
└── validate.go
```

核心结构：

```go
package cachecontract

type Contract struct {
	SessionID         string `json:"session_id"`
	HarnessSnapshot  string `json:"harness_snapshot"`
	SystemPromptHash string `json:"system_prompt_hash"`
	ToolSchemaHash   string `json:"tool_schema_hash"`
	SkillIndexHash   string `json:"skill_index_hash"`
	MiddlewareHash   string `json:"middleware_hash"`
	StablePrefixHash string `json:"stable_prefix_hash"`
}
```

运行时逻辑：

```text
session start:
  build stable prefix hash
  store contract

before every model call:
  rebuild stable prefix hash
  compare with session contract

if drift:
  error or warning, depending on mode
  write trace event cache_contract_violation
```

第一版可以先默认 warning，等稳定后改成 hard error。
但 canary eval 中必须视为失败。

提交：

```bash
git add internal/cachecontract
git commit -m "cache: add stable prefix contract model"
```

---

## P2.2 工具 schema canonicalization

目标：

```text
同一个 tool registry 序列化 100 次，hash 完全一致。
```

测试建议：

```go
func TestCanonicalToolSchemaIsStable(t *testing.T) {
	for i := 0; i < 100; i++ {
		// serialize tool schema
		// compare hash
	}
}
```

规则：

```text
1. tool 顺序稳定。
2. JSON key 顺序稳定。
3. 不允许 timestamp。
4. 不允许 random id。
5. 不允许 map iteration 直接进入 model-visible schema。
```

提交：

```bash
git add .
git commit -m "cache: canonicalize model-visible tool schema"
```

---

## P2.3 记录 DeepSeek cache hit/miss

DeepSeek API 文档说明返回 usage 中可观察 `prompt_cache_hit_tokens` 和 `prompt_cache_miss_tokens`。([DeepSeek API Docs][2])

在 provider response 解析处增加：

```go
type CacheStats struct {
	PromptCacheHitTokens  int64   `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int64   `json:"prompt_cache_miss_tokens"`
	CacheHitRatio         float64 `json:"cache_hit_ratio"`
	Available             bool    `json:"available"`
}
```

计算：

```text
cache_hit_ratio =
  prompt_cache_hit_tokens /
  (prompt_cache_hit_tokens + prompt_cache_miss_tokens)
```

验收标准：

```text
1. trace 的 model_response 事件包含 cache stats。
2. provider 不返回时 available=false，不 panic。
3. cache hit ratio 可被 lab report 读取。
```

提交：

```bash
git add .
git commit -m "cache: record provider cache hit and miss tokens"
```

---

# 9. P3：Harness Snapshot

核心思想：

> **snapshot 只在 harness 版本变化时生成，不随用户对话增长。**

## P3.1 新增 `internal/harness`

建议文件：

```text
internal/harness/
├── layout.go
├── lock.go
├── snapshot.go
├── loader.go
└── hash.go
```

第一版 snapshot layout：

```text
.reasonix-harness/
├── source/
│   ├── prompts/
│   │   └── system.md
│   ├── tool_descriptions/
│   │   └── bash.md
│   ├── skills/
│   │   └── debug/SKILL.md
│   ├── middleware/
│   │   └── post_success_guard.toml
│   └── routing/
│       └── model_routing.toml
├── snapshots/
│   └── h-0001/
│       └── harness.lock
├── manifests/
└── active
```

`harness.lock` 示例：

```json
{
  "snapshot_id": "h-0001",
  "created_at": "2026-06-05T00:00:00+10:00",
  "system_prompt_hash": "sha256:...",
  "tool_description_hash": "sha256:...",
  "skill_index_hash": "sha256:...",
  "middleware_hash": "sha256:...",
  "model_routing_hash": "sha256:...",
  "stable_prefix_hash": "sha256:..."
}
```

提交：

```bash
git add internal/harness
git commit -m "harness: add snapshot layout and lock file"
```

---

## P3.2 新增本地命令：`reasonix lab harness`

第一版命令：

```bash
./bin/reasonix lab harness init
./bin/reasonix lab harness snapshot create
./bin/reasonix lab harness snapshot list
./bin/reasonix lab harness snapshot activate h-0001
./bin/reasonix lab harness inspect h-0001
```

验收标准：

```text
1. init 能创建 .reasonix-harness/source。
2. snapshot create 能生成 h-0001/harness.lock。
3. activate 能写入 active。
4. session_start trace 中能记录 active snapshot id。
```

提交：

```bash
git add .
git commit -m "lab: add harness snapshot commands"
```

---

# 10. P4：Eval Runner

不要第一步就接 Terminal-Bench 2。
先做 Reasonix-AHE 自己的最小任务格式。

## P4.1 任务格式

目录：

```text
benchmarks/ahe/smoke/python-bugfix-001/
├── task.toml
├── prompt.md
├── setup.sh
├── verify.sh
└── files/
```

`task.toml` 示例：

```toml
id = "python-bugfix-001"
name = "Fix failing parser test"
category = "bugfix"
difficulty = "smoke"
timeout_seconds = 300

[models]
default = "deepseek-v4-flash"

[cache]
min_hit_ratio = 0.90
max_contract_violations = 0

[verify]
command = "bash verify.sh"
```

提交：

```bash
mkdir -p benchmarks/ahe/smoke benchmarks/ahe/canary
git add benchmarks/ahe
git commit -m "bench: add AHE smoke and canary task layout"
```

---

## P4.2 新增 `reasonix lab eval`

目标命令：

```bash
./bin/reasonix lab eval benchmarks/ahe/smoke
```

输出：

```text
.reasonix-ahe/evals/
└── run-20260605-abc/
    ├── result.json
    ├── trace.jsonl
    ├── diff.patch
    ├── verify.log
    └── cache_report.json
```

`result.json` 示例：

```json
{
  "run_id": "run-20260605-abc",
  "task_id": "python-bugfix-001",
  "model": "deepseek-v4-flash",
  "harness_snapshot": "h-0001",
  "passed": true,
  "duration_ms": 182000,
  "cache_hit_ratio": 0.963,
  "prompt_cache_hit_tokens": 1248000,
  "prompt_cache_miss_tokens": 48000,
  "contract_violations": 0
}
```

验收标准：

```text
1. eval 能执行 setup.sh。
2. eval 能调用本地 bin/reasonix 运行 prompt.md。
3. eval 能执行 verify.sh。
4. eval 能保存 trace、diff、verify.log、result.json。
5. contract violation > 0 时任务失败。
6. cache_hit_ratio 低于任务阈值时任务 warning 或 fail。
```

提交：

```bash
git add internal/lab benchmarks/ahe
git commit -m "lab: add local smoke eval runner"
```

---

# 11. P5：Cache Report

新增命令：

```bash
./bin/reasonix lab cache-report .reasonix-ahe/evals/run-xxx/trace.jsonl
```

输出示例：

```text
Reasonix-AHE Cache Report

Model calls:                18
Prompt cache hit tokens:    12,480,000
Prompt cache miss tokens:   310,000
Cache hit ratio:            97.57%
Stable prefix hash drift:   no
Contract violations:        0
```

JSON 输出：

```bash
./bin/reasonix lab cache-report .reasonix-ahe/evals/run-xxx/trace.jsonl --json
```

验收标准：

```text
1. 能读取 trace JSONL。
2. 能聚合 cache hit/miss。
3. 能计算 cache_hit_ratio。
4. 能检测 cache_contract_violation。
5. 能作为 eval gate 使用。
```

提交：

```bash
git add .
git commit -m "lab: add cache report and regression gate"
```

---

# 12. P6：Evidence Distiller

v0.1 先做 deterministic distiller，不必一开始就用模型总结。

## P6.1 单任务报告

命令：

```bash
./bin/reasonix lab distill .reasonix-ahe/evals/run-xxx
```

输出：

```text
.reasonix-ahe/evals/run-xxx/evidence/
├── task-python-bugfix-001.md
├── task-go-test-repair-001.md
└── clusters.md
```

单任务报告示例：

```markdown
# Task Report: python-bugfix-001

Result: FAILED
Harness snapshot: h-0001
Model: deepseek-v4-flash
Cache hit ratio: 94.1%
Contract violations: 0

## Last verifier output

...

## Tool-call summary

- read_file: 4
- grep: 3
- edit_file: 1
- bash: 5

## Suspected failure pattern

- verifier failed after partial success
- agent did not run verify.sh before final response

## Suggested components

- middleware/post_success_guard.toml
- tool_descriptions/bash.md
```

## P6.2 Failure taxonomy

第一版枚举：

```go
type FailureKind string

const (
	FailureVerifierFailed      FailureKind = "verifier_failed"
	FailureTimeout             FailureKind = "timeout"
	FailureToolErrorLoop       FailureKind = "tool_error_loop"
	FailurePrematureSuccess    FailureKind = "premature_success"
	FailurePermissionDenied    FailureKind = "permission_denied"
	FailureCacheContractBroken FailureKind = "cache_contract_broken"
	FailureNoPatch             FailureKind = "no_patch"
	FailurePatchDoesNotApply   FailureKind = "patch_does_not_apply"
)
```

提交：

```bash
git add .
git commit -m "lab: add deterministic evidence distiller"
```

---

# 13. P7：Proposal Manifest

v0.1 不自动 evolve，但要定义“未来自动演化时必须遵守的字据”。

## P7.1 Manifest schema

新增：

```text
docs/schemas/evolution_manifest.schema.json
```

示例：

```json
{
  "proposal_id": "p-0001-post-success-guard",
  "base_snapshot": "h-0001",
  "target_snapshot": "h-0002",
  "components_changed": [
    "middleware/post_success_guard.toml",
    "tool_descriptions/bash.md"
  ],
  "evidence": [
    "canary/post-success-verification-001 failed after partial test pass"
  ],
  "root_cause": "Agent finalized after narrow success signal without running verifier-equivalent command.",
  "expected_fixes": [
    "canary/post-success-verification-001"
  ],
  "regression_risks": [
    "Tasks with expensive full verification may become slower."
  ],
  "cache_risk": {
    "stable_prefix_changed": true,
    "expected_hit_ratio_delta": -0.01
  },
  "acceptance_rules": {
    "min_smoke_pass_rate": 0.8,
    "min_canary_pass_rate": 0.8,
    "min_cache_hit_ratio": 0.9,
    "max_contract_violations": 0
  },
  "rollback_rule": "Revert if canary pass rate drops or cache_hit_ratio < 0.90."
}
```

## P7.2 Proposal 命令

目标：

```bash
./bin/reasonix lab proposal create \
  --base h-0001 \
  --name post-success-guard
```

生成：

```text
.reasonix-ahe/proposals/
└── p-0001-post-success-guard/
    ├── manifest.json
    ├── evidence.md
    └── diff.patch
```

检查：

```bash
./bin/reasonix lab proposal check .reasonix-ahe/proposals/p-0001-post-success-guard
```

验收标准：

```text
1. proposal 没有 manifest 不允许 check 通过。
2. manifest 必须声明 expected_fixes。
3. manifest 必须声明 regression_risks。
4. manifest 必须声明 cache_risk。
5. manifest 必须声明 rollback_rule。
```

提交：

```bash
git add .
git commit -m "lab: add proposal manifest schema and checker"
```

---

# 14. P8：GC / Quota

这是防止本地项目变成“日志黑洞”的关键。

## P8.1 GC policy

配置示例：

```toml
[lab.gc.harness]
keep_last_accepted = 50
keep_rejected_days = 7
keep_proposals_days = 14

[lab.gc.sessions]
keep_full_transcript_days = 30
keep_compacted_summary_days = 365

[lab.gc.traces]
keep_raw_days = 14
keep_failed_raw_days = 30
keep_evidence_days = 365
max_total_trace_bytes = "10GB"

[lab.gc.quota]
max_total_reasonix_ahe_dir = "20GB"
max_snapshots = 200
max_eval_runs = 50
max_single_tool_output = "2MB"
max_concurrent_agents = 4
```

## P8.2 GC 命令

```bash
./bin/reasonix lab gc --dry-run
```

输出示例：

```text
Would delete:
- 14 old raw traces, 3.2GB
- 8 rejected proposals, 1.1MB
- 2 closed worktrees, 420MB

Would keep:
- active snapshot h-0042
- pinned snapshot h-0039
- 6 snapshots referenced by open sessions
```

验收标准：

```text
1. dry-run 不删除任何文件。
2. 能显示删除原因和保留原因。
3. active snapshot 不会被删。
4. open session 引用的 snapshot 不会被删。
5. failed eval 的 evidence 默认比 raw trace 保留更久。
```

提交：

```bash
git add .
git commit -m "lab: add GC dry-run and quota policy"
```

---

# 15. 推荐执行顺序

第一版不要开大杂烩式修改。建议严格按这个顺序：

```text
0. clone + build + tag baseline
1. docs/AHE.md
2. docs/LAB.md
3. internal/trace
4. agent loop trace integration
5. internal/cachecontract
6. tool schema canonicalization
7. DeepSeek cache hit/miss recording
8. internal/harness snapshot + harness.lock
9. lab harness commands
10. benchmarks/ahe task schema
11. lab eval runner
12. lab cache-report
13. lab distill
14. proposal manifest schema
15. lab gc --dry-run
```

对应本地提交历史可以长这样：

```text
docs(ahe): add local Reasonix-AHE design RFC
docs(lab): introduce Reasonix Lab local workflow
trace: add typed JSONL event sink
trace: record session model and tool events
cache: add stable prefix contract model
cache: canonicalize model-visible tool schema
cache: record provider cache hit and miss tokens
harness: add snapshot layout and lock file
lab: add harness snapshot commands
bench: add AHE smoke and canary task layout
lab: add local smoke eval runner
lab: add cache report and regression gate
lab: add deterministic evidence distiller
lab: add proposal manifest schema and checker
lab: add GC dry-run and quota policy
```

这串 commit 以后给 owner 看，会非常清楚：你不是在乱改 prompt，而是在给 Reasonix 建“观测仪表盘 + 缓存护城河 + 本地试炼场”。

---

# 16. 最小可交付版本

如果你想先压缩到一个真正能跑的小版本，只做这 7 个：

```text
MVP-1 clone/build/baseline tag
MVP-2 docs/AHE.md + docs/LAB.md
MVP-3 typed JSONL trace sink
MVP-4 model/tool/cache events
MVP-5 cache contract + stable prefix hash
MVP-6 harness snapshot + harness.lock
MVP-7 lab eval + cache-report
```

MVP 完成时，你应该能跑：

```bash
make build

./bin/reasonix lab harness init
./bin/reasonix lab harness snapshot create
./bin/reasonix lab eval benchmarks/ahe/smoke
./bin/reasonix lab cache-report .reasonix-ahe/evals/<run-id>/trace.jsonl
```

并得到：

```text
1. result.json
2. trace.jsonl
3. cache_report.json
4. diff.patch
5. verify.log
```

---

# 17. v0.1 Definition of Done

第一版完成标准可以这样定：

## 功能完成

```text
1. Reasonix-AHE 可以从 upstream/main-v2 本地构建。
2. 本地实验分支 ahe/local-v0.1 独立存在。
3. 能启动 Reasonix session 并绑定 active harness snapshot。
4. 能输出 typed JSONL trace。
5. trace 包含 model/tool/cache/session 关键事件。
6. 能检测 stable prefix drift。
7. 能运行 smoke eval task。
8. 能生成 result.json / trace / diff / verify.log。
9. 能生成 cache-report。
10. 能 dry-run GC。
```

## 缓存完成

```text
1. tool schema 连续序列化 hash 稳定。
2. live session 内 contract violations = 0。
3. cache_hit_ratio 可从 trace 聚合。
4. eval gate 能基于 cache_hit_ratio 和 contract violations 判定失败。
```

## 安全完成

```text
1. 不会 push 到 upstream。
2. trace 默认 redaction。
3. proposal 不自动 apply。
4. v0.1 不允许自动修改 internal/ core runtime。
5. raw trace / eval artifacts 不进入 git。
```

---

# 18. 之后和 owner 交流时可以怎么说

等你做完本地 MVP，可以这样概括：

```text
我没有改 Reasonix 的核心产品方向，也没有破坏 prefix-cache 设计。
我做的是一个本地 Reasonix Lab 原型：

1. typed trace：记录 model/tool/cache events
2. cache contract：检测 stable prefix drift
3. harness snapshot：让 harness 版本化而不是 live mutation
4. smoke eval runner：本地小任务评测
5. cache report：把 cache hit ratio 作为回归指标
6. evidence / manifest：为未来自动 harness evolution 做准备

v0.1 不自动改 core，不自动 merge proposal，不把 eval evidence 注入 live prompt。
```

这套表述很稳。它告诉 owner：
你不是来“魔改 Reasonix”的，而是来增强 Reasonix 的工程可观测性和长期演化能力。

---

# 19. 第一版最该避免的坑

```text
1. 不要一上来重命名 module path。
2. 不要一上来把所有 prompt 外置。
3. 不要一上来做自动 evolve agent。
4. 不要把 trace summary 注入 system prompt。
5. 不要让 snapshot 随每次对话生成。
6. 不要把 eval artifacts 提交进 git。
7. 不要先接 Terminal-Bench 2 全量。
8. 不要把 cache_hit_ratio 当成“nice to have”。
```

Reasonix 的根基是 cache-first。
Reasonix-AHE 的第一条律法应当是：**任何演化不得破坏缓存稳定性。**

---

# 20. 最后给你一段本地启动脚本

可以保存成：

```bash
bootstrap-reasonix-ahe.sh
```

内容：

```bash
#!/usr/bin/env bash
set -euo pipefail

ROOT="${HOME}/dev/agent-lab"
REPO="Reasonix-AHE"

mkdir -p "$ROOT"
cd "$ROOT"

if [ ! -d "$REPO" ]; then
  git clone --branch main-v2 --single-branch https://github.com/esengine/DeepSeek-Reasonix.git "$REPO"
fi

cd "$REPO"

if git remote get-url origin >/dev/null 2>&1; then
  git remote rename origin upstream
fi

git remote set-url --push upstream DISABLED || true

cat > .git/hooks/pre-push <<'HOOK'
#!/usr/bin/env bash
remote="$1"

if [ "$remote" = "upstream" ]; then
  echo "Refusing to push to upstream. This is a local Reasonix-AHE experiment."
  exit 1
fi
HOOK

chmod +x .git/hooks/pre-push

if ! git rev-parse --verify ahe/local-v0.1 >/dev/null 2>&1; then
  BASE_DATE=$(date +%Y%m%d)
  git tag "local/baseline-main-v2-${BASE_DATE}" || true
  git checkout -b ahe/local-v0.1
else
  git checkout ahe/local-v0.1
fi

cat >> .git/info/exclude <<'EOF'

# Reasonix-AHE local artifacts
.reasonix-ahe/
.reasonix-harness/snapshots/
.reasonix-harness/active
.reasonix-harness/proposals/
*.trace.jsonl
*.trace.jsonl.zst
*.eval.json
*.secret
.env.local
EOF

mkdir -p .reasonix-ahe/{traces,evals,reports,proposals,gc}
mkdir -p .reasonix-harness/{source,snapshots,manifests}
mkdir -p docs internal/trace internal/cachecontract internal/harness internal/lab
mkdir -p benchmarks/ahe/{smoke,canary}

echo "Current branch:"
git branch --show-current

echo "Current baseline:"
git log -1 --oneline

echo "Building Reasonix..."
make build

echo "Done. Local Reasonix-AHE workspace is ready."
```

运行：

```bash
chmod +x bootstrap-reasonix-ahe.sh
./bootstrap-reasonix-ahe.sh
```

---

## 总结

你现在的第一步，不是“让 Reasonix 会自动进化”，而是：

> **把 Reasonix-AHE 这个本地项目立起来，让它拥有 trace、cache contract、snapshot、eval、cache-report 这五块基石。**

基石稳了，后面的 evidence distiller、proposal manifest、半自动 evolve、DeepSeek-v4 专项 harness 优化，才不会变成空中楼阁。

我建议你第一轮就按这个顺序开工：

```text
clone → baseline build → docs → trace → cache contract → snapshot → eval → cache report
```

先铸镜，再磨剑。Reasonix 的锋芒在 cache，AHE 的魂魄在 observability；二者相合，才是 Reasonix-AHE 的正路。

[1]: https://github.com/esengine/deepseek-reasonix "GitHub - esengine/DeepSeek-Reasonix: DeepSeek-native AI coding agent for your terminal. Engineered around prefix-cache stability — leave it running. · GitHub"
[2]: https://api-docs.deepseek.com/quick_start/agent_integrations/reasonix "Integrate with Reasonix | DeepSeek API Docs"
[3]: https://arxiv.org/abs/2604.25850 "Agentic Harness Engineering: Observability-Driven Automatic Evolution of Coding-Agent Harnesses"
