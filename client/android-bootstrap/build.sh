#!/usr/bin/env bash
# 编译 BootProvider + gomobile Java stub 为 classes2.dex，拷进 client/decoded 供
# apktool 重打包（apktool 会原样保留根级未知文件）。
# 用法：AAR=/path/mercd35.aar ANDROID_SDK_ROOT=~/Android/Sdk ./build.sh
set -euo pipefail
cd "$(dirname "$0")"

SDK="${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}"
BT="$(find "$SDK/build-tools" -maxdepth 1 -type d -name '[0-9]*' | sort -V | tail -1)"
AJAR="$(find "$SDK/platforms" -maxdepth 2 -name android.jar | sort -V | tail -1)"
[ -x "$BT/d8" ] && [ -f "$AJAR" ] || { echo "SDK 缺 build-tools/android.jar：$SDK" >&2; exit 1; }
AAR="${AAR:-/tmp/mercd35.aar}"
DECODED="$(cd ../decoded && pwd)"

[ -f "$AAR" ] || { echo "缺少 AAR：gomobile bind -target=android -androidapi 21 -o $AAR ./server/mobile"; exit 1; }

rm -rf aar classes dex
mkdir -p aar classes dex
unzip -o -q "$AAR" -d aar

# d8（build-tools 30+）需要 Java 11+ 运行时，而 javac 侧用 JDK8 配
# -bootclasspath 编 android.jar；两者分开选择。
JAVAC8="${JAVAC8:-javac}"
D8_JAVA_HOME="${D8_JAVA_HOME:-$(/usr/libexec/java_home -v 11+ 2>/dev/null || true)}"
if [ -n "$D8_JAVA_HOME" ]; then
    D8() { PATH="$D8_JAVA_HOME/bin:$PATH" "$BT/d8" "$@"; }
else
    D8() { "$BT/d8" "$@"; }
fi

"$JAVAC8" -source 8 -target 8 -bootclasspath "$AJAR" \
    -cp "$AJAR:aar/classes.jar" -d classes src/com/mercd/backend/BootProvider.java

D8 --lib "$AJAR" --min-api 21 --output dex \
    classes/com/mercd/backend/*.class aar/classes.jar

cp dex/classes.dex "$DECODED/classes2.dex"
echo "=> $DECODED/classes2.dex"

# libgojni.so 只放 armeabi-v7a：客户端本体是 ARM32-only（libmain.so 等只有
# armeabi-v7a 一份）。APK 里一旦出现第二个 ABI 目录，安装器就按该 ABI 选进程
# 位数——x86/x86_64 会让模拟器 libmain.so 加载失败，arm64-v8a 会让 64 位真机
# 同样闪退（Unable to find native library main）。32 位 Go 在 ARM64 真机上经
# AArch32 兼容模式运行，houdini 模拟器亦走此路径。
rm -rf "$DECODED/lib/arm64-v8a" "$DECODED/lib/x86" "$DECODED/lib/x86_64"
mkdir -p "$DECODED/lib/armeabi-v7a"
cp "aar/jni/armeabi-v7a/libgojni.so" "$DECODED/lib/armeabi-v7a/libgojni.so"
echo "=> $DECODED/lib/armeabi-v7a/libgojni.so"

# 服务端资源打进 APK assets（首次启动由 BootProvider 解压到 filesDir）。
SERVER="$(cd ../../server && pwd)"
rm -rf "$DECODED/assets/backend"
mkdir -p "$DECODED/assets/backend"
cp -r "$SERVER/assets/cn350" "$DECODED/assets/backend/cn350"
cp -r "$SERVER/assets/shared/cards" "$DECODED/assets/backend/cards"
echo "=> $DECODED/assets/backend/{cn350,cards}"

# 单 APK 整包：remote_assets（server-state-cn35，运行时种子不入库）并入
# assets/unity 走 StreamingAssets——清单行无 md5 时客户端直接读包内文件，
# 跳过首启 1.1G 解压与持久目录双份占用。staging 树 decoded-single 是
# decoded 的 APFS clonefile 快照，避免污染 tracked 的 assets/unity。
REMOTE="${REMOTE_ASSETS_DIR:-$SERVER/../inputs/files}"
SINGLE="$DECODED/../decoded-single"
if [ -d "$REMOTE" ]; then
    rm -rf "$SINGLE"
    cp -Rc "$DECODED" "$SINGLE" 2>/dev/null || cp -R "$DECODED" "$SINGLE"
    # src/.（而非 src/）保证 GNU cp 也按“内容并入”处理；BSD cp 对 src/
    # 是内容并入、GNU cp 是目录整体——Linux 上会错误产出 unity/remote_assets/。
    cp -Rf "$REMOTE/." "$SINGLE/assets/unity/"
    python3 "$SERVER/../client/merge_asset_manifest.py" "$SINGLE"
    echo "=> ${SINGLE} (remote_assets 已并入 assets/unity，清单已重生成)"
    echo "完成，接下来跑 CLIENT_SOURCE_DIR=client/decoded-single client/build.sh --sign"
else
    echo "警告：缺少 $REMOTE，remote_assets 未并入，清单仅覆盖内嵌基线" >&2
    echo "完成，接下来跑 client/build.sh --sign"
fi
