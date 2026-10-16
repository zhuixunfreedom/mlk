# 用户自备材料

- `apk/国服-3.5.0.apk`：按 apk/SHA256SUMS 校验；不是任意同版本 APK。
- `files/`：Unity 资源目录，按逻辑名放置，如 unit/unit_414.Android.unity3d。不同文件集决定可用素材范围。
- 故事 fixture（可选）：将 index.json、story\_<id>.json 放入 `server/assets/cn350/stories/`，格式见 `server/internal/cn35game/story.go`。此仓库不提供原作剧本。

不要将以上材料、账号信息或签名文件提交到 Git。自备材料是否齐全必须按自己的素材清单检查；运行时 remote_assets_state.json 仅是版本/digest 台账。
