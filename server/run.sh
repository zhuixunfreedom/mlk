#!/usr/bin/env bash
# 从仓库根加载环境文件并启动 CN3.5 服务端。
# 用法：server/run.sh [--no-build] [--] [mercd35 参数]
# MERCD_BIN 可在进程环境中指定二进制路径；.env 只配置服务运行参数。
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
bin="${MERCD_BIN:-$repo_root/output/rebuilt/mercd35}"
if [[ "$bin" != /* ]]; then bin="$repo_root/$bin"; fi
build=true
args=()
while (($#)); do
  case "$1" in
    --no-build) build=false; shift ;;
    -h|--help)
      printf '%s\n' '用法：server/run.sh [--no-build] [--] [mercd35 参数]' \
        '先执行 make env 编辑 .env；参数优先于系统环境和 .env。' \
        '例如：server/run.sh -- -addr 127.0.0.1:18099 -server-host 127.0.0.1:18099'
      exit 0 ;;
    --) shift; args+=("$@"); break ;;
    *) args+=("$1"); shift ;;
  esac
done
cd "$repo_root"
if $build; then
  mkdir -p "$(dirname "$bin")"
  (cd server && CGO_ENABLED=0 go build -trimpath -o "$bin" ./cmd/mercd35)
fi
[[ -x "$bin" ]] || { printf 'run.sh: 二进制不可执行，请先 make build\n' >&2; exit 1; }
exec "$bin" "${args[@]}"
