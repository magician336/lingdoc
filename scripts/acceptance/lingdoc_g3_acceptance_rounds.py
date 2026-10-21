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


def date_stamp() -> str:
    """证据文件名里的日期戳（与前端 ``dateStamp()`` 同一形状）。"""
    now = time.localtime()
    return f"{now.tm_year}{now.tm_mon:02d}{now.tm_mday:02d}"


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


def run_dependency_chain(
    base_url: str,
    out_dir: Path,
    date_stamp: str,
    round_label: str,
    timeout: int = 900,
) -> dict[str, object]:
    """跑依赖候选链（独立脚本），把「验收台-4」形态的证据落进本轮目录。

    整链导出（``run-evidence.ts`` 的 ``buildAggregateEvidenceExport``）汇总的是
    **证据目录里所有 ``G3-验收台-*`` 文件**。依赖候选链的判定（AC-G3-15）住在
    它自己的证据文件里，所以这一步必须**先**跑，文件落进同一个目录，整链导出
    才带得上它的判定。

    它有**自己的账号与自己的项目**：候选的 ``target`` 必须是本项目的章节版本，
    复用别的链的项目会撞 409「基线已漂移」。

    文件名带轮次后缀（``-r1`` / ``-r2``）：两轮是**两次不同的运行**，同名的话
    整链证据里那条 ``runs[].file`` 引用会指到另一轮的文件上。
    """
    out_dir.mkdir(parents=True, exist_ok=True)
    npx = shutil.which("npx.cmd") or shutil.which("npx")
    if not npx:
        raise SystemExit("npx not found on PATH")
    out_path = out_dir / f"G3-验收台-4-依赖图与影响任务证据-{date_stamp}-{round_label}.json"
    started = time.monotonic()
    proc = subprocess.run(
        [
            npx,
            "tsx",
            "scripts/g3demo/run-dependencies.ts",
            "--base",
            base_url,
            "--out",
            str(out_path),
        ],
        cwd=str(ROOT / "frontend"),
        env=os.environ.copy(),
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=timeout,
    )
    elapsed = round(time.monotonic() - started, 1)
    print(proc.stdout[-4000:])
    if proc.returncode != 0:
        print(proc.stderr[-4000:], file=sys.stderr)
        raise SystemExit(f"run-dependencies failed rc={proc.returncode}")
    return {"elapsed_seconds": elapsed, "file": out_path.name}


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


def write_builtin_models_yaml(data_dir: Path) -> Path:
    """写一份**只含占位符**的模型登记文件（仓库外，随临时目录一起消失）。

    服务端启动时把 ``${DEEPSEEK_API_KEY}`` 从环境变量插值后 upsert 成模型行
    （``internal/types/builtin_models_config.go`` 的 ``LoadBuiltinModelsConfig``）。
    **这份文件里没有凭据本身**：凭据只从环境变量读，插值发生在服务端进程内。

    行的形状：``name=deepseek-flash`` / ``type=KnowledgeQA`` / ``source=remote`` /
    ``status=active`` / ``is_default=true`` / ``provider=generic`` /
    ``base_url=https://api.deepseek.com`` /
    ``extra_config.thinking_control=thinking_type``，
    真正发出去的模型名用 ``extra_config.remote_model_name=deepseek-chat``。
    ``is_default=true`` 是让助手链选到它的关键（``defaultGenerationModel``）。
    """
    path = data_dir / "builtin_models.yaml"
    path.write_text(
        "builtin_models:\n"
        "  - id: g3-acceptance-deepseek\n"
        "    name: deepseek-flash\n"
        "    type: KnowledgeQA\n"
        "    source: remote\n"
        "    status: active\n"
        "    is_default: true\n"
        "    parameters:\n"
        "      provider: generic\n"
        "      base_url: https://api.deepseek.com\n"
        '      api_key: "${DEEPSEEK_API_KEY}"\n'
        "      extra_config:\n"
        "        thinking_control: thinking_type\n"
        "        remote_model_name: deepseek-chat\n",
        encoding="utf-8",
    )
    return path


def start_server(
    binary: Path,
    data_dir: Path,
    port: int,
    fault: tuple[str, int] | None,
    model_config: Path | None = None,
):
    env = base.server_environment(data_dir, port)
    if model_config is not None:
        # `server_environment` 只透传一份白名单环境变量，凭据不在白名单里，
        # 所以要跑真实模型轮次时得**显式**把它交给服务端进程。
        key = os.environ.get("DEEPSEEK_API_KEY", "").strip()
        if not key:
            raise SystemExit("要跑真实模型轮次，环境变量 DEEPSEEK_API_KEY 必须有值")
        env["BUILTIN_MODELS_CONFIG"] = str(model_config)
        env["DEEPSEEK_API_KEY"] = key
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
    parser.add_argument(
        "--with-dependency-chain",
        action="store_true",
        help="两轮都跑依赖候选链（它的判定要进整链证据，见 run_dependency_chain）",
    )
    parser.add_argument(
        "--with-real-model",
        action="store_true",
        help="装配真实模型（凭据从环境变量 DEEPSEEK_API_KEY 读，登记文件只含占位符）",
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
    model_config = write_builtin_models_yaml(data_dir) if args.with_real_model else None
    proc = start_server(binary, data_dir, port, None, model_config)
    print("round1: healthy (no fault injection)")
    email, password, tenant_id = register_account(base_url, data_dir / "lingdoc.sqlite")
    print(f"round1: registered {email} · tenant_id={tenant_id}")
    date = date_stamp()
    if args.with_dependency_chain:
        # 依赖候选链先跑：整链导出汇总的是目录里已有的 `G3-验收台-*` 文件。
        run_dependency_chain(base_url, out_dir / "round1", date, "r1")
    round1 = run_evidence(base_url, email, password, out_dir / "round1", args.round1_chains)
    code1 = base.stop_process(proc)
    print(f"round1: server stopped, exit code={code1} (Windows TerminateProcess 期望 1)")

    # —— 轮 2：装配注入 ——
    proc2 = start_server(binary, data_dir, port, (args.fault_point, tenant_id), model_config)
    print(f"round2: healthy (fault injection armed for tenant {tenant_id})")
    if args.with_dependency_chain:
        run_dependency_chain(base_url, out_dir / "round2", date, "r2")
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
