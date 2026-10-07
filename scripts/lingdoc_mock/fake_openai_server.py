#!/usr/bin/env python3
"""Local OpenAI-compatible fake model used by the G6 runtime matrix."""
from __future__ import annotations

import argparse
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import re
import time
from typing import Any

SAFE_ID = re.compile(r"^[A-Za-z0-9_.:-]{1,128}$")


def _request_json(payload: dict[str, Any]) -> dict[str, Any]:
    for message in reversed(payload.get("messages", [])):
        if not isinstance(message, dict) or message.get("role") != "user":
            continue
        content = message.get("content")
        if not isinstance(content, str):
            continue
        try:
            content = content.split("\nUse this JSON schema:", 1)[0]
            value = json.loads(content)
        except (TypeError, json.JSONDecodeError):
            continue
        if isinstance(value, dict):
            return value
    return {}


def _source_id(request: dict[str, Any]) -> str:
    sources = request.get("sources")
    if isinstance(sources, list):
        for source in sources:
            if isinstance(source, dict) and SAFE_ID.fullmatch(str(source.get("id", ""))):
                return str(source["id"])
    return "source.unknown"


def _draft(request: dict[str, Any]) -> dict[str, Any]:
    source_id = _source_id(request)
    if "问题" in str(request.get("section_title", "")):
        body = (
            "研究问题：在合成调查样本中，带有来源标记的资料是否能够支持一份可复核的研究问题草稿？ "
            f"[[source:{source_id}]] "
            "研究假设：合成资料可以验证流程完整性，但不能单独支持真实研究结论。 "
            "自变量为资料类型（合成或真实），因变量为结论是否得到证据支持，控制变量为研究目标与分析流程。"
        )
        review_items = ["合成测试材料不能替代真实研究证据，需要在真实样本上复核研究假设。"]
    else:
        body = "本节描述使用合成资料复核工作流的方法，不把流程结果解释为真实研究结论。"
        review_items = []
    return {"body_markdown": body, "source_ids": [source_id], "review_items": review_items}


class Handler(BaseHTTPRequestHandler):
    server_version = "G6FakeOpenAI/1.0"

    def _json(self, status: int, value: Any) -> None:
        data = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self) -> None:
        if self.path in {"/health", "/v1/health"}:
            self._json(200, {"ok": True, "service": "g6-fake-model"})
            return
        if self.path == "/v1/models":
            self._json(200, {"object": "list", "data": [{"id": "g6-fake-model", "object": "model"}]})
            return
        self._json(404, {"error": {"message": "not found", "type": "invalid_request_error"}})

    def do_POST(self) -> None:
        if self.path.endswith("/embeddings"):
            try:
                length = int(self.headers.get("Content-Length", "0"))
                payload = json.loads(self.rfile.read(length).decode("utf-8"))
                inputs = payload.get("input", [])
                if isinstance(inputs, str):
                    inputs = [inputs]
                if not isinstance(inputs, list):
                    raise ValueError("input must be a string or list")
            except (ValueError, UnicodeDecodeError, json.JSONDecodeError) as error:
                self._json(400, {"error": {"message": str(error), "type": "invalid_request_error"}})
                return
            self._json(200, {
                "object": "list",
                "data": [{"object": "embedding", "embedding": [0.1, 0.2, 0.3], "index": index}
                        for index, _ in enumerate(inputs)],
                "model": payload.get("model", "g6-fake-embedding"),
                "usage": {"prompt_tokens": len(inputs), "total_tokens": len(inputs)},
            })
            return
        if not self.path.endswith("/chat/completions"):
            self._json(404, {"error": {"message": "not found", "type": "invalid_request_error"}})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            raw = self.rfile.read(length)
            payload = json.loads(raw.decode("utf-8"))
            if not isinstance(payload, dict):
                raise ValueError("request must be an object")
        except (ValueError, UnicodeDecodeError, json.JSONDecodeError) as error:
            self._json(400, {"error": {"message": str(error), "type": "invalid_request_error"}})
            return
        request = _request_json(payload)
        draft = _draft(request)
        log_path = getattr(self.server, "request_log", None)
        if isinstance(log_path, Path):
            with log_path.open("a", encoding="utf-8") as stream:
                stream.write(json.dumps({
                    "path": self.path,
                    "model": payload.get("model", ""),
                    "source_id_present": draft["source_ids"] != ["source.unknown"],
                    "source_count": len(draft["source_ids"]),
                    "status": 200,
                }, ensure_ascii=False, separators=(",", ":")) + "\n")
        response = {
            "id": "g6-fake-completion",
            "object": "chat.completion",
            "created": int(time.time()),
            "model": payload.get("model", "g6-fake-model"),
            "choices": [{"index": 0, "message": {
                "role": "assistant", "content": json.dumps(draft, ensure_ascii=False)},
                "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 128, "completion_tokens": 96, "total_tokens": 224},
        }
        self._json(200, response)

    def log_message(self, _format: str, *_args: Any) -> None:
        return


def main() -> int:
    parser = argparse.ArgumentParser(description="Run the G6 OpenAI-compatible fake model")
    parser.add_argument("--bind", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=18080)
    parser.add_argument("--log", type=Path)
    args = parser.parse_args()
    server = ThreadingHTTPServer((args.bind, args.port), Handler)
    server.request_log = args.log
    print(f"g6 fake model listening on {args.bind}:{args.port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        return 0
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
