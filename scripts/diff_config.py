#!/usr/bin/env python3
"""比较真实运行配置和模板，列出模板有、真实配置缺失或不同的项。

真实配置不入库（含密钥/部署差异），模板改了不会自动生效。部署前先跑：

    make config-diff CONFIG=config.platform.yaml

密钥字段（webhook/token/key/secret/password）不比较，避免把明文打到终端。
退出码：0 一致，1 有差异，2 读取失败。
"""

import sys
from pathlib import Path

try:
    import yaml
except ModuleNotFoundError:  # venv 未建或没装 scripts/requirements.txt
    raise SystemExit("缺少 PyYAML：先跑 make python-deps，或用 .venv/bin/python 执行")

ROOT = Path(__file__).resolve().parent.parent
DEFAULT_TEMPLATE = ROOT / "config.platform.yaml.example"
SENSITIVE = ("webhook", "token", "secret", "password", "authorization", "api_key", "apikey")


def is_sensitive(key: str) -> bool:
    lower = key.lower()
    return any(marker in lower for marker in SENSITIVE) or lower.endswith("key") or lower == "key"


def redacted(key: str, value) -> str:
    if is_sensitive(key):
        return "<已隐藏>"
    text = str(value)
    return text if len(text) <= 60 else text[:57] + "..."


def named(items):
    """把 [{name: x, ...}] 转成 {name: item}；不是具名列表时返回 None。"""
    if not isinstance(items, list) or not items:
        return None
    if not all(isinstance(item, dict) and isinstance(item.get("name"), str) for item in items):
        return None
    return {item["name"]: item for item in items}


def compare(template, real, path: str, out: list[str]) -> None:
    key = path.rsplit(".", 1)[-1]
    if is_sensitive(key):
        return

    if isinstance(template, dict):
        if not isinstance(real, dict):
            out.append(f"{path}: 类型不同（模板是映射）")
            return
        for name, value in template.items():
            child = f"{path}.{name}" if path else name
            if name not in real:
                out.append(f"{child}: 缺失")
            else:
                compare(value, real[name], child, out)
        return

    if isinstance(template, list):
        if not isinstance(real, list):
            out.append(f"{path}: 类型不同（模板是列表）")
            return
        left_named, right_named = named(template), named(real)
        if left_named is not None and right_named is not None:
            for name in left_named:
                if name not in right_named:
                    out.append(f"{path}[{name}]: 缺失")
                else:
                    compare(left_named[name], right_named[name], f"{path}[{name}]", out)
            return
        if all(isinstance(item, str) for item in template) and all(isinstance(item, str) for item in real):
            missing = [item for item in template if item not in real]
            added = [item for item in real if item not in template]
            if missing:
                out.append(f"{path}: 缺少模板项 {missing}")
            if added:
                out.append(f"{path}: 多出 {added}（确认是否本地扩展）")
            return
        if template != real:
            out.append(f"{path}: 不同（模板={redacted(key, template)}，实际={redacted(key, real)}）")
        return

    if template != real:
        out.append(f"{path}: 实际={redacted(key, real)}，模板={redacted(key, template)}")


def main() -> int:
    if len(sys.argv) < 2:
        print("用法: diff_config.py <真实配置> [模板]", file=sys.stderr)
        return 2
    real_path = Path(sys.argv[1])
    template_path = Path(sys.argv[2]) if len(sys.argv) > 2 else DEFAULT_TEMPLATE
    try:
        template = yaml.safe_load(template_path.read_text(encoding="utf-8"))
        real = yaml.safe_load(real_path.read_text(encoding="utf-8"))
    except (OSError, yaml.YAMLError) as err:
        print(f"读取失败: {err}", file=sys.stderr)
        return 2

    diffs: list[str] = []
    compare(template, real, "", diffs)
    if not diffs:
        print(f"{real_path} 与模板一致")
        return 0
    print(f"{real_path} 与模板差异（{len(diffs)} 项）：")
    for line in diffs:
        print(f"- {line}")
    print("按需把模板变更合进真实配置；密钥字段已跳过。")
    return 1


if __name__ == "__main__":
    sys.exit(main())
