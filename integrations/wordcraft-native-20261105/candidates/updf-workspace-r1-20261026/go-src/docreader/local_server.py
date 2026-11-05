"""Local transport adapter; PDF/DOCX parsing is the pinned magician implementation."""
import base64
import json
import logging
import mimetypes
import multiprocessing
import os
import sys
import threading
from concurrent.futures import ThreadPoolExecutor
from pathlib import PurePosixPath

# Record the actual Linux PID before importing parser dependencies. The parent
# can still stop this process if a dependency import fails or stalls.
if __name__ == "__main__" and os.environ.get("LINGDOC_READER_PID_FILE"):
    with open(os.environ["LINGDOC_READER_PID_FILE"], "x", encoding="utf-8") as owner_file:
        json.dump({"pid": os.getpid(), "runId": os.environ.get("LINGDOC_READER_RUN_ID", "")}, owner_file)

import grpc
from docreader.parser.docx_parser import DocxParser
from docreader.parser.pdf_parser import PDFParser
from docreader.proto import docreader_pb2 as pb, docreader_pb2_grpc as rpc

MAX_BYTES = 8 << 20
UPSTREAM = "acb9ac744f5c6fbe68bc672ecf08eb03e7db1a22"


class LocalReader(rpc.DocReaderServicer):
    def _read(self, request, context):
        if not context.peer().startswith(("ipv4:127.0.0.1:", "ipv6:[::1]:")):
            context.abort(grpc.StatusCode.PERMISSION_DENIED, "Local reader only")
        if request.url or request.config.parser_engine not in ("", "builtin"):
            raise ValueError("本地解析入口只接收上传文件，不调用外部解析器。")
        kind = request.file_type.lower().lstrip(".")
        if kind not in ("pdf", "docx") or not 0 < len(request.file_content) <= MAX_BYTES:
            raise ValueError("请选择不超过 8 MiB 的 PDF 或 DOCX。")
        content = request.file_content
        if kind == "pdf" and not content.startswith(b"%PDF-"):
            raise ValueError("文件内容不是有效 PDF。")
        if kind == "docx" and not content.startswith(b"PK"):
            raise ValueError("此文件不是 DOCX，旧 DOC 或改扩展名文件请先转换。")
        parser_type = PDFParser if kind == "pdf" else DocxParser
        document = parser_type(file_name=PurePosixPath(request.file_name.replace("\\", "/")).name, file_type=kind).parse(content)
        if int(document.metadata.get("scanned_page_count", 0)) > 0 or document.metadata.get("image_source_type") == "scanned_pdf":
            raise ValueError("这份 PDF 含扫描页，当前本地链尚未启用 OCR。请使用带文字层的 PDF；未将图片当作可引用原文。")
        if not document.content.strip():
            raise ValueError("没有提取到可引用文字，请检查加密、损坏或扫描内容。")
        if len(document.content.encode("utf-8")) > MAX_BYTES:
            raise ValueError("解析文字超过本地证据定位上限，请拆分资料。")
        metadata = {key: str(value) for key, value in document.metadata.items()}
        metadata.update(parser_class=parser_type.__name__, parser_revision=UPSTREAM)
        return document, metadata

    def ReadStream(self, request, context):
        try:
            document, metadata = self._read(request, context)
            yield pb.ReadStreamResponse(meta=pb.ReadStreamMeta(
                markdown_content=document.content, metadata=metadata, image_count=len(document.images)))
            for ref, encoded in document.images.items():
                raw = base64.b64decode(encoded)
                if len(raw) > MAX_BYTES:
                    raise ValueError("单张图片超过本地处理上限。")
                yield pb.ReadStreamResponse(image=pb.ImageRef(
                    filename=PurePosixPath(ref).name, original_ref=ref,
                    mime_type=mimetypes.guess_type(ref)[0] or "application/octet-stream", image_data=raw))
        except Exception as exc:
            logging.exception("Document conversion failed")
            # Return an actual parser failure; the Go ingestion service retains it.
            yield pb.ReadStreamResponse(meta=pb.ReadStreamMeta(error=str(exc)))

    def ListEngines(self, request, context):
        return pb.ListEnginesResponse(engines=[pb.ParserEngineInfo(
            name="builtin", description="Pinned magician PDFParser/DocxParser, native text",
            file_types=["pdf", "docx"], available=True)])


def main():
    # The upstream DOCX parser uses multiprocessing.Manager. Forking the
    # already-threaded gRPC server can hang; spawn starts a clean interpreter.
    multiprocessing.set_start_method("spawn", force=True)
    logging.basicConfig(level=logging.WARNING, stream=sys.stderr)
    server = grpc.server(ThreadPoolExecutor(max_workers=2), options=[
        ("grpc.max_receive_message_length", MAX_BYTES + (1 << 20)),
        ("grpc.max_send_message_length", MAX_BYTES + (1 << 20)),
    ])
    rpc.add_DocReaderServicer_to_server(LocalReader(), server)
    port = server.add_insecure_port("127.0.0.1:0")
    if not port:
        raise RuntimeError("Could not bind local document reader")
    server.start()
    print(json.dumps({"ready": True, "pid": os.getpid(), "port": port, "upstream": UPSTREAM,
                      "runId": os.environ.get("LINGDOC_READER_RUN_ID", "")}), flush=True)
    stopped = threading.Event()
    def control():
        # The owning Node process holds this pipe. EOF also closes the service.
        sys.stdin.readline()
        stopped.set()
    threading.Thread(target=control, daemon=True).start()
    try:
        stopped.wait()
    except KeyboardInterrupt:
        pass
    finally:
        server.stop(2).wait(3)


if __name__ == "__main__":
    main()
