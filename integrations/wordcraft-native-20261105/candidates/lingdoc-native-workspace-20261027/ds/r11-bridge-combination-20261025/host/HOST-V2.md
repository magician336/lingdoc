# HOST-V2 — WordCraft 固定宿主 v2（历史版本读取 + 双会话冲突验证）

创建：2026-10-24（本轮）　分支：DSH 分支（宿主实现手）
宿主目录：`reports/wordcraft-delivery-v1-20261024/rebuild/host-v2-20261024/`
基线：`reports/wordcraft-delivery-v1-20261024/candidate/host/`（**只读**，未改动，见 §8 核对）

---

## 1. 一句话结论

在**新目录**里做出宿主 v2：v1 的 open/save/font 语义、同源+token+Host 三重校验、端口保护、静态白名单全部保留；
新增 `POST /api/version`（只读历史版本读取，字节 sha256 与"该版本回执"逐次核对，**不动 state.json 指针、不新建/删除任何文件**）；
新增 `data/versions.json` 回执索引（启动时从 `state.json` 反向重建 v1，此后每次成功保存追加一条）；
新增 `WORDCRAFT_WEB_DIR` 指定 web 产物目录（默认仍 `../../candidate/web`，启动打印目录+两产物哈希）。

四项验证**全部真跑通过**（合计 95 项断言 + 14 项编排断言）：

| 验证 | 证据 | 断言数 | 结果 |
|---|---|---|---|
| A/B.1 `/api/version` 契约（含 404/400/篡改/指针不变） | `evidence/01-version-read.json` | 36 | 全过 |
| B.2 双会话并发冲突 + 可恢复重试 | `evidence/02-dual-session.json` | 24 | 全过 |
| B.3 原 17 项契约回归（+ 原版 v1 工具复跑） | `evidence/03-contract-smoke-17.json`、`evidence/03b-original-contract-tool.json` | 22（含 17 项） | 全过；原版工具 16/16 |
| A.2/A.3 `WORDCRAFT_WEB_DIR` + 端口保护 | `evidence/04-web-and-guards.json` | 13 | 全过 |
| 编排/收尾（端口释放、外来端口属主不变、无残留进程） | `evidence/00-run-all.json` | 14 | 全过 |

---

## 2. 交付物清单

```
rebuild/host-v2-20261024/
  server.mjs             v2 宿主（v1 的 282 行 → v2 ~430 行；改动集中在 §3）
  start.ps1              v2 启动器（默认端口 5321；可选 -WebDir）
  stop.ps1               v2 停止器（只杀本 v2 树、只杀 node+server.mjs，拒绝 5255/7100/7101）
  font-manifest.json     12 项字体清单（原样复制，未改）
  site/{index.html,boot.js,host.js}   v1 页面（原样复制，未改；本轮不做 UI 接线）
  tools/                 从 candidate/host/tools 原样复制（含 contract-smoke.mjs，字节级未改）
  data/                  宿主自己的数据目录（saved-v1.docx + state.json 来自 v1；versions.json 由 v2 启动生成）
  data-seed/             上述两者的**原始副本**（每个测试都从它复制一份隔离数据目录，保证可复跑）
  tests/                 lib.mjs + 4 个验证脚本 + run-all 编排（见 §7）
  evidence/              00/01/02/03/03b/04 共 6 个 JSON（机器可读结论）
  runs/<stamp>/<test>/   本轮测试的隔离工作副本（data/audit，可随时删，不影响复跑）
  HOST-V2.md             本文件
```

日志（仓内 `logs/`，命名 `host-v2-*`）：

| 文件 | 用途 |
|---|---|
| `logs/host-v2-run-all.log` | run-all 全量输出（含每项测试耗时与结论） |
| `logs/host-v2-<test>-server.log` | 各测试宿主进程的 ready 行 + stdout + stderr |
| `logs/host-v2-audit.jsonl` | start.ps1 启动的那次宿主审计（含 `version_ok`） |
| `logs/host-v2-smoke-audit.jsonl` | 17 项回归那次宿主的审计 |
| `logs/host-v2-launcher-check.log` | start.ps1/stop.ps1 手工联调记录 |
| `logs/host-v2-launcher-version-body.json` | 上述联调时 `/api/version` 的请求体 |

---

## 3. 相对 v1 的改动（server.mjs）

1. **`POST /api/version`（新增）**
   请求 `{diskVersion: int}`，成功返回：
   ```json
   {"data":"<base64>","sha256":"<64hex>","bytes":26132,"diskVersion":1,
    "receipt":{…该版本回执…},"savedAt":"2026-10-24T14:10:40.903Z"}
   ```
   行为与边界：
   - 文件固定为 `data/saved-v<N>.docx`（回执里的 `file` 必须自洽：文件名中的 N == `diskVersion`，且解析后父目录必须是 `DATA`，否则 500 配置错误）；
   - 每次调用**重新读取并重新哈希**该文件，`sha256` 与 `bytes` 必须同时等于该版本回执的 `sha256`/`bytes`，否则 `500 {"error":"version hash mismatch"}`；
   - 版本不存在（索引里没有，或文件读不到）→ `404 {"error":"unknown disk version"}`；
   - 参数非法（缺失／字符串／负数／小数／null／布尔／数组／超 `Number.MAX_SAFE_INTEGER`）→ `400 {"error":"invalid diskVersion"}`；
   - 非 POST → `405 {"error":"POST required"}`；缺/错 token、非回环 `Host`、与已验证 `Host` 不一致的 `Origin` → `403`；`127.0.0.1` 与 `localhost` 仅允许使用当前监听端口；
   - **只读**：该分支不读也不写 `state.json`，不创建、不删除任何文件（§5.2 用"调用前后目录清单 + state.json/versions.json 双哈希"逐次证明）。
2. **`data/versions.json`（新增，回执索引）**
   ```json
   {"schema":"wordcraft-versions-v1","updatedAt":"…",
    "entries":[{"diskVersion":1,"baseDiskVersion":0,"file":"saved-v1.docx","sha256":"c2bd…",
                "bytes":26132,"documentId":2,"revision":20,"saveToken":"…","verified":true,"savedAt":"…"}]}
   ```
   - 启动时若索引缺失 → **从 `state.json` 反向重建**（这就是既有 `saved-v1.docx` 的索引来源），并原子落盘；
   - 每次成功保存（写完 `saved-vN.docx`、校验回读哈希、原子换 `state.json` 指针之后）→ 内存索引追加 + 原子落盘；
   - 只信任"当前回执 + 追加回执"：没有回执的 `saved-vN.docx`（例如脏写残留）**不做猜测**，一律 404；
   - 索引落盘失败 → 保存返回 500（快照与指针已持久，同一 payload 幂等重放即可恢复），并记审计 `version_index_failed`。
3. **`WORDCRAFT_WEB_DIR`（新增，A.2）**
   取代写死的 web 目录；默认 `path.resolve(ROOT,'..','..','candidate','web')`。两个产物
   （`wordcraft.js`、`wordcraft_bg.wasm`）在**开监听之前**读取，缺任一 → 启动失败并打印
   `web artifact unreadable at …`；ready 行打印 `web.dir`、`web.source`（`default`｜`WORDCRAFT_WEB_DIR`）、
   以及两产物的 `bytes`/`sha256`（另起一行 `{"webArtifacts":[…]}` 便于 grep）。
   **哈希只打印不钉死**——挂 R11 新候选 wasm 正是它的用途；v1 冻结包的哈希核验仍由契约冒烟负责。
4. **测试期隔离变量（新增，纯加法）**：`WORDCRAFT_DATA_DIR`、`WORDCRAFT_AUDIT_PATH` 覆盖 `data/` 与审计文件路径。
   默认值仍是"宿主自己的 `data/`"与 `<delivery>/logs/host-v2-audit.jsonl`，所以直接 `start.ps1` 的行为与文档一致。
5. **端口与日志**：默认端口 5311 → **5321**（避免与现役 v1 宿主撞端口）；初始 v2 曾在模块顶部拒绝 5255/7100/7101。当前 LingDoc 候选副本仅保留旧外来端口 5255 的拒绝，7100/7101 可作为普通预览端口，真实占用仍由 `listen()` 拒绝；
   审计日志默认改到 `logs/host-v2-audit.jsonl`（v1 的 `logs/host-audit.jsonl` 只属于 v1，不混写）。
6. `start.ps1`/`stop.ps1`：默认端口 5321、日志/PID 全部 `host-v2-*`；`start.ps1` 传 **绝对路径** 启动
   （便于按命令行认领进程），可选 `-WebDir`；`stop.ps1` 额外要求命令行里出现 `host-v2-20261024`，
   因此**不可能误杀 v1 宿主**。
7. 其余（fixture 身份钉死 `E6EF31B4…`、12 字体启动核验、静态白名单、保存的 fsync+rename+回读哈希、
   幂等重放返原回执、stale/同 token 身份冲突 409、审计字段）**逐字保留 v1 实现**。

---

## 4. `/api/version` 契约实测（B.1，36 项全过）

测试方式：宿主独占端口 5321，数据目录为 `data-seed/` 的隔离副本（**没有** `versions.json`，从零走反向重建），
`/api/save` 造第二版，全部请求/响应/审计摘要见 `evidence/01-version-read.json`。

| 结论 | 实测证据 |
|---|---|
| 启动即把既有 `saved-v1.docx` 索引进 `versions.json` | ready 行 `versions.created=true, rebuilt=[{diskVersion:1,from:"state.json"}]`；索引条目 sha256 = `c2bd4f5c…`、file=`saved-v1.docx`、字节 26132、saveToken 与 state.json 一致 |
| `{diskVersion:1}` → 200，字节与回执严格一致 | 返回 decoded 26132B，`sha256(report)=sha256(recomputed)=sha256(receipt)=c2bd4f5c34d4db223335369621eab7a1232ed4aee17ad81386340f9352a3e020`；`savedAt=2026-10-24T14:10:40.903Z` |
| 保存第二版后 `{2}` → 200 且与 v1 不同 | 第二版 = fixture 字节（`saveToken=version-read:B:1`，base 1 → diskVersion 2），`{2}` 返回 `sha256=e6ef31b4…`、字节等于 fixture，`{1}` 仍返回 `c2bd4f5c…` |
| `/api/open` 仍**只**返回最新版 | open 返回 `diskVersion=2`、`sha256=e6ef31b4…`，不等于 v1 |
| 未知版本 → 404 且不写盘 | `{99}` → `404 {"error":"unknown disk version"}`；前后目录清单、state.json sha、versions.json sha 三同；`{0}` 同样 404（"尚无快照"没有回执） |
| 非法参数 → 400 | 8 种形态（缺失/字符串/-1/1.5/null/true/数组/2^53+1）全部 `400 {"error":"invalid diskVersion"}`；`GET` → 405 |
| 调用不改指针、不建/不删文件 | 每个版本调用前后快照 `{listing, state.json sha256, versions.json sha256}` 完全相同；最终目录恰为 `saved-v1.docx, saved-v2.docx, state.json, versions.json`（无 `.tmp` 残留） |
| 字节被篡改 → 拒绝 | 改 `saved-v2.docx` 中间 1 字节后 `{2}` → `500 {"error":"version hash mismatch"}`，指针仍不动；还原后 `{2}` 重新 200 且 sha 复原 |
| 三重校验对新端点同样生效 | 无 token / 错 token / 异源 Origin / 异源 Host → 全 403 |
| 审计 | `version_ok=4`、`version_missing=2`、`version_invalid=8`、`version_hash_mismatch=1`、坏行 0；`version_missing` 行带 `diskVersion:99`；**审计全文不含 64hex token** |

---

## 5. 双会话冲突实测（B.2，24 项全过）

两个**并发** `POST /api/save`（`Promise.all`，同一 `expectedDiskVersion=1`，各自不同 `saveToken`/documentId/revision、
载荷是两份真实的 docx：`inputs/short.docx` 与 `inputs/long.docx`）：

| 结论 | 实测 |
|---|---|
| 状态码分布 | `[200, 409]`（恰一个赢）；本次赢家 = A（`short.docx`，sha `ea2acfca…`），输家 = B（`long.docx`，sha `b87bf1c2…`）；并发时刻 15:27:52.529 → 落定 15:27:52.605 |
| 失败方回执 | `409 {"error":"stale disk version"}`，无 receipt 字段 |
| 只多出一个版本文件 | 前 `saved-v1.docx, state.json, versions.json` → 后仅多 `saved-v2.docx`（无 `.tmp` 残留） |
| 失败方字节未被静默写入 | 扫描 `data/` 全部文件二进制：**没有任何文件包含输家字节**；`state.json`、`versions.json` 里也**不含输家 sha256** |
| 指针指向成功方 | `state.json` = v2 回执（`sha256=ea2acfca…`、`saveToken=dual-session:A:rev1`、`file=saved-v2.docx`） |
| 历史可读 | `{1}` = 原始 seed `c2bd4f5c…`；`{2}` = 赢家 `ea2acfca…`（与文件字节逐位相等） |
| 同 stale 重试仍被拒 | 输家用同一个 `expectedDiskVersion=1` 再发 → 仍 409，且前后目录/指针/索引三同（不写盘） |
| 带新 diskVersion 重试 → 成功 | `expectedDiskVersion=2` 重发 → `200, diskVersion=3, baseDiskVersion=2, file=saved-v3.docx, sha256=b87bf1c2…` |
| 不覆盖较新内容 | 恢复后 `saved-v2.docx` 仍逐位等于赢家字节（sha 未变）；`{2}` 仍 200 赢家、`{3}` 200 输家（各按自己回执） |
| 版本账完整 | 目录 = `saved-v1, saved-v2, saved-v3, state.json, versions.json`；`versions.json` 索引恰 `1,2,3` 无重复；`state.json` 指向 v3 |
| 审计 | `save_ok=2`（v2 赢家、v3 恢复）＋ `save_failed` 中 `result=409/error="stale disk version"` 共 2 条（并发输家 + 同 stale 重试）；坏行 0；无 token 泄漏 |

---

## 6. 回归（B.3）：原 17 项契约冒烟 + 原版工具复跑

跑法：v2 宿主跑在 5321，数据目录用宿主**自己的** `data/`（这样 copied 的原版工具按自身路径解析 `data/` 也能对上）。
冒烟本身只读（幂等重放 + stale 拒绝），所以跑完 `state.json` 与 `saved-v1.docx` 必须**一字节未变**——已断言。

17 项逐项结果（全过，明细 `evidence/03-contract-smoke-17.json`）：

1. fixture 身份（ready 行 `E6EF31B4…`，与 `inputs/` 实际哈希一致）
2. `wordcraft.js` 200 / 165503B
3. `wordcraft.js` sha = `5F24B85B…`
4. `wordcraft_bg.wasm` 200 / 50311304B
5. `wordcraft_bg.wasm` sha = `16B3B2A4…`
6. 6 条未声明路径全 403（`/nope` `/../server.mjs` `/data/state.json` `/data/saved-v1.docx` `/host-config.js.bak` `/site/index.html`）
7. 无 `X-Host-Token` → 403
8. 错 token → 403
9. 异源 `Origin` → 403
10. 异源 `Host` 头 → 403
11. `/api/open` 字节哈希 == 其自称
12. `/api/font` simsun.ttc 逐字节一致（18316748B / `1526ac24…`）
13. 未声明字体（`../../windows/win.ini`）→ 404
14. 幂等重放返回原回执（同 diskVersion/savedAt）
15. 幂等重放不新建版本文件
16. stale `expectedDiskVersion` → 409 且不写盘
17. 审计行均为合法 JSON 且带 event 名，且不含 token

额外两条：17 项跑完 `state.json` sha 仍等于 seed 值、目录仍为 `saved-v1.docx,state.json,versions.json`；
以及**原版工具复跑**：`tools/contract-smoke.mjs` 与 `candidate/host/tools/contract-smoke.mjs` **字节级同一**
（sha 已断言），不改一行，对 v2 宿主跑出 `allPassed=true`、16/16 通过（`evidence/03b-original-contract-tool.json`）。

> **顺带发现的 v1 缺陷（重要，未修，因为 candidate/host 冻结）**：v1 的 `tools/contract-smoke.mjs` 第 24 行写的是
> `process.env.WORCRAFT_HOST_PORT`（少一个 D），所以它**永远**只打 5311，v1 当时能过是因为宿主也在 5311。
> v2 复跑时保持该文件一字不动，改为直接喂它真正读取的那个变量名（`WORCRAFT_HOST_PORT=5321`），
> 因此"原版工具原样通过"这条结论仍然成立。建议 v1 交接文档标注此 typo。

---

## 7. 如何复跑

```powershell
# 全量（4 项顺序跑，各自起停宿主，退出码 0 = 全过；约 20 秒）
cd D:\Mon3tr\projects\lingdoc-dachuang\reports\wordcraft-delivery-v1-20261024\rebuild\host-v2-20261024
node tests\v2-run-all.mjs

# 单项（每项都自带起停与端口释放断言；WORDCRAFT_V2_STAMP 可指定 runs/ 子目录名）
node tests\v2-version-read.mjs      # -> evidence/01-version-read.json
node tests\v2-dual-session.mjs      # -> evidence/02-dual-session.json
node tests\v2-contract-smoke.mjs    # -> evidence/03-contract-smoke-17.json + 03b
node tests\v2-web-and-guards.mjs    # -> evidence/04-web-and-guards.json
```

- 每次运行会**覆盖**对应 `evidence/*.json`；`runs/<stamp>/` 是隔离数据副本，可整目录删除。
- 本轮全量脚本连跑两次（`20261024-152745`、`20261024-152934`）均 14 项编排断言全过，留存的证据是第二次；
  单次约 18–20 秒。
- 测试脚本不会碰 `data/`（除 17 项冒烟的只读检查外）——它们用 `WORDCRAFT_DATA_DIR` 指向 `runs/` 副本。
- 若上一次运行异常中断留下监听，先跑 `.\stop.ps1 -Port 5321`（它只认本 v2 树），或直接确认
  `Get-NetTCPConnection -State Listen -LocalPort 5321` 为空。

宿主本体（人用启动命令）：

```powershell
cd D:\Mon3tr\projects\lingdoc-dachuang\reports\wordcraft-delivery-v1-20261024\rebuild\host-v2-20261024
.\start.ps1 -Port 5321                      # 默认 web 目录 = <delivery>\candidate\web
.\start.ps1 -Port 5321 -WebDir D:\path\to\r11-candidate-web    # 挂新候选 wasm
.\stop.ps1 -Port 5321
# 或直接：
#   $env:WORDCRAFT_HOST_PORT=5321; $env:WORDCRAFT_WEB_DIR=...; node server.mjs
```

启动器实跑记录见 `logs/host-v2-launcher-check.log`：ready 行含 `web.dir/source/artifacts` 与
`versions`，`/api/version {1}` 返回 200（`c2bd4f5c…` / 26132B / savedAt 一致），
随后 `stop.ps1` 停掉 pid、`port 5321 released = True`、残留 host-v2 进程 0。

---

## 8. 纪律核对（未触碰的东西）

- **`candidate/host/`、`candidate/web/` 零写入**：两目录内所有文件 `LastWriteTime` ≤ 2026-10-24 22:19（本会话 23:18 之后无任何变更）；
  `candidate/host/data/{state.json,saved-v1.docx}` 哈希与 v2 的 `data-seed/` 完全一致（seed 是它们的副本）。
- **端口**：测试全程 5321；`5255/7100/7101` 三个外来实例在运行前/运行后**属主 pid 完全相同**
  （`5255=70460`、`7100=55528`、`7101=16956`），说明既没抢占也没误杀；结束后 5311/5321 均 `free`。
- **无残留进程**：`Get-CimInstance Win32_Process` 过滤 `host-v2-20261024` 的 node 进程数 = 0（run-all 断言 + 事后复核）。
- **写盘范围**：仅 `rebuild/host-v2-20261024/**` 与 `logs/host-v2-*`；未动 `repo/`、`prototypes/`，未执行任何 git 命令，未调用模型。

---

## 9. 限制与遗留（明确未跑/未证明）

1. **本轮的"两版"不是引擎导出**：B.1 的两版是"既有 v1 引擎快照 + fixture 原字节"，
   B.2 的两份载荷是 `inputs/short.docx` / `inputs/long.docx`。任务书允许这种构造；
   但"两次真实编辑→两次真实导出→历史版本读取"的**端到端**链路本轮**未跑**（需要浏览器+OS 键盘通道，见 v1 的 `evidence/host/`）。
2. **`/api/version` 尚无 UI 接线**：`site/host.js` 原样未改，页面不提供"打开旧版本"入口；
   本轮只证明 HTTP 契约。
3. **并发窗口是服务端串行化的结果**：v1/v2 都用 promise 链串行化 save，所以"并发"在服务端是确定性的；
   真两个浏览器 + 网络抖动下的同一语义本轮未做（语义上等价，但未实测多机时延）。
4. **索引落盘失败路径未做故障注入**：代码路径存在（500 + 审计 `version_index_failed` + 幂等重放可恢复），
   但没有真的制造磁盘写失败。
5. **没有回执的历史文件不做猜测**：若 `data/` 里存在"有文件无回执"的 `saved-vN.docx`，`/api/version` 返回 404。
   这是有意的安全取舍（宁缺不错），已在 §3.2 记录。
6. **web 产物哈希只打印不校验**：这正是 `WORDCRAFT_WEB_DIR` 的用途（换候选）；v1 冻结包的身份核验仍由
   17 项冒烟（第 2–5 项）承担。若后续要求"允许清单 + 钉死哈希"，属下一轮改动。
7. `/api/version` 把整份快照 base64 塞进 JSON（当前 26–36KB 快照无压力）；若未来文档到 MB 级需要改成流式/分块。
8. `tools/` 下的 `e2e-driver.mjs`、`input-editor-probe.mjs`、`sendinput-text.ps1` 是原样复制过来的 v1 浏览器通道脚本，
   **本轮未运行**（其内写死 5311/浏览器路径，未做 v2 适配）；保留只为血缘完整，不作为 v2 的可用工具。
9. 审计日志是 `appendFile` 异步写；测试断言"事件已出现"用了进程结束前的读取，故断言用"至少/恰好按本轮实际次数"，
   极端负载下可能出现行序交错（JSON 行本身仍逐行合法）。
