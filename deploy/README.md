# Snet 服务器部署

已部署：Hostodo VPS `us-tpa01-8304f360`（66.187.6.46，Debian 13 x86_64，root + systemd）。
对外服务：`https://snet.uizhi.eu.org:8090`（HTTPS 协调）+ `udp://snet.uizhi.eu.org:51820..52075`（对称 NAT 中继池，懒绑定按需占用）。

> 原目标 45.202.246.18 因 IP 被墙弃用。域名 `snet.uizhi.eu.org` A 记录指向 66.187.6.46。
> 协调端口 8090/tcp（HTTPS）、探测 8091/udp、中继池 51820..52075/udp（可配置 `-relay-count`）均已放行并验证。

## 1. 上传二进制

从本机（darwin）构建并上传 `build/linux-<arch>/` 三个二进制：
`server`、`snetd`、`snetctl`（确认 `uname -m`：x86_64→linux-amd64，aarch64→linux-arm64）。

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/linux-amd64/... ./cmd/server ./cmd/client/snetd ./cmd/client/snetctl
scp build/linux-amd64/server root@66.187.6.46:/usr/local/snet/bin/
```

## 2. 目录、权限、环境文件、TLS 证书

```bash
ssh root@66.187.6.46
install -d -m 700 /var/lib/snet
install -d -m 755 /usr/local/snet/bin
install -d -m 700 /usr/local/snet/certs
chmod 755 /usr/local/snet/bin/*

# admin 登录：管理页账号（admin 数据 API 同时兼容 session token 与 VNET_ADMIN_TOKEN）
# 全部留空也可以：首次访问 /admin 会进入 Web 引导初始化向导创建管理员（见第 6 节）
umask 077
cat > /etc/snet-server.env <<'EOF'
VNET_ADMIN_USER=admin
VNET_ADMIN_PASSWORD=CHANGE_ME_strong_password
# 可选：静态 token（历史兼容，与登录会话并存）
VNET_ADMIN_TOKEN=CHANGE_ME_random_hex
EOF

# 自签名服务器证书（客户端用 CA 固定校验，见"客户端接入"）
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
  -keyout /usr/local/snet/certs/server-key.pem \
  -out /usr/local/snet/certs/server.pem \
  -subj "/CN=snet.uizhi.eu.org" \
  -addext "subjectAltName=DNS:snet.uizhi.eu.org,IP:66.187.6.46,DNS:localhost"
chmod 600 /usr/local/snet/certs/server-key.pem
```

## 3. systemd 服务

```bash
install -m 644 deploy/snet-server.service /etc/systemd/system/snet-server.service
systemctl daemon-reload
systemctl enable --now snet-server
systemctl status snet-server
curl -sk https://127.0.0.1:8090/healthz   # 应返回 200 OK
```

注意 `deploy/snet-server.service` 中 `ExecStart` 需按实际环境填 `-relay-host`
（中继对外公布的域名/IP）、证书路径与 relay 端口范围：

```
ExecStart=/usr/local/snet/bin/server -addr 0.0.0.0:8090 \
  -probe-addr 0.0.0.0:8091 \
  -db /var/lib/snet/snet.db \
  -tls-cert /usr/local/snet/certs/server.pem \
  -tls-key /usr/local/snet/certs/server-key.pem \
  -relay-host snet.uizhi.eu.org -relay-base 51820 -relay-count 256 \
  -zombie-ttl 72h
```

> 服务器参数：`-tls-cert/-tls-key` 启用 HTTPS；`-relay-host/-relay-base/-relay-count`
> 启用 UDP 中继（每网络分配一个端口，**懒绑定**：只有活跃网络的端口才 bind；池内
> 端口被占用时自动跳下一候选，故 `-relay-count` 可取较大值 256 而零启动开销）。
> `-behind-proxy` 供反代场景读取 X-Forwarded-For。
> `-zombie-ttl`：任何网络（含 owner 网络）连续 N 时间无设备在线即自动删除；默认 72h，`0` 关闭。
> **服务端直接创建的网络（管理页「创建网络」）不受清理限制**，且无网主、客户端无法认领。
> 在线判定 = 客户端 HTTP 轮询（LastActivityAt）+ 中继收包（每端口 5s 节流）双来源。
> 协议/API 版本协商：客户端请求带 `X-Snet-Api-Version`，不匹配时回 426（响应头
> `X-Snet-Api-Version` / `X-Snet-Server-Version` 供诊断）；无版本头的旧客户端兼容。

### 可选：强制设备授权（-require-device-auth）

默认关闭（兼容存量部署与 e2e 回归）。开启后，**未绑定设备授权码的设备无法
创建/加入/注册**（create/join/register 返回 403「设备未授权」），达到"只允许白名单
设备接入"的效果：

```bash
# 开启方式一：命令行标志
ExecStart=.../server -require-device-auth ...
# 开启方式二：环境文件（/etc/snet-server.env）
VNET_REQUIRE_DEVICE_AUTH=1
```

- 开启后先在管理页「设备」页生成若干授权码，再让各客户端在桌面端「设置」里
  用授权码「链接服务器」（或 `snetctl bind --server ... --code ...`）。
- 授权码明文存于服务端（bbolt），管理页列表可随时查看复制；一码可绑定多设备（生成时设定）；
  换码自动释放旧码；吊销/解绑只影响后续准入，已加入节点由管理员踢出。
- 绑定接口 `/api/v1/devices/bind` 不套强制门槛，但按 IP 限频（20 次/分钟）。

## 4. 防火墙

放行以下端口（Debian 13 默认无活动防火墙；如启用 nftables/iptables 需放行）：

```bash
iptables -A INPUT -p tcp --dport 8090 -j ACCEPT   # HTTPS 协调
iptables -A INPUT -p udp --dport 8091 -j ACCEPT   # 公网 IP 探测回显
iptables -A INPUT -p udp --dport 51820:52075 -j ACCEPT  # UDP 中继池
```

> 若 VPS 还有云平台安全组，同样放行 tcp 8090 / udp 8091 / udp 51820-52075。

## 5. 客户端接入

- 桌面端「设置」里服务器填 `https://snet.uizhi.eu.org:8090`，并指定 CA 固定证书
  （服务器公钥证书 `server.pem`，客户端 `snetd --ca-path` / `snetctl --ca-path`）。
- 客户端为多网络模式：一台机器可加入多个网络并存（每个网络独立 utun + WG 端口，
  端口从 `--port` 起自动探测空闲）；单协调服务器（多服务器请另跑一个 daemon）。
- 创建设备身份：`snetd` 首次启动生成 16 位设备 ID（macOS 取 IOPlatformUUID、Linux 取 DMI product_uuid 派生，虚拟机等取不到时回退随机）存于 `/usr/local/snet/device.id`（卸载重装不丢失；硬件 ID 派生，重装系统也不变）；新创建的网络自动认领为 owner（迁移旧网络：`snetctl claim --nid`）。设备名自动上报（Android: manufacturer+model，桌面端: hostname），管理端可随时改名。
- 创建网络 → 生成邀请链接/配对码 → 另一端复制链接加入（加入方同样需 `--ca-path`）。
- 客户端创建的网络 72h 无任何设备在线会被服务端清理；owner 可在管理页/客户端看到僵尸状态。
  服务端在管理页创建的网络无此限制，配对码长期有效、可反复加入（管理页重置即作废旧码）。
- 数据面：客户端优先 NAT 打洞直连（UDP 8091 探测）；打不通时经服务器 UDP 中继转发。
  中继端点由服务器下发给各对端（`relay-host:网络端口`），客户端无需手动指定。

## 6. 管理页

### 首次初始化（Web 引导向导）

服务器未配置任何管理员（无 `-admin-token`、无环境变量账号、库内无账号）时，
访问 `/admin` 会显示初始化向导：设置管理员用户名和密码后即可登录使用，
无需提前在服务器上准备凭据。`cmd/server` 启动时若无凭据会打印对应提示。
一旦完成初始化（或配置了环境变量/静态 token），向导自动隐藏，接口返回 `{"available":false}`。

### 登录与权限

- 页面：`https://snet.uizhi.eu.org:8090/admin`
- 登录：用户名/密码 → 内存 session token（24h 过期，登录限频 5/min）。
  通过 `VNET_ADMIN_PASSWORD` 引导的密码在首次登录时写入数据库（bcrypt 哈希）；
  之后改密走页面右上角「修改密码」，`VNET_ADMIN_PASSWORD` 不再生效（改密后所有会话失效需重登）。
- 支持（admin 对所有网络拥有绝对管理权，含客户端 owner 网络）：
  - 创建网络：无网主、豁免 72h 清理、配对码长期有效可反复加入、客户端无法认领
    （需要手机等外部设备时用「添加外部节点」生成节点配置）
  - 网络卡片操作收在名称右侧「⋯ 更多操作」菜单：查看/收起成员 / 编辑设置 /
    邀请 / 查看配对码 / 删除网络；有待批准请求时名称行保留
    「待批准 (N)」可点击按钮（点击打开待批准列表弹窗）
  - 成员弹窗：设备名（设备ID） | IP | 状态 | 操作（踢出），无待批准区块
  - 设备列表：设备名 | ID | 状态 | 操作，搜索支持名称/ID
  - 三个列表（网络/设备/授权码）均为服务端分页（每页 20 条），网络支持按名称/ID/
    网段搜索与 在线/离线/服务端管理/僵尸/待批准 状态筛选
- 数据 API 兼容 `Authorization: Bearer <VNET_ADMIN_TOKEN>`（静态 token 双通道，不支持改密）

### 安全加固

服务端已启用以下安全措施：

- **限流**：创建网络（50/h）、加入网络（60/min）、绑定设备（20/min）、注册设备（30/min）、登录（5/min）
- **安全头**：CSP（default-src 'self'）、no-store、HSTS（1 年）
- **输入校验**：公钥 Base64 44 字节、端点 host:port 格式、设备名/网络名 64 字符
- **错误脱敏**：400 统一 `{"error":"请求格式错误"}`（不泄露解码细节）、500 统一 `{"error":"服务器内部错误"}`
- **XFF 处理**：取最右可信代理 IP（`-behind-proxy` 启用时）
- **日志消毒**：控制字符替换为 `\xNN`
- **凭据保护**：adminUser/adminPassHash 加 RWMutex、bootstrap 加互斥锁
- **POST /admin/logout**：显式注销 session

### 数据 API 形状

分页列表统一返回信封 `{items, total, page, pageSize}`；查询参数
`page`（≥1）、`page_size`（默认 20，上限 100）。越界页码自动收敛到最后一页；
排序固定创建时间倒序（最新在前）。

```bash
# 网络列表：q=关键词(名称/ID/网段) status=online|offline|managed|zombie|pending
curl -sk 'https://snet.uizhi.eu.org:8090/admin/networks?page=1&page_size=20&status=pending' \
  -H "Authorization: Bearer $VNET_ADMIN_TOKEN"
# 设备列表：q=关键词(ID/名称)，返回含 deviceName 字段
curl -sk 'https://snet.uizhi.eu.org:8090/admin/devices?q=dev-abc' -H "Authorization: Bearer $VNET_ADMIN_TOKEN"
# 授权码列表
curl -sk 'https://snet.uizhi.eu.org:8090/admin/devices/authcodes?page=2' -H "Authorization: Bearer $VNET_ADMIN_TOKEN"
# 总览统计（徽标/汇总用，免拉全量列表）
curl -sk https://snet.uizhi.eu.org:8090/admin/stats -H "Authorization: Bearer $VNET_ADMIN_TOKEN"
# → {"networksTotal":..,"networksOnline":..,"nodesTotal":..,"pendingTotal":..,
#     "devicesTotal":..,"codesTotal":..,"codesBound":..,"codesFree":..,"recentNetworks":[..]}
# 设备改名
curl -sk -X PATCH https://snet.uizhi.eu.org:8090/admin/devices/<deviceId> \
  -H "Authorization: Bearer $VNET_ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"新名称"}'
```

### 设备授权码（开启强制授权后的准入流程）

```bash
# 1) 管理员生成授权码（管理页「设备」页，或数据 API）
curl -sk -X POST https://snet.uizhi.eu.org:8090/admin/devices/authcodes/generate \
  -H "Authorization: Bearer $VNET_ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"count":5}'          # 返回 codes（明文）与 ids（吊销用）

# 2) 客户端绑定（桌面端「设置」→「链接服务器」，或命令行）
snetctl bind --server https://snet.uizhi.eu.org:8090 --code XXXX...   # 需 -ca-path（如适用）
# 客户端首次绑定时自动上报设备名（Android: manufacturer+model，桌面端: hostname）；
# 管理端可通过 PATCH /admin/devices/<id> 改名，改名后客户端不再覆盖

# 3) 运维
curl -sk https://snet.uizhi.eu.org:8090/admin/devices/authcodes \
  -H "Authorization: Bearer $VNET_ADMIN_TOKEN"                        # 列表（明文+绑定状态）
curl -sk -X POST .../admin/devices/authcodes/revoke  -d '{"id":"..."}'     # 吊销某码（204）
curl -sk -X POST .../admin/devices/authcodes/unbind -d '{"deviceId":"..."}' # 解绑设备（204）
```

## 7. 运维

```bash
journalctl -u snet-server -f
# 数据落盘于 /var/lib/snet/snet.db（bbolt），重启不丢
# 备份：停止服务后 cp 该文件（或定期拷出）
# 已部署环境的 admin token 记录于 /etc/snet-server.env（VNET_ADMIN_TOKEN）
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
`snetd.exe`、`wintun.dll`、`LICENSE-wintun.txt` 打进安装包（位于安装目录，SNET.exe 旁）。

也可用 GitHub Actions 构建（无需本地 Rust/Node 环境）：推送到远程后手动触发
`.github/workflows/build-windows.yml`（`Actions` → `Build Windows installer` → `Run workflow`），
或在 `v*` 打 tag 时自动构建；安装包在构建完成后作为 `snet-windows-nsis` artifact 下载。
工作流会先用 go 重新编译 `snetd.exe/snetctl.exe`（windows/amd64，覆盖仓库内旧产物），
再执行 `npm ci && npm run tauri build -- --bundles nsis`。

### 首次运行（自动安装后台服务）

打开 GUI 后如 daemon 不可达（`127.0.0.1:19432`），「启动后台服务」会弹出 UAC 提示，
以管理员身份执行一段 PowerShell（写临时 `.ps1` 再 `-Verb RunAs` 启动，避免嵌套引号），完成：

- 在 `C:\ProgramData\SNET` 建目录；
- 停/删旧服务后，`New-Service` 注册 **snetd**（LocalSystem、自动启动），
  `BinaryPathName` = `"C:\Program Files\SNET\snetd.exe" -config "C:\ProgramData\SNET\daemon.json" -device-id-file "C:\ProgramData\SNET\device.id"`；
- `sc.exe failure snetd reset= 0 actions= restart/5000/restart/10000/restart/30000`
  （崩溃自动重启；daemon 收到 `/ctl/shutdown` 会干净上报停止，正常退出不会被拉起）；
- `New-NetFirewallRule` 放行 `C:\Program Files\SNET\snetd.exe` 入站（WireGuard UDP，含 wintun 适配器流量）；
- `Start-Service snetd`。

GUI 不 spawn 子进程；daemon 由 SCM 托管，重启后自启。

### 手动服务管理

```powershell
sc.exe query snetd                       # 状态
Start-Service snetd / Stop-Service snetd
sc.exe delete snetd                      # 先 Stop
# 卸载残留：sc.exe failure snetd reset= 0 actions= restart/5000/restart/10000/restart/30000（重设）
curl.exe http://127.0.0.1:19432/ctl/status   # daemon 状态
```

### 已知注意点

- 安装包未签名 → SmartScreen「更多信息→仍要运行」；服务与驱动不涉及内核签名（wintun 自带微软签名）。
- wintun 强制 MTU 1420（配置的 mtu 在 Windows 忽略）；适配器名固定 `Snet`，daemon 重启间复用。
- 设备 ID：Windows 取注册表 `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid` 派生（重装系统会变）。
- 客户端创建的网络与 macOS/Linux 一致：优先 UDP 打洞直连，失败经服务器中继转发，互通不依赖平台。

## 9. 手机（Android WireGuard App）


服务器创建网络后，用手机导入对端配置（QR 或文件，见 `deploy/phone-android.conf`）：
- `Endpoint` 填服务器中继地址 `snet.uizhi.eu.org:51820`（域名，App 会自行解析）；
- `PersistentKeepalive` 建议 **10**（运营商 CGNAT 会话超时短，保活过慢会周期性丢包）；
- 重要：在系统设置中把 WireGuard 的电池/后台限制设为「无限制」，
  否则手机 Doze 会挂起隧道、导致入向流量丢失。

## 10. Docker 客户端（snetd 容器化部署）

将 snetd 守护进程容器化，用于在 Docker 主机/VPS 上作为虚拟网络成员节点。

### 构建镜像

```bash
# 从项目根目录
docker compose -f deploy/docker/docker-compose.yml build
```

### Web 控制台

容器内置 nginx 提供 Web GUI，访问 `http://<host>:8080` 即可管理网络。

首次打开会显示绑定引导：输入服务器地址和设备授权码，点击「绑定并进入」。
也可以点击「跳过，进入控制台」查看状态，后续在设置中绑定。

### 首次运行（自动绑定）

```bash
SNET_SERVER=https://snet.uizhi.eu.org:8090 \
SNET_BIND_CODE=你的授权码 \
docker compose -f deploy/docker/docker-compose.yml up -d
```

容器启动时自动完成：
1. 启动 snetd（创建设备 ID、WireGuard 隧道）
2. 调用 `snetctl bind` 绑定到服务器
3. 启动 nginx 提供 Web GUI（端口 8080）
4. 持续运行 snetd 守护进程

### 首次运行（自动绑定）

```bash
SNET_SERVER=https://snet.uizhi.eu.org:8090 \
SNET_BIND_CODE=你的授权码 \
docker compose -f deploy/docker/docker-compose.yml up -d
```

容器启动时自动完成：
1. 启动 snetd（创建设备 ID、WireGuard 隧道）
2. 调用 `snetctl bind` 绑定到服务器
3. 持续运行 snetd 守护进程

### 后续重启

```bash
docker compose -f deploy/docker/docker-compose.yml up -d
```

已绑定的设备会自动跳过绑定步骤。

### 手动绑定（不设环境变量）

```bash
# 1. 先启动容器（不设 SNET_SERVER / SNET_BIND_CODE）
docker compose -f deploy/docker/docker-compose.yml up -d

# 2. 进入容器手动绑定
docker exec -it snetd sh
snetctl -ctl 127.0.0.1:19432 bind --server https://snet.uizhi.eu.org:8090 --code 你的授权码
```

### 管理操作

```bash
# 查看状态
docker exec snetd snetctl -ctl 127.0.0.1:19432 status

# 创建网络
docker exec snetd snetctl -ctl 127.0.0.1:19432 create "my-network"

# 加入网络
docker exec snetd snetctl -ctl 127.0.0.1:19432 join <network-id> --code <invite-code>

# 查看对端
docker exec snetd snetctl -ctl 127.0.0.1:19432 peers <network-id>
```

### Docker Compose 配置说明

| 配置项 | 说明 |
|--------|------|
| `cap_add: NET_ADMIN` | TUN 设备和 WireGuard 必需 |
| `devices: /dev/net/tun` | 内核 TUN 设备 |
| `ports: 8080:8080` | Web 控制台（nginx） |
| `ports: 52100-52163/udp` | WireGuard 数据面（每个网络一个端口） |
| `volumes: snet-data:/data` | 持久化 device.id + daemon.json |
| `sysctls: net.ipv4.ip_forward=1` | IP 转发（容器内子网路由需要） |
| `restart: unless-stopped` | 崩溃/重启自动恢复 |

### 注意事项

- Web GUI 通过 nginx 代理 snetd 控制 API（127.0.0.1:19432），仅容器内部可访问
- WireGuard 端口范围 52100-52163（避开服务端 relay 池 51820-52075）
- 容器内 snetd 以 root 运行（TUN 设备创建需要 root 权限）
- 容器内 nginx 以 nobody 运行，仅提供静态文件和 API 代理

## 11. 群晖 NAS Docker 部署

在群晖 Synology NAS 上使用 Docker 部署 snetd 客户端，加入虚拟网络。

### 前置条件

1. 群晖已安装 **Container Manager**（DSM 7.2+）或 **Docker**（DSM 7.1 及以下）
2. 已开启 SSH 访问（控制面板 → 终端机和 SNMP → 启用 SSH）
3. 已获取服务器设备授权码（在 VPS Web UI「设备」页生成）

### 方式一：通过 Docker Compose 部署（推荐）

#### 步骤 1：上传部署文件

1. 在群晖 **File Station** 中创建文件夹 `/docker/snet/`
2. 上传以下文件到 `/docker/snet/`：
   - `docker-compose.yml`（见下方模板）
   - `snet-server.tar`（预构建镜像，从本地 Mac 桌面上传）

#### 步骤 2：创建 docker-compose.yml

在 `/docker/snet/` 目录下创建 `docker-compose.yml`：

```yaml
version: '3.8'

services:
  snet-client:
    image: snet-server:latest
    container_name: snet-client
    restart: unless-stopped
    network_mode: host
    cap_add:
      - NET_ADMIN
    devices:
      - /dev/net/tun:/dev/net/tun
    volumes:
      - ./data:/data
    environment:
      - SNET_SERVER=${SNET_SERVER:-}
      - SNET_BIND_CODE=${SNET_BIND_CODE:-}
```

#### 步骤 3：导入镜像

1. 打开 **Container Manager → 镜像**
2. 点击 **导入 → 从文件添加**
3. 选择 `/docker/snet/snet-server.tar`
4. 等待导入完成

#### 步骤 4：启动容器

**方式 A：自动绑定（推荐首次使用）**

编辑 `/docker/snet/.env` 文件（如没有则创建）：

```bash
SNET_SERVER=https://snet.uizhi.eu.org:8090
SNET_BIND_CODE=你的设备授权码
```

然后在 **Container Manager → 项目** 中：
1. 点击 **新增**
2. 项目名称：`snet`
3. 路径：选择 `/docker/snet/`
4. 来源：选择 `docker-compose.yml`
5. 点击 **下一步 → 完成**

**方式 B：手动绑定**

不创建 `.env` 文件，直接启动容器后通过 Web UI 绑定：

1. 启动容器后访问 `http://群晖IP:8080`
2. 在 Web UI 中点击 **链接服务器**
3. 输入服务器地址和设备授权码

#### 步骤 5：加入网络

容器启动并绑定服务器后：

1. 访问 `http://群晖IP:8080` 打开 Web UI
2. 点击 **加入网络**
3. 粘贴邀请链接（格式：`snet://join?nid=...&code=...`）
4. 点击 **加入**

### 方式二：通过群晖 GUI 部署

#### 步骤 1：导入镜像

1. 打开 **Container Manager → 镜像 → 导入**
2. 选择 `snet-server.tar` 文件

#### 步骤 2：创建容器

1. 打开 **Container Manager → 容器 → 新增**
2. **常规设置**：
   - 容器名称：`snet-client`
   - 启用资源限制：关闭
3. **端口设置**：
   | 宿主机 | 容器 | 类型 |
   |--------|------|------|
   | 8080 | 8080 | TCP |
   | 8443 | 8443 | TCP |
   | 52100 | 52100 | UDP |
   | 52101 | 52101 | UDP |
   | 52102 | 52102 | UDP |
4. **存储空间**：
   | 宿主机路径 | 容器路径 | 权限 |
   |-----------|---------|------|
   | `/docker/snet/data` | `/data` | 读写 |
5. **环境变量**（可选，用于自动绑定）：
   | 变量 | 值 |
   |------|-----|
   | `SNET_SERVER` | `https://snet.uizhi.eu.org:8090` |
   | `SNET_BIND_CODE` | `你的设备授权码` |
6. **网络**：选择 **host** 模式
7. **高级设置 → 权限**：
   - 启用 **特权模式**（或添加 `/dev/net/tun` 设备）
8. 点击 **下一步 → 完成**

#### 步骤 3：绑定和加入网络

参考方式一的步骤 5。

### 群晖 NAS 特殊配置

#### 开启 TUN 设备支持

群晖默认可能禁用 TUN 设备，需要 SSH 登录后执行：

```bash
# 检查 TUN 设备
ls -la /dev/net/tun

# 如果不存在，创建 TUN 设备
sudo mkdir -p /dev/net
sudo mknod /dev/net/tun c 10 200
sudo chmod 600 /dev/net/tun

# 开机自动创建（添加到 /etc/rc.local）
echo 'mkdir -p /dev/net && mknod /dev/net/tun c 10 200 && chmod 600 /dev/net/tun' | sudo tee -a /etc/rc.local
```

#### 防火墙配置

如果群晖启用了防火墙，需要放行以下端口：

- **8080/tcp**：Web UI（HTTP）
- **8443/tcp**：Web UI（HTTPS）
- **52100-52163/udp**：WireGuard 数据面（避开服务端 relay 池 51820-52075）

### 验证部署

1. **检查容器状态**：
   ```bash
   docker ps | grep snet-client
   ```

2. **查看日志**：
   ```bash
   docker logs snet-client
   ```

3. **检查网络连接**：
   ```bash
   docker exec snet-client snetctl -ctl 127.0.0.1:19432 status
   ```

4. **访问 Web UI**：
   - 浏览器打开 `http://群晖IP:8080`
   - 确认显示已绑定服务器和加入的网络

### 故障排查

**问题：容器启动失败，提示 TUN 设备不存在**
```bash
# 解决方案：手动创建 TUN 设备
sudo mkdir -p /dev/net
sudo mknod /dev/net/tun c 10 200
sudo chmod 600 /dev/net/tun
```

**问题：Web UI 无法访问**
```bash
# 检查端口是否被占用
netstat -tlnp | grep 8080
# 检查容器日志
docker logs snet-client
```

**问题：无法绑定服务器**
```bash
# 检查网络连通性
docker exec snet-client curl -sk https://snet.uizhi.eu.org:8090/healthz
# 检查授权码是否有效
docker exec snet-client snetctl -ctl 127.0.0.1:19432 status
```

### 升级容器

```bash
# 1. 导入新镜像
docker load -i snet-server.tar

# 2. 重启容器
docker restart snet-client
# 或使用 docker compose
cd /docker/snet && docker compose up -d
```
