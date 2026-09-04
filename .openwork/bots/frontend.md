# @snet-frontend-bot — 前端（Tauri 桌面 + 共享 Web UI）

> **挂靠频道**：`#snet-frontend`
> **Cron job**：每日 17:00
> **OpenCode skills**：`subagent-driven-development`（三适配器一致性强校验）
> **角色定位**：Tauri 桌面代理 + 共享 TS UI + 三平台适配器

## 项目先验知识

SNET 虚拟组网，三端共享 UI（`shared/web/{ui,types,utils,subnet}.ts` + `index.html` + `styles.css`），由 `scripts/build-web.sh` 同步到 Android 与 Docker。Tauri v2 桌面代理 `desktop/src-tauri/src/{lib.rs,snet.rs}` 22 个 Tauri 命令 → /ctl/* 代理。三平台适配器：`desktop/src/desktop.ts`（Tauri IPC）、`desktop/src/web.ts`（fetch + auth）、`android/src/android.ts`（@JavascriptInterface → Go/JNI）。当前 es2022 target。

## 关键不变量

- [ ] `shared/web/types.ts` 的 `Backend` 接口 19 方法，三个 adapter 必须全实现
- [ ] 修改 `shared/web/ui.ts` 后是否已跑 `scripts/build-web.sh`（commit message 提示）
- [ ] `tsconfig.json` target 与 esbuild target 一致（当前 es2022）
- [ ] desktop.ts 的 `hasDaemonControl = true`，web.ts = false，android.ts = true

## 触发命令

| 命令 | 行为 |
|------|------|
| `snet-frontend sync-web` | 执行 `scripts/build-web.sh` 并把产物路径贴在频道 |
| `snet-frontend check-adapters` | diff `types.ts` 与三个 adapter 实现 |
| `snet-frontend build-tauri` | `cd desktop && npx tauri build`（需 node + rust） |
