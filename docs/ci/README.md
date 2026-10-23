# CI 门禁的排队状态与「排队 vs 真在跑」判据

> 关联票：[#550](https://github.com/magician336/lingdoc/issues/550)。
> 本文件记录该票的两项决定：**门禁 run 的取消口径**，以及**「排队中」与「真在跑／已失败」的机器可读判据**。
> 判据脚本：[`scripts/ci/lingdoc_gate_queue_state.py`](../../scripts/ci/lingdoc_gate_queue_state.py)。

## 1. 问题

仓库的门禁工作流 [`lingdoc-review.yml`](../../.github/workflows/lingdoc-review.yml) 把 `LingDoc PR gate` 跑在 **hosted runner** 上（`runs-on: ubuntu-latest`），仓库**没有任何自托管 runner**：

```console
$ gh api repos/magician336/lingdoc/actions/runners --jq '.total_count'
0
```

GitHub 对「还没跑」和「正在跑」只显示一个词：`in_progress`。于是下面两种情况在人眼里一模一样：

| 观测 | 含义 |
| --- | --- |
| `started_at == null` ＋ `status=in_progress` | **排队中**：这个 job 还没拿到 runner |
| `started_at` 有值 ＋ `status=in_progress` | **真在跑**：这个 job 正在跑步骤 |

分不清的后果是连锁的：排队中的 run 在 `cancel-in-progress: true` 下会被下一次 push **取消**，于是「推新 commit → 排队的 run 被取消 → 重新排队 → runner 一直排不上 → 门禁永远 `in_progress`」。人看到 `in_progress` 会以为「测试挂住了」，于是继续推 head。

本仓的合并授权口径是「`LingDoc PR gate` 在**当前 head** 真绿」。**一次新 push 把唯一一次验证取消掉，破坏的正是放行链依赖的那个不变量。**

## 2. 口径裁定：`cancel-in-progress: false`

`.github/workflows/lingdoc-review.yml` 的 `concurrency` 块现在是：

```yaml
concurrency:
  group: lingdoc-review-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: false
```

依据：

1. **与放行链一致。** 每个 head 各自跑完一次门禁。旧 head 的 run 对当前 head 没有意义，但**无害**；而它不会再因为一次新 push 而**没跑成**。
2. **不引入自托管 runner。** 那需要部署侧提供机器，当前 `total_count = 0`，不在本仓可自助解决的范围内。
3. **代价可接受，而且可量化。** 旧 run 不是和新 run **抢** runner，而是**挡在**新 run 前面：同一个 PR 的新 head 与旧 head 在**同一个 concurrency group** 里，新 head 的门禁要等旧 head 的门禁跑完才开工。实测这个等待是 **1107 秒**（见 §5.2）。本仓 PR 数量少、每个 PR 推 head 次数有限，这个代价在本仓可接受。

**没有**因此改动任何测试覆盖：`SKIPPED` 仍不等于跑过（#482 / AC-04-05）；ruleset 对「当前 head 门禁真绿」的要求**未变**；`runs-on`、job 的触发条件**未动**。

## 3. 判据脚本

### 3.1 命令

```console
$ python scripts/ci/lingdoc_gate_queue_state.py --pr 550
$ python scripts/ci/lingdoc_gate_queue_state.py --run 40022872106
```

可选 `--repo owner/name`；不给时依次取环境变量 `GITHUB_REPOSITORY`、`origin` 远端、最后是 `magician336/lingdoc`。数据经 `gh api` 读取，全部是 `GET`。

### 3.2 判据

**判据建立在 `started_at` 是否为空上，不靠 `status` 字面量猜测。**

- `started_at` 字段存在且为 `null` ⇒ **排队中**（GitHub 明说「这个 job 还没开始」）；
- `started_at` 字段存在且有值 ⇒ **真在跑**；
- `started_at` 字段**根本不存在** ⇒ **无法判定**（GitHub 什么都没说，**不得**当成「没排队」或「在跑」）。

**记录优先级：job 记录优先于聚合 check-run。** job 记录（`/actions/runs/{id}/jobs`）是 runner 分配的记录；聚合 check-run（`/commits/{sha}/check-runs`）是汇总视角。二者不一致时以 job 为准——票面记录的「聚合 check `in_progress` 但同一次 run 的所有 job 都已 completed」正是这种不一致。

**旁证：** 同一个 head 上其它 check 全部 `completed`、只有门禁还开着 ⇒ 门禁是唯一还在等 runner 的 job（票面第三行观测）。旁证只进 `evidence`，不参与判定。

### 3.3 输出

stdout 是一个 JSON 对象：

| 字段 | 含义 |
| --- | --- |
| `pr` / `run_id` / `head_sha` | 被判定的对象 |
| `gate_state` / `verdict` | 判定结果，取值见下表 |
| `queued` / `running` / `passed` / `failed` / `missing` | 判定结果的布尔形式 |
| `reason` | 为什么是这个判定（人读） |
| `observed_status` / `observed_conclusion` | GitHub 原始的 `status` / `conclusion` |
| `next_action` | 读者该怎么做（人读） |
| `evidence` | 判据依据：门禁 check-run、门禁 job、该 run 的全部 job、该 head 的全部 check，以及 `all_other_jobs_completed` / `all_other_checks_completed` 两个旁证 |
| `checked_at` | 判定时刻（UTC） |
| `exit_code` | 与进程退出码一致 |

`evidence` 里每个字段的取值是 GitHub 的原始值；**字段不存在时写作 `<absent>`**，与「值为 `null`」区分开。

### 3.4 退出码

| 判定 | 退出码 | 含义 |
| --- | --- | --- |
| `queued` | 0 | 排队中，等 runner |
| `running` | 0 | 正在跑 |
| `passed` | 0 | 这个 head 门禁真绿 |
| `failed` | 1 | 门禁没过（`failure` / `cancelled` / `timed_out` / `skipped` …） |
| `missing` | 2 | 判不出来：没有门禁 check，或数据不足以判定 |

`queued` 与 `running` 是**正常状态**，退出 0：盯门禁的脚本读 JSON，不读退出码。只有 `failed` 与 `missing` 是非 0，而且两者**非零值不同**——调用方能区分「门禁说了不行」与「门禁问不出结果」。

`skipped` 判为 `failed`：skipped 是 NOT RUN，不是跑过。

### 3.5 「要不要再推」的决策规则

| 判定 | 人 / 脚本该做什么 |
| --- | --- |
| `queued` | **不要推。** 再推不会让队列变短，只会让队列里多一个还在等的 run。等这个 run 拿到 runner。 |
| `running` | **不要推。** 等它跑完。 |
| `passed` | 这个 head 绿了。 |
| `failed` | 这个 head 不绿。看 run 日志。 |
| `missing` | 这个 head **按不绿处理**。去 run 列表看为什么没有门禁 check。 |

推新 head 之前先跑一次这个判据：`queued` 时再推是纯粹的浪费（而且在旧口径下还会取消掉正在排队的那一次验证）。

在新口径下，`queued` 有两种成因：**等 runner**（供给问题）与**等前一个 head 的门禁**（concurrency group 排队）。两者的处置一样：**等，不要推**。要区分它们看 `evidence.run.status`：`pending` 是后者（等前一个 head），`queued` 是前者（等 runner）。

### 3.6 实跑样本

判据脚本对本 PR 自己的一次实跑（PR #562，run `40024991786`，判定时刻 `2026-10-23T21:12:19Z`）：

```console
$ python scripts/ci/lingdoc_gate_queue_state.py --pr 562
{
  "pr": 562,
  "run_id": 40024991786,
  "head_sha": "e9dc083d5714fdabb624d9c2068df1ee861faa63",
  "gate_state": "running",
  "verdict": "running",
  "queued": false,
  "running": true,
  "passed": false,
  "failed": false,
  "missing": false,
  "reason": "the gate job has started; it is running its steps",
  "observed_status": "in_progress",
  "observed_conclusion": null,
  "next_action": "The gate is running. Wait for it to finish; do not push again.",
  "evidence": {
    "gate_job": {
      "id": 120658826821,
      "name": "LingDoc PR gate",
      "status": "in_progress",
      "conclusion": null,
      "started_at": "2026-10-23T21:12:03Z",
      "completed_at": null
    },
    "all_other_jobs_completed": false,
    "all_other_checks_completed": true,
    "run": {
      "id": 40024991786,
      "status": "in_progress",
      "conclusion": null,
      "cancelled": false,
      "created_at": "2026-10-23T21:12:01Z",
      "run_started_at": "2026-10-23T21:12:01Z"
    }
  },
  "checked_at": "2026-10-23T21:12:19Z",
  "exit_code": 0
}
```

注意这一帧里 `all_other_checks_completed` 是 `true`（该 head 上其它 check 都已结束），而判定仍然是 `running` 而不是 `queued`——因为门禁自己的 `started_at` 有值。**旁证不参与判定**，判定只认 `started_at`。

## 4. 守护测试

[`scripts/ci/test_lingdoc_gate_queue_state.py`](../../scripts/ci/test_lingdoc_gate_queue_state.py) 覆盖票面的六行判据：

| 场景 | 期望判定 |
| --- | --- |
| ① `started_at=null` ＋ `in_progress` | `queued` |
| ② `started_at` 有值 ＋ `in_progress` | `running` |
| ③ 聚合 check `in_progress`，但同 head 的其它 check 都 completed | `queued` |
| ④ gate `SUCCESS` | `passed` |
| ⑤ gate `FAILURE` | `failed` |
| ⑥ 数据缺失（没有 check / 没有 `started_at` / 没有 conclusion） | `missing`，**且不得**是 `passed` / `queued` / `running` |

另含：`status` 字面量不参与判定的验证（`queued` / `waiting` / `pending` 等各种状态词配 `started_at=null` 一律判 `queued`）、job 记录优先于聚合 check 的验证、退出码语义、工作流策略（`cancel-in-progress` 仍为 `false`）的守护。

测试不触网：它把 GitHub API 会发布的记录喂给判定函数。

## 5. 容量基线（AC-3）

数据来源：`gh api` 取 `LingDoc Review` 的 run 与 job 记录（`created_at` / `started_at` / `completed_at`）。

### 5.1 「排队时长」有两个来源

「run 建好 → 门禁 job 开始」这段时间里可能包含两件事：

- **runner 供给排队**：等 GitHub 分配一台 hosted runner。
- **concurrency group 等待**：同一个 PR 的上一个 head 的门禁还在跑，新 head 的 run 在同一个 concurrency group 里排队，等前一个结束。

两者表现为同一个时间差，所以下表里的「排队时长」是**两者之和**。判据脚本把它们分开：`evidence.run.status` 为 `pending` 说明它在等 concurrency group，为 `queued` 说明它在等 runner；`evidence.gate_job.started_at` 为 `null` 说明两者都还没到。

### 5.2 新口径（`cancel-in-progress: false`）下的实测

新口径下的样本就是本 PR 自己：同一个 PR 连推多个 head，每个 head 的门禁都在**同一个 concurrency group** 里依次跑。

| run id | head | 排队(s) | 其中 concurrency 等待(s) | 运行(s) | 合计(s) | conclusion |
| --- | --- | --- | --- | --- | --- | --- |
| 40024991786 | `e9dc083d` | 2 | 0 | 1144 | 1146 | success |
| 40025077061 | `9caffeca` | 1107 | 1107 | 611 | 1718 | success |
| 40028688178 | `382ec325` | 2 | 0 | 826 | 828 | success |

（表中列的是本 PR 的**前三个 head** 的 run。同一个 PR 的第 N 个 head 排队多久，取决于它前面还有几个 head 的门禁在排队。）

第二个 head 的门禁**等了 18 分 27 秒**才开工。这不是 runner 供给不足——它是排在**前一个 head 的门禁后面**（实测 run 状态先是 `pending`，前者结束后才转 `in_progress`）。第三个 head 前面没有未完成的门禁，所以排队只有 2 秒。

**这就是 `cancel-in-progress: false` 的确切代价**：新 head 的门禁要等前一个 head 的门禁跑完。因此：

- 一个 PR 推 N 次 head ⇒ 门禁**串行**跑 N 次，当前 head 的门禁最后跑完。
- 实测一次全量门禁约 10–19 分钟（见下），所以推 3 次 head 约等于多等 10–19 分钟。

后一次的运行时长（611 s、826 s）比第一次（1144 s）短，是因为前一个 run 把 Go 的构建缓存焐热了——串行的代价因此小于「N × 单次时长」。

### 5.3 旧口径（`cancel-in-progress: true`）下的样本

样本 = 2026-10-23 18:24Z–21:07Z 之间最近 10 个门禁 run，全部 `conclusion=success`。

| 分量 | 定义 | 样本取值 | 中位数 | 最大 |
| --- | --- | --- | --- | --- |
| **排队时长** | run `created_at` → 门禁 job `started_at` | 2, 2, 2, 2, 3, 3, 3, 3, 3, 8 秒 | 3 秒 | 8 秒 |
| **运行时长** | 门禁 job `started_at` → `completed_at` | 71 … 1217 秒 | ≈110 秒 | 1217 秒（20 分 17 秒） |
| **合计** | run `created_at` → 门禁 job `completed_at` | 73 … 1225 秒 | ≈117 秒 | 1225 秒 |

逐 run 明细：

| run id | head branch | 排队(s) | 运行(s) | 合计(s) | conclusion |
| --- | --- | --- | --- | --- | --- |
| 40024380522 | docs/139-f0107-gap-545 | 2 | 71 | 73 | success |
| 40022872106 | chore/registry-pf14-paths | 3 | 106 | 109 | success |
| 40022852013 | docs/139-f0107-gap-545 | 2 | 115 | 117 | success |
| 40022230426 | docs/139-f0107-gap-545 | 3 | 83 | 86 | success |
| 40020678358 | fix/534-delayed-retry-budget | 3 | 885 | 888 | success |
| 40019689822 | feat/535-role-vocabulary | 2 | 354 | 356 | success |
| 40018775028 | fix/543-generation-error-text | 3 | 337 | 340 | success |
| 40018345183 | chore/139-pg17-acceptance | 2 | 111 | 113 | success |
| 40017909687 | fix/534-delayed-retry-budget | 3 | 669 | 672 | success |
| 40016064550 | chore/139-pg17-acceptance | 3 | 93 | 96 | success |

⚠️ **这 10 个样本采集于旧口径**：每次 push 都会取消上一个 run，所以后一个 head 从不需要等前一个 head——它们的排队时长里**没有** concurrency 等待。**因此 5.3 的排队时长不能代表新口径的成本**；新口径的成本见 5.2。

### 5.4 局限（如实标注）

- **5.3 的样本量是 10 个 run**，且集中在**同一天、同一批 PR**（多为 `docs/139-f0107-gap-545`、`chore/registry-pf14-paths`、`fix/534-delayed-retry-budget`）。它覆盖的是**常规改动**的门禁成本；`scripts/acceptance/**` 一类改动会额外触发 `LingDoc Interface Integration`（拉 PostgreSQL 17 service），量级另计（见 #539）。
- **新口径的样本量很小**（本 PR 自己的前三个 head，共 3 个 run），且都在同一天、同一个 PR、同一种改动类型（治理类改动 ⇒ 强制全量）。它们不能代表一般情况。
- **全量门禁的成本由「治理类改动强制全量」决定，而不是本票的改动决定。** `lingdoc_gate_decision.py` 的口径是「governance changed; the whole scope is tested」，所以任何 `scripts/ci/**` 或 `.github/workflows/**` 的改动都会跑 provider `go test -race`（实测 883 s）＋ 前端（实测 136 s）。本票的 `cancel-in-progress` 改动**不增加**任何测试量。
- 运行时长最大值（1217 秒）来自 PR #536 的 head `06c69372`（run `40002984905`）——**票面把它记为「长期 `in_progress`、排不上队」，但它最终跑完并变绿了**：排队 8 秒、运行 1217 秒。也就是说那次观测到的是「排队 + 长运行」叠在一起被人读成了「排不上队」，而不是永远排不上。
- **runner 供给紧张时的排队时长仍未被任何样本覆盖。** 本基线只回答「供给正常时的成本是多少」，不回答「供给最差时会排多久」。这是保留项，不是已解决的问题。

## 6. 边界

- 本判据**只报告门禁在做什么**。它不决定合并，不改 ruleset 对「当前 head 门禁真绿」的要求。
- 本判据**没有**接进合并流程，也**没有**改成自动触发——那是别的票的范围。
- 本票**没有**降低任何测试覆盖，**没有**动 `runs-on`，**没有**引入自托管 runner，**没有**改 job 的触发条件。
- workflow run 对象**没有** `cancelled_at` 字段（只有 `cancel_url`）。一次 run 被取消，是通过 `conclusion == "cancelled"` 表达的——脚本的 `evidence.run.cancelled` 就是这么算的。
