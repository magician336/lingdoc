# 本地 PDF / DOCX 接入

复用 magician336/lingdoc 提交 `acb9ac744f5c6fbe68bc672ecf08eb03e7db1a22` 的 `PDFParser`、`DocxParser` 及所需依赖模块。`UPSTREAM.json` 记录逐文件 Git blob；这些解析模块保持原样。没有引入新的 PDF/DOCX 解析器。

`local_server.py` 仅将两个已有解析器接到仓库现有 gRPC `ReadStream` 协议，并限制本地文件、大小和扫描件。刻意不导入上游聚合 `parser/__init__.py`，避免为两种文件安装所有其他格式和在线服务依赖。

运行环境为 `LingDoc-Beta-R29-Ubuntu2404` 中的 Python 3.12，项目隔离环境位于相邻原生候选的 `build/docreader-venv`。一次准备：

```powershell
$linuxCandidate = '/mnt/d/Mon3tr/projects/lingdoc-dachuang/worktrees/native-ui-s0-kimi/candidates'
wsl.exe -d LingDoc-Beta-R29-Ubuntu2404 -- python3 -m venv "$linuxCandidate/lingdoc-native-workspace-20261027/build/docreader-venv"
wsl.exe -d LingDoc-Beta-R29-Ubuntu2404 -- "$linuxCandidate/lingdoc-native-workspace-20261027/build/docreader-venv/bin/python" -m pip install --only-binary=:all: -r "$linuxCandidate/updf-workspace-r1-20261026/go-src/docreader/requirements-local.txt"
```

研究启动器自动持有解析器的控制管道并负责退出；普通启动不安装依赖。可用 `LINGDOC_DOCREADER_PYTHON` 指定已经准备好的 WSL Python 绝对路径。解析服务与 Go 同在 WSL，仅绑定 loopback；不借用 Windows localhost，也不开放局域网端口。

本地入口将 Python 子进程启动方式设为 `spawn`。这是对上游 DOCX 解析器所用 `multiprocessing.Manager` 的运行配置，避免在 gRPC 多线程服务中直接 `fork` 挂起；解析器源码未改。

本地支持不超过 8 MiB 的带文字层 PDF 和 DOCX 正文/表格。扫描或混合扫描 PDF 明确失败，OCR 未启用。引用锚点绑定同一次解析的文字及原文件哈希；当前不提供 PDF 页面坐标、DOCX 页码或自动生成参考文献。原文件仍由已有知识库下载接口读回。
