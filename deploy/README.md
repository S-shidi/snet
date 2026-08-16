# vnet 服务器部署

已部署：Hostodo VPS `us-tpa01-8304f360`（66.187.6.46，Debian 13 x86_64，root + systemd）。
对外服务：`https://vnet.uizhi.eu.org:8090`（HTTPS 协调）+ `udp://vnet.uizhi.eu.org:51820..51883`（对称 NAT 中继）。

> 原目标 45.202.246.18 因 IP 被墙弃用。域名 `vnet.uizhi.eu.org` A 记录指向 66.187.6.46。
> 协调端口 8090/tcp（HTTPS）、探测 8091/udp、中继 51820..51883/udp 均已放行并验证。

## 1. 上传二进制

从本机（darwin）构建并上传 `build/linux-<arch>/` 三个二进制：
`server`、`vnetd`、`vnetctl`（确认 `uname -m`：x86_64→linux-amd64，aarch64→linux-arm64）。

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/linux-amd64/... ./cmd/server ./cmd/client/vnetd ./cmd/client/vnetctl
scp build/linux-amd64/server build/linux-amd64/vnetd build/linux-amd64/vnetctl root@66.187.6.46:/usr/local/vnet/bin/
```

## 2. 目录、权限、环境文件、TLS 证书

```bash
ssh root@66.187.6.46
install -d -m 700 /var/lib/vnet
install -d -m 755 /usr/local/vnet/bin
install -d -m 700 /usr/local/vnet/certs
chmod 755 /usr/local/vnet/bin/*

# admin 登录：管理页账号（admin 数据 API 同时兼容 session token 与 VNET_ADMIN_TOKEN）
umask 077
cat > /etc/vnet-server.env <<'EOF'
VNET_ADMIN_USER=admin
VNET_ADMIN_PASSWORD=CHANGE_ME_strong_password
# 可选：静态 token（历史兼容，与登录会话并存）
VNET_ADMIN_TOKEN=CHANGE_ME_random_hex
EOF

# 自签名服务器证书（客户端用 CA 固定校验，见"客户端接入"）
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
  -keyout /usr/local/vnet/certs/server-key.pem \
  -out /usr/local/vnet/certs/server.pem \
  -subj "/CN=vnet.uizhi.eu.org" \
  -addext "subjectAltName=DNS:vnet.uizhi.eu.org,IP:66.187.6.46,DNS:localhost"
chmod 600 /usr/local/vnet/certs/server-key.pem
```

## 3. systemd 服务

```bash
install -m 644 deploy/vnet-server.service /etc/systemd/system/vnet-server.service
systemctl daemon-reload
systemctl enable --now vnet-server
systemctl status vnet-server
curl -sk https://127.0.0.1:8090/healthz   # 应返回 200 OK
```

注意 `deploy/vnet-server.service` 中 `ExecStart` 需按实际环境填 `-relay-host`
（中继对外公布的域名/IP）、证书路径与 relay 端口范围：

```
ExecStart=/usr/local/vnet/bin/server -addr 0.0.0.0:8090 \
  -probe-addr 0.0.0.0:8091 \
  -db /var/lib/vnet/vnet.db \
  -tls-cert /usr/local/vnet/certs/server.pem \
  -tls-key /usr/local/vnet/certs/server-key.pem \
  -relay-host vnet.uizhi.eu.org -relay-base 51820 -relay-count 64 \
  -zombie-ttl 72h
```

> 服务器参数：`-tls-cert/-tls-key` 启用 HTTPS；`-relay-host/-relay-base/-relay-count`
> 启用 UDP 中继（每网络分配一个端口）。`-behind-proxy` 供反代场景读取 X-Forwarded-For。
> `-zombie-ttl`：任何网络（含 owner 网络）连续 N 时间无设备在线即自动删除；默认 72h，`0` 关闭。
> **服务端直接创建的网络（管理页「创建网络」）不受清理限制**，且无网主、客户端无法认领。
> 在线判定 = 客户端 HTTP 轮询（LastActivityAt）+ 中继收包（每端口 5s 节流）双来源。

### 可选：强制设备授权（-require-device-auth）

默认关闭（兼容存量部署与 e2e 回归）。开启后，**未绑定设备授权码的设备无法
创建/加入/注册**（create/join/register 返回 403「设备未授权」），达到"只允许白名单
设备接入"的效果：

```bash
# 开启方式一：命令行标志
ExecStart=.../server -require-device-auth ...
# 开启方式二：环境文件（/etc/vnet-server.env）
VNET_REQUIRE_DEVICE_AUTH=1
```

- 开启后先在管理页「设备」页生成若干授权码，再让各客户端在桌面端「设置」里
  用授权码「链接服务器」（或 `vnetctl bind --server ... --code ...`）。
- 授权码仅存哈希 + 末尾 4 位掩码，明文只在生成时显示一次；一码一设备；
  换码自动释放旧码；吊销/解绑只影响后续准入，已加入节点由管理员踢出。
- 绑定接口 `/api/v1/devices/bind` 不套强制门槛，但按 IP 限频（20 次/分钟）。

## 4. 防火墙

放行以下端口（Debian 13 默认无活动防火墙；如启用 nftables/iptables 需放行）：

```bash
iptables -A INPUT -p tcp --dport 8090 -j ACCEPT   # HTTPS 协调
iptables -A INPUT -p udp --dport 8091 -j ACCEPT   # 公网 IP 探测回显
iptables -A INPUT -p udp --dport 51820:51883 -j ACCEPT  # UDP 中继池
```

> 若 VPS 还有云平台安全组，同样放行 tcp 8090 / udp 8091 / udp 51820-51883。

## 5. 客户端接入

- 桌面端「设置」里服务器填 `https://vnet.uizhi.eu.org:8090`，并指定 CA 固定证书
  （服务器公钥证书 `server.pem`，客户端 `vnetd --ca-path` / `vnetctl --ca-path`）。
- 客户端为多网络模式：一台机器可加入多个网络并存（每个网络独立 utun + WG 端口，
  端口从 `--port` 起自动探测空闲）；单协调服务器（多服务器请另跑一个 daemon）。
- 创建设备身份：`vnetd` 首次启动生成 16 位设备 ID（macOS 取 IOPlatformUUID、Linux 取 DMI product_uuid 派生，虚拟机等取不到时回退随机）存于 `/usr/local/vnet/device.id`（卸载重装不丢失；硬件 ID 派生，重装系统也不变）；新创建的网络自动认领为 owner（迁移旧网络：`vnetctl claim --nid`）。
- 创建网络 → 生成邀请链接/配对码 → 另一端复制链接加入（加入方同样需 `--ca-path`）。
- 客户端创建的网络 72h 无任何设备在线会被服务端清理；owner 可在管理页/客户端看到僵尸状态。
  服务端在管理页创建的网络无此限制，配对码长期有效、可反复加入（管理页重置即作废旧码）。
- 数据面：客户端优先 NAT 打洞直连（UDP 8091 探测）；打不通时经服务器 UDP 中继转发。
  中继端点由服务器下发给各对端（`relay-host:网络端口`），客户端无需手动指定。

## 6. 管理页

设置 `VNET_ADMIN_USER`/`VNET_ADMIN_PASSWORD`（或保留 `-admin-token`）后：
- 页面：`https://vnet.uizhi.eu.org:8090/admin`（登录后使用）
- 登录：用户名/密码 → 内存 session token（24h 过期，登录限频 5/min）。
  首次部署时 `VNET_ADMIN_PASSWORD` 作为引导密码，登录成功即写入数据库（bcrypt 哈希）；
  之后改密走页面右上角「修改密码」，`VNET_ADMIN_PASSWORD` 不再生效（改密后所有会话失效需重登）。
- 支持（admin 对所有网络拥有绝对管理权，含客户端 owner 网络）：
  - 创建网络：无网主、豁免 72h 清理、配对码长期有效可反复加入、客户端无法认领
    （需要手机等外部设备时用「添加外部节点」生成节点配置）
  - 列网络（含在线/僵尸状态）/ 列设备 / 查看网络成员 / 踢节点 /
    重置配对码 / 编辑网络设置（名称、网段、待批准开关）/ 审批或拒绝待加入请求 /
    删除网络
  - 设备授权码：「设备」页批量生成（1..100）/ 查看掩码与绑定状态 / 吊销 / 解绑设备
- 数据 API 兼容 `Authorization: Bearer <VNET_ADMIN_TOKEN>`（静态 token 双通道，不支持改密）

### 设备授权码（开启强制授权后的准入流程）

```bash
# 1) 管理员生成授权码（管理页「设备」页，或数据 API）
curl -sk -X POST https://vnet.uizhi.eu.org:8090/admin/devices/authcodes/generate \
  -H "Authorization: Bearer $VNET_ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"count":5}'          # 返回 codes（明文，仅此一次）与 ids（吊销用）

# 2) 客户端绑定（桌面端「设置」→「链接服务器」，或命令行）
vnetctl bind --server https://vnet.uizhi.eu.org:8090 --code XXXX...   # 需 -ca-path（如适用）

# 3) 运维
curl -sk https://vnet.uizhi.eu.org:8090/admin/devices/authcodes \
  -H "Authorization: Bearer $VNET_ADMIN_TOKEN"                        # 列表（掩码+绑定状态）
curl -sk -X POST .../admin/devices/authcodes/revoke  -d '{"id":"..."}'     # 吊销某码（204）
curl -sk -X POST .../admin/devices/authcodes/unbind -d '{"deviceId":"..."}' # 解绑设备（204）
```

## 7. 运维

```bash
journalctl -u vnet-server -f
# 数据落盘于 /var/lib/vnet/vnet.db（bbolt），重启不丢
# 备份：停止服务后 cp 该文件（或定期拷出）
# 已部署环境的 admin token 记录于 /etc/vnet-server.env（VNET_ADMIN_TOKEN）
# 证书 10 年有效；到期前需重新生成 server.pem/server-key.pem 并同步到所有客户端
```

## 8. Windows 客户端（构建 + 部署）

### 构建

Tauri 无法在 macOS 交叉编译 Windows 安装包，需在一台 Windows 机器/VM 上构建。
前置：Rust（`rustup` 默认 MSVC toolchain）、Visual Studio Build Tools（含「使用 C++ 的桌面开发」）、
Node 20+、WebView2（Win10/11 一般已内置）。

```powershell
cd desktop
npm install
npm run tauri build -- --bundles nsis   # 产物：src-tauri/target/release/bundle/nsis/SNET_*-x64-setup.exe
```

`tauri.conf.json` 的 `bundle.resources` 会把仓库 `build/windows-amd64/` 下的
`vnetd.exe`、`wintun.dll`、`LICENSE-wintun.txt` 打进安装包（位于安装目录，SNET.exe 旁）。

也可用 GitHub Actions 构建（无需本地 Rust/Node 环境）：推送到远程后手动触发
`.github/workflows/build-windows.yml`（`Actions` → `Build Windows installer` → `Run workflow`），
或在 `v*` 打 tag 时自动构建；安装包在构建完成后作为 `snet-windows-nsis` artifact 下载。
工作流会先用 go 重新编译 `vnetd.exe/vnetctl.exe`（windows/amd64，覆盖仓库内旧产物），
再执行 `npm ci && npm run tauri build -- --bundles nsis`。

### 首次运行（自动安装后台服务）

打开 GUI 后如 daemon 不可达（`127.0.0.1:19432`），「启动后台服务」会弹出 UAC 提示，
以管理员身份执行一段 PowerShell（写临时 `.ps1` 再 `-Verb RunAs` 启动，避免嵌套引号），完成：

- 在 `C:\ProgramData\SNET` 建目录；
- 停/删旧服务后，`New-Service` 注册 **vnetd**（LocalSystem、自动启动），
  `BinaryPathName` = `"C:\Program Files\SNET\vnetd.exe" -config "C:\ProgramData\SNET\daemon.json" -device-id-file "C:\ProgramData\SNET\device.id"`；
- `sc.exe failure vnetd reset= 0 actions= restart/5000/restart/10000/restart/30000`
  （崩溃自动重启；daemon 收到 `/ctl/shutdown` 会干净上报停止，正常退出不会被拉起）；
- `New-NetFirewallRule` 放行 `C:\Program Files\SNET\vnetd.exe` 入站（WireGuard UDP，含 wintun 适配器流量）；
- `Start-Service vnetd`。

GUI 不 spawn 子进程；daemon 由 SCM 托管，重启后自启。

### 手动服务管理

```powershell
sc.exe query vnetd                       # 状态
Start-Service vnetd / Stop-Service vnetd
sc.exe delete vnetd                      # 先 Stop
# 卸载残留：sc.exe failure vnetd reset= 0 actions= restart/5000/restart/10000/restart/30000（重设）
curl.exe http://127.0.0.1:19432/ctl/status   # daemon 状态
```

### 已知注意点

- 安装包未签名 → SmartScreen「更多信息→仍要运行」；服务与驱动不涉及内核签名（wintun 自带微软签名）。
- wintun 强制 MTU 1420（配置的 mtu 在 Windows 忽略）；适配器名固定 `vnet`，daemon 重启间复用。
- 设备 ID：Windows 取注册表 `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid` 派生（重装系统会变）。
- 客户端创建的网络与 macOS/Linux 一致：优先 UDP 打洞直连，失败经服务器中继转发，互通不依赖平台。

## 9. 手机（Android WireGuard App）


服务器创建网络后，用手机导入对端配置（QR 或文件，见 `deploy/phone-android.conf`）：
- `Endpoint` 填服务器中继地址 `vnet.uizhi.eu.org:51820`（域名，App 会自行解析）；
- `PersistentKeepalive` 建议 **10**（运营商 CGNAT 会话超时短，保活过慢会周期性丢包）；
- 重要：在系统设置中把 WireGuard 的电池/后台限制设为「无限制」，
  否则手机 Doze 会挂起隧道、导致入向流量丢失。
