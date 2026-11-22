# 当前原生操作说明

本页只描述当前 Rust／egui 桌面主链。旧 `start-unified.ps1`、7241 浏览器服务、WSL Go 和 docreader Python 属于历史运行体系，不是当前默认启动步骤。

## 任务与施工隔离

按 gong 明确交办记录任务范围、基线和验收目标。独立任务使用独立分支或工作树；集成前检查差异、冲突与证据。共享任务台账不再是认领或施工前置，历史工具仅在明确需要时查询。

开始修改前必须检查 `git status`：工作区可能保留上一阶段尚未提交的原生保存、AI 或绘图修复，不得回退、覆盖或混入无关提交。

## 构建

在 `ENGINE` 目录执行；`ENGINE` 的含义见 [开发路由](DEVELOPMENT.md)。Windows 本机默认使用项目便携 Rust（`x86_64-pc-windows-gnu`）与完整 `tools\winlibs-manual\mingw64\bin` 工具链（不要混用缺少 DLL 的 `.tooling\mingw-binutils`），并将构建产物与临时目录统一指向 D 盘项目目录：

```powershell
$tooling = 'D:\Mon3tr\projects\lingdoc-dachuang\.tooling'
$env:CARGO_HOME = "$tooling\cargo"
$env:RUSTUP_HOME = "$tooling\rustup"
$env:TEMP = 'D:\Mon3tr\projects\lingdoc-dachuang\.tmp'
$env:TMP = 'D:\Mon3tr\projects\lingdoc-dachuang\.tmp'
$env:PATH = "$tooling\cargo\bin;D:\Mon3tr\projects\lingdoc-dachuang\tools\winlibs-manual\mingw64\bin;$env:PATH"
# 全仓唯一构筑目录：所有任务、worktree 共用 .tooling\target，不再按任务命名。
# 构筑可无损重建；新任务开工时若其中是旧任务残留且未被使用，先删除再构建。
# 例外仅限真正的并发编译（两个进程同时构建同一工作区），临时目录用完即删。
$env:CARGO_TARGET_DIR = "$tooling\target"
cargo build -p wordcraft
```

对应调试程序为：

```text
D:\Mon3tr\projects\lingdoc-dachuang\.tooling\target\debug\wordcraft.exe
```

硬规则：`.tooling` 下只保留这一个 target 目录。任何任务（含并行子代理、隔离审查）不得另建 `target-*` 目录；确需临时目录的，任务结束当场删除。历史遗留的多目录累积曾占满 D 盘（2026-11-20，104.7 GB / 五份构筑）。

不要把旧构建、旧 WASM 或其他候选的可执行文件当成本次源码结果。工具链缺失时如实报告，不把静态检查写成构建通过。

## AI 绘图工作台运行时

AI 绘图依赖三个机器级共享位置，均放在 `.tooling` 下，全 worktree 共用：

- **便携 Node**：`.tooling\node\node.exe`（v22.19.0）。应用启动绘图子进程时优先使用，不依赖系统 PATH 上的 Node。
- **开发源码与构建缓存**：`.tooling\drawing-runtime\source`。允许独立运行 `npm run dev`、`npm run build` 或删除 `.next` 调试；这里的构建结果不再直接作为稳定演示运行时。
- **WordCraft 实际使用的运行包**：`.tooling\drawing-runtime\app`。只有 `prepareDrawingUpstream({directory, promote:true})` 才会发布到这里；发布会先验证 `server.js`、`package.json` 和 `.next/required-server-files.json`，再替换上一版。构建或校验失败时，已有 `app` 保持不变。

完整修复命令为 `node desktop/drawing-upstream/prepare.mjs --build --promote`；日常开发只用 `--build`，不必发布。需要 Node 22+ 与 Git、网络；便携副本旁没有 npm 时，用带 npm 的 Node 发行版驱动 `prepare.mjs`。

应用查找顺序为显式 `LINGDOC_DRAWING_UPSTREAM` → 共享 `app` → 共享 `source` → 仓库内 `desktop/drawing-upstream/runtime/app` → `runtime/source`。开发调试可显式指向 `source`，或用 `LINGDOC_DRAWING_ORIGIN` 连接独立调试服务。覆盖 Node 路径使用 `LINGDOC_DRAWING_NODE`。

## 打开隔离测试项目

先创建不含真实用户文稿的测试目录，并按现行契约只放一份主 DOCX：

```powershell
& 'D:\Mon3tr\projects\lingdoc-dachuang\.tooling\target\debug\wordcraft.exe' --project 'D:\path\to\isolated-project'
```

项目会持有 `.lingdoc.lock`；第二实例应被拒绝。测试结束通过应用退出并确认本次进程退出，不擅自杀掉用户正在使用的实例。

## 验证边界

- 按改动范围运行对应 Rust 测试；例如绘图桥可运行 `cargo test -p wordcraft drawing_bridge`。
- 涉及保存、锁、Recovery、外部修改或跨模块写入时，必须验证真实接口路径，并在隔离目录注入相应场景。
- 涉及 AI 时区分 Mock、协议测试和真实模型调用；真实调用可能计费，需有明确任务依据。资料问答须验证默认携带当前主 DOCX（桌面端优先使用编辑器当前内存正文快照，含未保存修改）与全部可读取资料、后台线程构建上下文（失败时回滚未发出的用户提问且不调用模型）、长项目确定性节选（含英文大小写无关匹配与跨文档配额兜底）、多会话原子落盘与跨启动恢复、当前可见历史和项目切换隔离；打开／导入不得为 AI 预抽全文。资料与历史预算分开，实际进入请求的来源不等于精确引用。选区 AI 只处理冻结选文，当前文档仅作只读上下文；改写附加项目资料仍需显式勾选。
- 正常桌面使用 Windows 凭据管理器中的用户级 DeepSeek 凭据；环境变量只作为隔离测试的显式覆盖。不得把密钥写入文档、源码、证据或日志。
- AI 绘图仅在用户打开工作台时启动其外部服务；生成、插图、DOCX 保存重开和原图编辑分别验收。
- 历史证据保留原范围，不能覆盖，也不能据此宣称当前整体通过。

构建成功、单元测试通过、应用真实操作和完整产品验收是不同层次，交付时分别列明未运行项。
