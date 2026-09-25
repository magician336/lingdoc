# T06：固定契约 Mock 入口与请求层消费

## 领取与交付边界

| 项目 | 内容 |
|---|---|
| 任务 / 领取 | T06，Issue #11 |
| 实现者 | ChatGPT，通过 Lees-42 账号提交，受 gong 授权 |
| 消费者 | T07–T14 的前端/API adapter 实现者 |
| 独立评审 | 请求 magician336；作者账号不自行批准或合并 |
| 基线 | `9b7cf049120c19098b77172074cada15fa8c6965` |
| 交付 | 共享 OpenAPI 固定响应服务器、现有 request.ts 的开发消费页、反例与 HTTP/浏览器回归 |
| 状态 | 实现待独立评审；现场 CI 证据附 PR，不以本文静态文字替代运行结果 |

这是 T06 的最小入口，不是第二套后端或通用 Mock 平台。PR #6 提供的审查门禁继续保留。
字段与样例仍只来自 `contracts/openapi.json`，不在此新增业务 DTO、修改错误码或重编号场景。
T02 固定 reader 是否接入真实项目，与本入口读取契约中的固定模板样例是两件事。

## 启动

从仓库根执行（Python 3.10+；CI 使用 3.12）：

```sh
python -m pip install -r scripts/ci/requirements-lingdoc.txt
python scripts/lingdoc_mock/server.py --check
python scripts/lingdoc_mock/server.py --list
python scripts/lingdoc_mock/server.py
```

默认只监听 `127.0.0.1:4010`，不提供监听公网的选项。不需要账户、数据库或模型密钥。
`--check` 验证每个 inline JSON 响应样例；它不代替已有 `validate_artifacts.py` 的参考流程与静态不变量检查。
服务器启动时先检查所有样例，每次响应前再检查所选样例。破坏必填字段或类型会失败，不能发出假成功。

另开终端：

```sh
cd frontend
npm ci
npm run dev -- --host 127.0.0.1 --strictPort
```

打开 `http://127.0.0.1:5173/lingdoc-mock.html`，分别点击 **200 成功** 与 **422 失败**。
页面明确标 MOCK，使用现有 `src/utils/request.ts` 的 `get()`，显示 loading、HTTP 状态、错误与原始契约载荷。
入口不加入产品路由/菜单，Vite 当前正式构建仅含 main/embed，不包含这个开发 HTML。

消费路径：

```text
开发页面 → 现有 request.ts / Axios 拦截器
        → 显式 loopback adapter（过滤认证凭据）
        → 本地 HTTP 服务 → 共享 OpenAPI 样例与 schema
```

没有替换全局 API 地址，没有修改现有请求拦截器。开发 adapter 只演示 200/422；**不通过它模拟 401**，避免触发真实的刷新登录流程。
即使浏览器已有登录，Authorization、Cookie、X-Tenant-ID 也不转发；fetch 禁止重定向、携带 Cookie 或缓存，连接失败不回退真实接口。
这验证的是请求封装兼容性，不是用户认证实现。

## 选择其他领域样例

服务器按方法与路径匹配 OpenAPI。用下面两个开发头选择已声明的响应，不创建新的全局会话状态：

```sh
curl -i http://127.0.0.1:4010/api/v1/lingdoc/templates/template-demo \
  -H 'X-LingDoc-Mock-Status: 422' \
  -H 'X-LingDoc-Mock-Example: invalid_state'
```

不指定时使用首个已声明 2xx，优先 `success` 样例。具体可选项看 `--list`，不要猜名字。
写操作仍要求契约中的 JSON 字段、路径参数及 Idempotency-Key，但**不会写数据库或实现幂等状态机**。
二进制下载没有可复用 JSON 样例时明确拒绝；不返回假 DOCX。未知路径/样例也不转发到其他服务。

默认允许 `http://127.0.0.1:5173` 与 `http://localhost:5173` 的 CORS；其他开发端口用 `--origin` 显式增加，仅接受 loopback HTTP origin。
不接收真实凭据，不模拟授权，不开放任意 Origin。请求体上限 1 MiB，连接读取超时 10 秒；这是本地调试工具，不是生产 HTTP 服务。

## 验证与负例

```sh
python -m unittest discover -s scripts/lingdoc_mock -p 'test_*.py' -v
# frontend 目录中，固定响应服务器已经启动：
LINGDOC_MOCK_ORIGIN=http://127.0.0.1:4010 npx tsx --test src/dev/lingdoc-mock/*.test.*
# 两个服务都启动后，仓库根执行（Node 22+，Chrome/Chromium）：
node scripts/lingdoc_mock/browser_smoke.mjs
```

PowerShell 用 `$env:LINGDOC_MOCK_ORIGIN='http://127.0.0.1:4010'` 设置变量后再执行 `npx`。
浏览器脚本默认 `google-chrome`，可通过 `CHROME_BIN` 指向本机 Chrome/Chromium。

| 检查 | 证明什么 | 不证明什么 |
|---|---|---|
| 引擎单测与真实 loopback HTTP | 严格请求/响应检查；破坏样例会拒绝；无凭据/路径/跨域旁路 | 正式认证、真实数据库与模型 |
| 仓库契约回归 | 当前共享样例直接加载；删除真实模板响应的 data 即失败 | OpenAPI 元规范全覆盖、业务状态一致性 |
| 实际 request.ts + 真实 Axios | 成功保留 envelope/$httpStatus，422 保留错误码/request_id | 真实登录恢复；相邻 i18n/存储/认证模块在 Node 测试中隔离 |
| 真实浏览器点击 | 开发页消费成功/失败；仅两次本地 API 请求、不泄露已有登录凭据 | 正式工作台交互与 F01 |

`request.integration.test.mjs` 不抄写拦截器：读取当前真实 request.ts，转换后使用真实 Axios；仅隔离其相邻 UI/认证依赖。
没有设置本地服务地址时，真实 HTTP 集成测试明确 SKIP，不标 PASS。专属 CI 启动服务并设置地址，该项必须实际运行。

专属工作流 `.github/workflows/lingdoc-mock.yml` 自动跑以上检查、前端类型检查/构建并保存服务日志。
它有路径过滤，是补充检查；不要将它单独设置为所有 PR 的 required check。现有稳定 `LingDoc PR gate` 保留。

## 明确限制与接真责任

固定响应没有连续状态。保存后读回仍是固定样例；路径中的任意 ID 不会重写响应对象 ID。用 `p-demo` 等契约样例身份演示，不能宣称已实现真实项目操作。
F01 连续流程、并发/幂等/权限副作用由真实提供方和测试数据库验证，不从静态 Mock 通过推导。
各领域继续维护本域 OpenAPI 与测试；T06 不接管全部接口或 T15/T16。接真时显式换回真实 adapter，不做“真实失败就回退 Mock”。

回退：移除此 PR 新增的开发入口、脚本、测试与专属工作流即可。没有修改运行时接口、数据库、现有请求层或产品路由。
