#!/usr/bin/env python3
"""将已并入单机staging的资源名称加入清单；不解析或解密Unity资源。"""
import json
from pathlib import Path
import sys


def main():
    root = Path(sys.argv[1]).resolve()
    types = json.loads((Path(__file__).parent / "asset-types.json").read_text())
    manifest = root / "assets/backend/cn350/serve_asset_info_android4_v2_short.txt"
    rows = manifest.read_text().strip().split("@")
    metadata, assets = [], {}
    for row in rows:
        fields = row.split(",")
        if fields[0] in {"version", "ios_version", "android_version", "maintenance"}:
            metadata.append(fields)
            continue
        if len(fields) < 5:
            raise SystemExit("基线清单形状不符合约定")
        idx = int(fields[1])
        name = types[idx][1] + fields[0]
        assets[name] = fields[:5]
    unity = root / "assets/unity"
    if not unity.is_dir():
        raise SystemExit("缺少已合并的assets/unity")
    for path in sorted(unity.rglob("*.Android.unity3d")):
        if path.is_symlink():
            raise SystemExit("不允许资源符号链接")
        name = path.relative_to(unity).as_posix()[:-len(".Android.unity3d")]
        if name in assets:
            continue
        matches = [(i, prefix) for i, (_, prefix, _) in enumerate(types) if name.startswith(prefix)]
        if not matches:
            raise SystemExit("未知资源类型："+name)
        longest = max(len(prefix) for _, prefix in matches)
        candidates = [(i, prefix) for i, prefix in matches if len(prefix) == longest]
        # unit_btn_recruit/unit_btn share a prefix; normal local bundles use unit_btn.
        idx, prefix = candidates[-1]
        assets[name] = [name[len(prefix):], str(idx), "1", "0", "1"]
    # Force a newer config version after extending the embedded asset set.
    for fields in metadata:
        if fields[0] == "version":
            fields[1] = str(int(fields[1])+1)
    manifest.write_text("@".join(",".join(fields) for fields in metadata + [assets[name] for name in sorted(assets)])+"\n")
    print(f"单机名称清单：{len(assets)}项；无md5，客户端读取StreamingAssets")


if __name__ == "__main__":
    main()
