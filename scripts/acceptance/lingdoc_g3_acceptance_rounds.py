#!/usr/bin/env python3
"""G3 验收台 · 当前 head 的两轮实跑编排。

#295 补的三条链（路线图编辑会话 / 受控故障注入的可观测口径 / 助手两组说明与
依赖候选读回）要落证，需要一次**受控故障注入命中**的运行。受控故障注入是
组合根按环境变量装配的夹具，按**租户**限定（`internal/container/lingdoc_fault_injection.go`），
所以本脚本跑两轮，用**同一个账号、同一个租户、同一个 SQLite**：

- **轮 1**：后端**不装配**注入。按 #295 的口径，AC-G3-03 此时必须记 ``NOT RUN``
  并指向夹具证据，**不得**判 ``PASS``。
- **轮 2**：后端**装配**注入（``LINGDOC_FAULT_INJECTION`` +
  ``LINGDOC_FAULT_TENANT_ID``）。此时 apply 会返回 503 + ``injected_fault``，
  回滚可观测，AC-G3-03 才有资格判定。

三条链各用**各的项目**：链路自己会 apply 变更集从而推进项目的上下文修订，
共用一个项目时后一条链会撞 409「基线已漂移」。

用法::

    python scripts/acceptance/lingdoc_g3_acceptance_rounds.py --binary .g3-server.exe
    python scripts/acceptance/lingdoc_g3_acceptance_rounds.py --binary .g3-server.exe --out .acceptance-rounds

前提：二进制是**当前 head** 编译的（``-ldflags "-X .../handler.CommitID=<head>"``），
证据里的 ``backend_head.commit_id`` 必须与仓库 head 一致。
"""
from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "acceptance"))

import lingdoc_research_closure as base  # noqa: E402


def run_evidence(
    base_url: str,
    account_email: str,
    account_password: str,
    out_dir: Path,
    only_chains: str = "",
    skip_lifecycle: bool = False,
    timeout: int = 900,
) -> dict[str, object]:
    """跑一次真实验收台链路（``--live``），返回落盘证据文件列表。"""
    env = os.environ.copy()
    env["G3_BASE_URL"] = base_url
    env["G3_EVIDENCE_DIR"] = str(out_dir)
    env["G3_ACCOUNT_EMAIL"] = account_email
    env["G3_ACCOUNT_PASSWORD"] = account_password
    if only_chains:
        env["G3_ONLY_CHAINS"] = only_chains
    if skip_lifecycle:
        # 装配了注入的那一轮里 apply 返回 503，前置的变更集链会中断并把整轮带崩。
        env["G3_SKIP_LIFECYCLE"] = "1"
    out_dir.mkdir(parents=True, exist_ok=True)
    npx = shutil.which("npx.cmd") or shutil.which("npx")
    if not npx:
        raise SystemExit("npx not found on PATH")
    started = time.monotonic()
    proc = subprocess.run(
        [npx, "tsx", "scripts/g3demo/run-evidence.ts", "--live"],
        cwd=str(ROOT / "frontend"),
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=timeout,
    )
    elapsed = round(time.monotonic() - started, 1)
    print(proc.stdout[-8000:])
    if proc.returncode != 0:
        print(proc.stderr[-4000:], file=sys.stderr)
        raise SystemExit(f"run-evidence failed rc={proc.returncode}")
    return {
        "elapsed_seconds": elapsed,
        "files": sorted(p.name for p in out_dir.glob("*.json")),
    }


def register_account(base_url: str, db_path: Path) -> tuple[str, str, int]:
    """注册一个合成演示账号，返回 (email, password, tenant_id)。

    口令只留在内存里，**不落盘**；证据里只留账号标识与租户号。
    """
    suffix = base.uuid.uuid4().hex[:12]
    email = f"g3acceptance-{suffix}@example.com"
    password = "Synthetic" + base.uuid.uuid4().hex[:6] + "42"
    status, registration, _ = base.request(
        base_url,
        "POST",
        "/api/v1/auth/register",
        payload={"username": "g3acc-" + suffix, "email": email, "password": password},
        expected=(200, 201),
    )
    if status not in (200, 201):
        raise RuntimeError(f"registration failed: {status} {registration}")
    _, login, _ = base.request(
        base_url,
        "POST",
        "/api/v1/auth/login",
        payload={"email": email, "password": password},
    )
    token = login.get("token")
    if not isinstance(token, str) or not token:
        raise RuntimeError("login did not return a token")
    tenant_id = base.tenant_id_of(db_path, token, email)
    return email, password, tenant_id


def start_server(binary: Path, data_dir: Path, port: int, fault: tuple[str, int] | None):
    env = base.server_environment(data_dir, port)
    if fault is not None:
        point, tenant_id = fault
        env["LINGDOC_FAULT_INJECTION"] = point
        env["LINGDOC_FAULT_TENANT_ID"] = str(tenant_id)
    proc = base.subprocess.Popen(
        [str(binary)], env=env, cwd=str(ROOT), stdout=None, stderr=None
    )
    base.wait_health(proc, f"http://127.0.0.1:{port}", timeout=240)
    return proc


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True, help="当前 head 编译出的后端二进制")
    parser.add_argument("--out", default="", help="证据输出目录（默认临时目录）")
    parser.add_argument(
        "--fault-point",
        default="impact_task_write",
        help="轮 2 装配的注入点（impact_task_write / audit_write / authorization）",
    )
    parser.add_argument(
        "--round2-chains",
        default="fault",
        help="轮 2 只跑哪几条链（fault / roadmap / assistant，逗号分隔；默认只跑 fault）",
    )
    parser.add_argument(
        "--round1-chains",
        default="",
        help="轮 1 跑哪几条链（空 = 三条全跑）",
    )
    args = parser.parse_args()

    binary = Path(args.binary).resolve()
    if not binary.exists():
        print(f"missing binary: {binary}")
        return 2

    data_dir = Path(tempfile.mkdtemp(prefix="g3-acceptance-"))
    out_dir = Path(args.out).resolve() if args.out else Path(tempfile.mkdtemp(prefix="g3-evidence-"))
    out_dir.mkdir(parents=True, exist_ok=True)
    port = base.free_port()
    base_url = f"http://127.0.0.1:{port}"
    print(f"data_dir={data_dir}")
    print(f"out_dir={out_dir}")
    print(f"base_url={base_url}")

    # —— 轮 1：不装配注入 ——
    proc = start_server(binary, data_dir, port, None)
    print("round1: healthy (no fault injection)")
    email, password, tenant_id = register_account(base_url, data_dir / "lingdoc.sqlite")
    print(f"round1: registered {email} · tenant_id={tenant_id}")
    round1 = run_evidence(base_url, email, password, out_dir / "round1", args.round1_chains)
    code1 = base.stop_process(proc)
    print(f"round1: server stopped, exit code={code1} (Windows TerminateProcess 期望 1)")

    # —— 轮 2：装配注入 ——
    proc2 = start_server(binary, data_dir, port, (args.fault_point, tenant_id))
    print(f"round2: healthy (fault injection armed for tenant {tenant_id})")
    round2 = run_evidence(
        base_url,
        email,
        password,
        out_dir / "round2",
        args.round2_chains,
        skip_lifecycle=True,
    )
    code2 = base.stop_process(proc2)
    print(f"round2: server stopped, exit code={code2}")

    summary = {
        "base_url": base_url,
        "data_dir": str(data_dir),
        "out_dir": str(out_dir),
        "account_email": email,
        "tenant_id": tenant_id,
        "fault_point": args.fault_point,
        "round1_without_fault_injection": round1,
        "round2_with_fault_injection": round2,
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
