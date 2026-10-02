#!/usr/bin/env python3
"""Run F02 with its declared in-process observer and a local real-service HTTP pass."""
from __future__ import annotations

import json
import os
from pathlib import Path
import runpy
import subprocess
import sys
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[2]
BASE_URL = "http://127.0.0.1:8080"
CONTRACTS = ROOT / "docs/08-本轮实施方案/contracts"
REPORT = ROOT / "docs/08-本轮实施方案/T15-S8-验证报告.json"
def login(email: str, password: str) -> str:
    request = Request(BASE_URL + "/api/v1/auth/login",
                      data=json.dumps({"email": email, "password": password}).encode(),
                      headers={"Content-Type": "application/json"}, method="POST")
    with urlopen(request, timeout=15) as response:
        payload = json.loads(response.read())
    data = payload.get("data", payload)
    token = data.get("token") or data.get("access_token")
    if not isinstance(token, str) or not token:
        raise RuntimeError("local login succeeded without a token field")
    return token


def main() -> int:
    credentials = ROOT / ".scratch-t15-03/env_facts.py"
    if not credentials.is_file():
        raise RuntimeError(f"local fixture credentials are unavailable: {credentials}")
    facts = runpy.run_path(str(credentials))
    token = login(facts["EMAIL"], facts["PASSWORD"])

    env = dict(os.environ, LINGDOC_TEST_TOKEN=token)
    run = subprocess.run([
        sys.executable, "scripts/lingdoc_mock/run_scenario.py", "--scenario", "F02",
        "--base-url", BASE_URL + "/api/v1/lingdoc", "--report", str(REPORT),
    ], cwd=ROOT, env=env, text=True, capture_output=True)
    if run.stdout:
        print(run.stdout, end="")
    if run.stderr:
        print(run.stderr, end="", file=sys.stderr)
    return run.returncode


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, KeyError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"F02 white-box run failed: {error}", file=sys.stderr)
        raise SystemExit(1)
