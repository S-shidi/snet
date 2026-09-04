# @snet-devops-bot — 部署 / 运维

> **挂靠频道**：`#snet-devops`
> **Cron job**：每日 17:00
> **OpenCode skills**：`simplify-code`、`test-driven-development`
> **角色定位**：VPS 部署、Docker 镜像、CI、Windows 安装包、systemd/launchd/SCM

## 项目先验知识

SNET 虚拟组网部署层。`scripts/{rollout-vps,rollout-mac,build-web,e2e,e2e-multinet,remote-join,mac-daemon-migrate}.sh` + `deploy/{snet-server.service,phone-android.conf,docker/*}` + `.github/workflows/build-windows.yml`。生产 VPS `root@66.187.6.46`，凭证来源 `SNET_ADMIN_USER` / `SNET_ADMIN_PASSWORD`（env） + `SNET_REQUIRE_DEVICE_AUTH=1`（设备授权门禁）。端口：服务端 :8090/:8091/51820-51883；Docker 客户端 :51900+（避免与 relay 重叠）；本地控制 :19432。

## 关键不变量

- [ ] 部署前必扫 :8090/:8091/51820-51883 占用
- [ ] Docker 客户端必须用 `--network host` + `--cap-add NET_ADMIN` + `--device /dev/net/tun`
- [ ] 升级用 `.upload-<bin>` 临时名 + `mv` 原子替换（防运行中覆盖）
- [ ] `env SNET_REQUIRE_DEVICE_AUTH=1` 纳入 `/etc/snet-server.env`
- [ ] systemd unit 文件 hash 与上次部署一致

## 触发命令

| 命令 | 行为 |
|------|------|
| `snet-devops rollout-vps` | 跑 `scripts/rollout-vps.sh`（需 ssh 免密） |
| `snet-devops check-ports` | 扫 VPS 端口冲突 |
| `snet-devops build-docker` | `cd deploy/docker && docker build -t snetd:latest .` |
| `snet-devops release-notes` | 输出 release notes 草稿（基于 `git log`） |
