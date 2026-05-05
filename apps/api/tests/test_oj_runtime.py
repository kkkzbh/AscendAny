from __future__ import annotations

import asyncio
import shutil
import subprocess

import pytest

from apps.api.core.config import OjConfig, Settings
from apps.api.services.oj import JudgeService


def _settings() -> Settings:
    return Settings(
        oj=OjConfig(
            runtime="podman",
            image="ascendany-oj-cpp-python:latest",
            work_dir="var/oj/test-runs",
            compile_timeout_seconds=10,
            time_buffer_seconds=0.2,
            memory_mb=256,
            output_limit_bytes=4096,
        )
    )


def _image_exists() -> bool:
    if not shutil.which("podman"):
        return False
    return (
        subprocess.run(
            ["podman", "image", "exists", "ascendany-oj-cpp-python:latest"],
            check=False,
        ).returncode
        == 0
    )


@pytest.mark.skipif(not _image_exists(), reason="OJ podman image is not built")
def test_oj_podman_runtime_smoke_ac_wa_ce_tle() -> None:
    judge = JudgeService(_settings())

    ac = asyncio.run(
        judge.run_once(
            code="#include <bits/stdc++.h>\nusing namespace std; int main(){int a,b; cin>>a>>b; cout<<a+b<<'\\n';}",
            input_data="1 2\n",
            language="C++",
            time_limit_ms=1000,
            memory_limit_kb=262144,
        )
    )
    assert ac.status == "OK"
    assert ac.stdout.strip() == "3"

    wa = asyncio.run(
        judge.judge(
            code="#include <bits/stdc++.h>\nusing namespace std; int main(){cout<<0<<'\\n';}",
            language="C++",
            testcases=[
                {
                    "input_data": "",
                    "output_data": "1\n",
                    "weight": 1,
                    "time_limit_ms": 1000,
                    "memory_limit_kb": 262144,
                }
            ],
        )
    )
    assert wa.status == "WA"

    ce = asyncio.run(
        judge.run_once(
            code="int main( {",
            input_data="",
            language="C++",
            time_limit_ms=1000,
            memory_limit_kb=262144,
        )
    )
    assert ce.status == "CE"

    tle = asyncio.run(
        judge.run_once(
            code="#include <bits/stdc++.h>\nint main(){while(true){} }",
            input_data="",
            language="C++",
            time_limit_ms=100,
            memory_limit_kb=262144,
        )
    )
    assert tle.status == "TLE"
