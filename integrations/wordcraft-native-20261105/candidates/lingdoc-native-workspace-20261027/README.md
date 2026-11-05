# LingDoc 原生工作台候选

基线日期：2026-10-27。S0 原版复建、S1 原生浅色外壳与 S2 六章真实面板均已完成限定范围验收；S3 统一保存和后续发布验收尚未实施。

## 范围

- `engine/`：WordCraft 源码的独立施工副本。S0 保持冻结源码；S1 修改原生外壳；S2 在 `ui-egui` 与 Web 入口复用现有 manifest/focus 适配器。
- `ds/`：复用 R11 固定宿主和固定真实 DOCX，仅用于隔离验收。
- `build/`、`runtime/`、`artifacts/`：本地构建、运行和原始验收产物，不进入版本控制。
- `../../evidence/native-lingdoc-ui-20261027/s0/`：S0 冻结基线证据。
- `../../evidence/native-lingdoc-ui-20261027/s1/`：S1 构建、四尺寸截图、真实命令与撤销证据。
- `../../evidence/native-lingdoc-ui-20261027/s2/`：S2 构建、六章真实定位、四尺寸与撤销证据。

## 第一版环境与启动

以下命令均在本候选根目录执行，不需要额外 npm 依赖。首次接手或工具路径变化时检查一次：

```powershell
npm run env:check
```

检查现有 Node、Git、Rust/WASM/MSVC、WSL/Go、浏览器、固定字体、DOCX、编辑器产物和数据目录。独立检查并行执行，外部命令限时 25 秒；显示缺项及恢复办法，不安装、不下载、不编译产品、不启动服务。`-- --json` 可输出详细路径和版本；`-- --runtime` 只查启动所需部分。环境通过不代表产品验收通过，也不证明全部 Go 依赖已缓存。

启动真实 DS＋Go 工作台：

```powershell
npm start
```

启动器先查运行依赖，再启动现有 Go 隔离服务与 DS，显示 `LingDoc 试用：http://127.0.0.1:<端口>/`。默认自动选择空闲端口，也可使用 `npm start -- --port 7102`。终端用 Ctrl+C 停止本次进程，数据库和文件保留。现有 `npm run dev` 仍是仅 DS 的编辑器预览，不提供 Go 版本登记和交付服务。

| 用途 | 固定位置或要求 | 缺项时处理 |
|---|---|---|
| Node / Git | 本机 PATH；Node 22+ | 先定位现有程序，不重复安装依赖 |
| Rust / MSVC | `build/rust/`；Rust 1.95.0、wasm32、现有 VS2022 C++ 工具 | 按下文 `use-rust.ps1 -Repair` 从已有组件补齐 |
| Go 服务 | WSL `LingDoc-Beta-R29-Ubuntu2404`，`/root/lingdoc-r29/toolchains/go/`；版本与旧候选 `go.mod` 一致，当前 1.26.0 | 优先复用现有 WSL、Go、gcc；路径集中在 `scripts/local-environment.mjs` |
| Go 缓存 | WSL `/root/lingdoc-r29/cache/go-mod`、`go-build` | 保留缓存；日常运行 `GOPROXY=off`，不依赖临时联网下载 |
| 浏览器 / 字体 | 验收脚本使用 x86 安装路径的 Edge；字体由 DS 的 `host/font-manifest.json` 锁定 | 先核对本机文件；不修改字体清单绕过失败，不把系统字体打包进 Git |
| 编辑器 / 模板 | `build/out/wordcraft.js`、`wordcraft_bg.wasm`；`ds/inputs/nssfc2026-official-template.docx` | 复用当前产物；只有源码需要重建时再编译；固定 DOCX 身份须一致 |
| 用户运行数据 | `runtime/first-version/lingdoc.db`、`actual-origin.txt`、`ds/` | 首次启动初始化，重开复用；这几项一起保留，不按缓存清除 |
| 本次进程与日志 | `runtime/first-version/runtime.lock`、`logs/<运行ID>/` | 锁存在先确认原实例，不能删锁后强行复用同一数据目录 |

换电脑时先用清单检查实际缺项。Go/WSL 路径不同，可设置 `LINGDOC_WSL_DISTRO`、`LINGDOC_GO_HOME`、`LINGDOC_GO_MOD_CACHE`、`LINGDOC_GO_BUILD_CACHE`；检查器和启动器读取同一组值。没有本地依赖时再单独准备缺失组件，本入口不会悄悄下载或升级。当前启动方案仍依赖 Windows 字体与既有 WSL，不是可拷贝即运行的跨平台安装包。

Agent 执行环境还需允许本机回环通信、WSL 和清理本次子进程。若出现 `connect EACCES 127.0.0.1` 或测试进程无法退出，先保留首错、核对执行权限与进程归属；不要因此重装工具或修改产品。2026-10-31 的 `verify:dev` 首轮记录了这类沙箱限制，后续对照结果另存，不能覆盖首轮失败。

环境验证使用独立数据目录，示例：`npm start -- --runtime-dir runtime/environment-trial`。正常试用保留默认目录即可。不要用测试目录替换用户数据，也不要把真实后端的启动通过当成保存、确认、冻结、导出的完整 UI 验收。验证记录在 `../../evidence/native-build-environment-20261031/unified/`。

## 复核与复建

冻结清单位于 `fixtures/source-baseline.sha256`，SHA-256 为 `71a5c3fcb86c93eb40f7812e686259820956eb51d76d7a7fd0a96550a360ec0a`。638/638 匹配只是 S0 历史基线；S1/S2 已合法修改原生外壳及适配代码，不要再要求当前施工副本通过该冻结检查。

```bash
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "candidates/lingdoc-native-workspace-20261027/build-s0.ps1" -RunName <new-run-name> -Jobs 2
```

构建使用候选内 `build/rust` 的 Rust 1.95.0、wasm32 target、项目内 wasm-bindgen 0.2.129，以及候选内独立 Cargo 缓存和 target。`build-s0.ps1` 关闭 LTO、设置 codegen-units=16、离线构建，并保存锁文件前后副本。

### 本地 Rust 环境与恢复（2026-10-31）

正式入口是受版本控制的 `build-s0.ps1` 和 `scripts/use-rust.ps1`，不依赖 C 盘 Temp、全局 rustup 或 `build/` 内的临时脚本。从候选根目录执行以下命令，先列出实际工具路径、版本和两个标准库是否齐全，不编译产品：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./build-s0.ps1 -CheckEnvironment
```

文件统一存放如下，均为候选相对路径：

| 路径 | 用途 |
|---|---|
| `build/rust/` | 实际使用的工具链，含 `bin/` 与 host、WASM 标准库；长期保留 |
| `build/toolchain/` | 已解压的组件；`cargo/bin/`、`rustc/bin/` 是原始分包结构，并不是已安装的根目录 |
| `build/*.tar.xz` | 已下载的离线恢复包，保留以免重复下载 |
| `build/cargo-home/`、`build/target/` | 依赖与编译缓存；修复工具路径时不清理 |
| `build/tmp/` | 本地临时目录，不能当作工具链安装目录 |

缺文件时，先复用已经解压的本地组件。该操作只复制缺失文件，不覆盖现有工具、不联网、不清缓存；有 Rust 进程运行时会拒绝修复：

```powershell
. ./scripts/use-rust.ps1 -Repair
```

普通 Rust 命令也先加载同一个入口，再进入 `engine/`；这会在当前 PowerShell 设置 Rust、MSVC 与候选缓存路径。例如 `cargo fmt --version` 只核对格式工具，不改源码：

```powershell
. ./scripts/use-rust.ps1
Push-Location ./engine
cargo fmt --version
Pop-Location
```

`rustfmt`、`clippy` 缺失会列入 `optionalMissing`，不阻断只需编译的任务；需要它们时从已有组件补齐，不把工具缺失误记为产品行为失败。若本地恢复包也没有所缺组件，再补对应 **1.95.0 / Windows MSVC / wasm32** 分包；不要先下载整套或清 `target`。切换 Rust 版本、平台或源码依赖版本时，应重新核对版本匹配，不能把本次路径约定套到另一工具链。

本次另发现：工具文件存在、`PATH` 正确，也不保证 `cargo fmt` 能用。Cargo 会优先从 `CARGO_HOME/bin` 找子命令，此处遗留的 rustup 代理会报“未选择工具链”。入口因此显式设置 `RUSTUP_TOOLCHAIN` 为本地 `build/rust` 绝对路径，保留原缓存和代理，不设置全局默认、不联网安装。依据：[Cargo 子命令搜索顺序](https://doc.rust-lang.org/cargo/reference/external-tools.html)、[rustup 的本地绝对路径支持](https://rust-lang.github.io/rustup/environment-variables.html)。

本次故障是旧构建入口硬编码了失效的 `C:/Users/gongqc/AppData/Local/Temp/wc-r5-rust-195-01a0bd94`。2026-10-31 的 `build/logs/first-version-integration-r2-result.json` 已证明 D 盘 `build/rust` 构建和 bindgen 返回 0；临时修正版成功并未自动修复正式入口，因此本次将路径与缺件恢复合并到上述入口。没有证据表明是谁移除了旧目录中的文件。环境修复的验证见 [记录](../../evidence/native-build-environment-20261031/verification.json)，不等同完整 CI 或产品验收通过。

## S0 基线验收

```bash
node --check candidates/lingdoc-native-workspace-20261027/scripts/verify-s0.mjs
node candidates/lingdoc-native-workspace-20261027/scripts/verify-s0.mjs
```

S0 同源码复建及真实 DOCX 打开通过；JS 哈希与固定交付一致，WASM 哈希不同，不能宣称逐字节复现。详见 S0 证据目录的 `README.md` 和 `verification.json`。

## S1 原生界面与真实命令验收

```bash
node --check candidates/lingdoc-native-workspace-20261027/scripts/verify-s1.mjs
node candidates/lingdoc-native-workspace-20261027/scripts/verify-s1.mjs
```

脚本使用动态空闲端口，启动隔离宿主和 Edge，加载新构建的 WASM，打开 `ds/inputs/nssfc2026-official-template.docx`，检查引擎身份、Canvas 和页面错误，保存截图，并确认宿主与浏览器退出。

通过范围：

- 1440×900、1366×768、1280×800、1920×1080，DPR=1 的真实 DOCX 打开与逐张视觉检查。
- 原生浅色、LingDoc 单标题、真实 Ribbon、LingDoc 页签、章节导航和功能区收展。
- 加粗、段落对齐、样式命令的真实修订、结构读回及撤销恢复。
- `pageErrors=[]`，宿主与浏览器均退出。

S1 WASM SHA-256 为 `d96bb3e7b55b2f580c8f31b85944b181ea27db4c8dbc3e48c19de2ae981a53f8`。完整环境、产物、截图和逐命令状态见 S1 证据目录。

## S2 六章真实面板与定位验收

```bash
node --check candidates/lingdoc-native-workspace-20261027/scripts/verify-s2.mjs
node candidates/lingdoc-native-workspace-20261027/scripts/verify-s2.mjs
```

通过范围：

- 原生 egui 侧栏从当前真实 DOCX 的 manifest 投影六章，不复制正文，不使用 HTML 覆盖层。
- 六章均通过真实鼠标点击定位到 `bodyRange.anchor`，文档身份、revision、dirty 和 manifestHash 不变。
- 真实插入后切章，一次 Ctrl+Z 撤销插入，六章正文哈希全部恢复。
- 四种窗口尺寸与六张定位截图均已逐张查看，宿主与浏览器均退出。

S2 WASM SHA-256 为 `9299d25fa2249a0e856ffb2552d9452ebd749ee586a71d5f53275cb2f37ba633`。章节选中只表示点击定位，不随正文滚动自动切换。完整环境、产物、截图和逐章状态见 S2 证据目录。

## 本地预览

候选根目录已提供 `npm run dev`，无需安装额外 npm 依赖。默认使用回环地址和 7102 端口，也可转发预览系统参数：

```bash
npm run dev -- --host 127.0.0.1 --port 7102
```

启动器复用 `build/out` 的真实 WASM 与现有固定宿主，数据隔离在 `runtime/preview/data`，审计写入 `artifacts/preview`。已用动态空闲端口验证 HTTP 200、真实工作空间 HTML、正确 WASM/数据目录及进程退出；测试结束后未留下预览服务。结果见 `../../evidence/native-lingdoc-ui-20261027/s2/preview-verification.json`。

候选宿主已移除对 7100、7101 的过时硬编码拒绝；两个端口均已真实启动、返回 HTTP 200 并确认关闭。若端口确有其他进程占用，仍由系统 `EADDRINUSE` 拒绝重复监听，且不会终止原进程。证据见 `../../evidence/native-lingdoc-ui-20261027/s2/port-verification.json`。

启动器兼容 `127.0.0.1`、`localhost`、`0.0.0.0`、`::`、`[::]` 等预览参数，通配 host 仅作为输入别名，实际服务仍只监听回环地址。修改启动器、端口或预览入口后必须运行：

```bash
npm run verify:dev
```

该命令回归默认启动、7100/7101、常见 host 与参数形式、真实占用保护、真实页面响应和进程清理。当前全部通过，证据见 `../../evidence/native-lingdoc-ui-20261027/s2/startup-regression.json`；共同约定已写入 `docs/DEVELOPMENT.md` 的“原生候选预览启动合同”。

## 未完成与门禁

AI 六章生成、评审、保护、预览一键交付、核查容器、统一保存、保存中继续编辑、IME、严格 Word 排版保真和现有预览切换不在 S0–S2 通过范围内。S3 前必须重新核对 `native-save-recovery` 归属。

`cargo xtask ci` 的 S0 历史运行因缺少可用 `cargo-fmt` 在格式检查停止，后续检查跳过，详见 `../../evidence/native-lingdoc-ui-20261027/s0/cargo-xtask-ci.txt`。2026-10-31 已补齐本地工具并修复代理指向，本次只验证环境与小程序，未重跑完整 CI；不得将工具恢复、成功构建或 S1/S2 专项验收写成完整 CI 通过。
