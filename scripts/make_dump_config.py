#!/usr/bin/env python3
"""从 config.platform.yaml.example 生成 config.dump.yaml。

source-dump 需要一个不推送、跳过时段门控的配置。手抄一份很容易和模板漂移
（2026-11-09 金十评分参数就漏同步过一次），所以改成生成：

- session 源去掉 schedule/sinks，加 options.force=true（跳过交易日/时间门控）
- session-us 去掉 us-macro-report 成员（dump 环境通常没有 FRED key）
- 追加 brief-* 源，把时段简报的成员单独拆出来逐段核对

生成结果是丢弃注释的纯配置；真源永远是 config.platform.yaml.example。
"""

from pathlib import Path

try:
    import yaml
except ModuleNotFoundError:  # venv 未建或没装 scripts/requirements.txt
    raise SystemExit("缺少 PyYAML：先跑 make python-deps，或用 .venv/bin/python 执行")

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "config.platform.yaml.example"
TARGET = ROOT / "config.dump.yaml"

HEADER = """\
# 自动生成，请勿手改：make dump-config
# 真源：config.platform.yaml.example（source-dump 用的免推送/跳过时段门控版本）
"""

BRIEFS = [
    {
        "name": "brief-pre-market",
        "type": "market-briefing",
        "options": {
            "briefing_type": "pre_market",
            "python_cmd": "python3",
            "scripts_dir": "scripts",
        },
    },
    {
        "name": "brief-closing",
        "type": "market-briefing",
        "options": {
            "briefing_type": "closing",
            "sections": ["sentiment", "hot_sector", "capital_flow", "lhb"],
            "python_cmd": "python3",
            "scripts_dir": "scripts",
        },
    },
    {
        "name": "brief-us-preview",
        "type": "market-briefing",
        "options": {
            "briefing_type": "us_preview",
            "include_fedwatch": False,
            "python_cmd": "python3",
            "scripts_dir": "scripts",
        },
    },
    {
        "name": "brief-us-close",
        "type": "market-briefing",
        "options": {
            "briefing_type": "us_close",
            "include_earnings": True,
            "earnings_min_cap_100m": 200,
            "python_cmd": "python3",
            "scripts_dir": "scripts",
        },
    },
    {
        "name": "brief-us-overnight",
        "type": "market-briefing",
        "options": {"briefing_type": "us_overnight"},
    },
    {
        "name": "brief-hk-close",
        "type": "market-briefing",
        "options": {"briefing_type": "hk_close"},
    },
    {
        "name": "brief-econ-calendar",
        "type": "script",
        "options": {
            "command": "python3 scripts/econ_calendar.py --days 1",
            "timeout": 120,
            "push_output": True,
        },
    },
]


def main() -> None:
    config = yaml.safe_load(SOURCE.read_text(encoding="utf-8"))
    sources = config.setdefault("sources", [])

    for source in sources:
        if source.get("type") != "session":
            continue
        source.pop("schedule", None)
        source.pop("sinks", None)
        options = source.setdefault("options", {})
        options["force"] = True
        if source.get("name") == "session-us":
            options["members"] = [
                member
                for member in options.get("members", [])
                if member.get("type") != "us-macro-report"
            ]

    existing = {source.get("name") for source in sources}
    for brief in BRIEFS:
        if brief["name"] not in existing:
            sources.append(brief)

    body = yaml.safe_dump(
        config,
        sort_keys=False,
        allow_unicode=True,
        default_flow_style=False,
        width=120,
    )
    TARGET.write_text(HEADER + body, encoding="utf-8")
    print(f"generated {TARGET.relative_to(ROOT)} ({len(sources)} sources)")


if __name__ == "__main__":
    main()
