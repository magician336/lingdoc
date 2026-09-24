# T05：固定两章 DOCX 导出验证

状态：本地实现与自动校验通过；gong 回报人工副本编辑保存验收通过。基线为 `origin/main` 的 `9b7cf049`，工作分支 `codex/t05-docx-poc`。

## 交付范围

- `internal/lingdoc/docx`：仅使用 Go 标准库，把两章、正文、引用清单、待核事项及处置理由写为可编辑的 WordprocessingML/DOCX。只接受 `internal_demo`；内容含已识别的不支持 Markdown、缺失引用、重复待核项或不完整处置时拒绝生成，不会悄悄丢内容。
- `cmd/lingdoc-docx-demo`：读取仓库已有的 `frozen-input.canonical.json` 合成样例，映射为渲染输入并输出真实文件。仅做 T05 固定输入 PoC，不声称执行 T13 的冻结/权限/当前性检查。
- 文件留在仓外正式施工现场：`D:/Mon3tr/projects/lingdoc-dachuang/acceptance/T05-demo.docx`。内容是合成资料，没有真实个人数据。代码没有提交或推送。

## 复现

在此工作树根目录运行：

```powershell
$env:GOMODCACHE='D:\Mon3tr\projects\lingdoc-dachuang\caches\go-mod'
$env:GOCACHE='D:\Mon3tr\projects\lingdoc-dachuang\caches\go-build'
$env:GOTOOLCHAIN='local'
& 'D:\Mon3tr\projects\lingdoc-dachuang\tools\go\bin\go.exe' test ./internal/lingdoc/docx -count=1
& 'D:\Mon3tr\projects\lingdoc-dachuang\tools\go\bin\go.exe' vet ./internal/lingdoc/docx ./cmd/lingdoc-docx-demo
& 'D:\Mon3tr\projects\lingdoc-dachuang\tools\go\bin\go.exe' run ./cmd/lingdoc-docx-demo -output 'D:\Mon3tr\projects\lingdoc-dachuang\acceptance\T05-demo.docx'
```

2026-09-24 本机结果：包测试通过，`go vet` 通过；生成文件 2,189 字节，SHA-256 `F94593B1B754411FB87A71B72098ACE4EB39103EB1EE2B9693AD605BCAC1D56A`。`python-docx 1.1.2` 独立读出 10 段，确认两章顺序、`100条`、`[1]` 引用、待核附录及“保留警示”，再修改标题、保存为 `acceptance/T05-demo-roundtrip.docx` 并重新打开成功。该往返只验证程序层可编辑，不替代 Word/WPS 人工打开。

## 人工验收与边界

2026-09-24，gong 回报已创建 `acceptance/T05-人工验收.docx` 副本，并在文档末尾增加编辑内容，人工验收通过。独立复核该副本可被 `python-docx` 重新打开：原样例 10 段，人工副本 11 段，且仍含两章、引用资料和待核附录。具体使用的软件版本及界面提示未记录，故这里只把 Word/WPS 人工结果归于 gong 的回报，不将程序复核冒称为界面操作。

仅支持标题、纯文本普通段落和 `[[source:id]]` 引用标记。列表、表格、图片、链接、强调/删除线、行内/块级代码、HTML、行内/块级公式、Markdown 硬换行及精细分页均不支持；渲染器在导出前显式拒绝这些已识别语法，避免把未渲染 Markdown 原样写入 DOCX。接入界面前需明确提示。T14 仍需把本渲染器接到经过 T13 检查的冻结输入，并补齐授权、任务状态、文件持久化、下载和当前/历史标记；本 PoC 不提供这些能力。
