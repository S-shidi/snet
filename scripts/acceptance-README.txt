Snet 真机验收（当前架构：VPS 协调 + HTTPS + UDP 中继）
========================================================

架构
  - 协调服务器：VPS 66.187.6.46，域名 snet.uizhi.eu.org:8090（HTTPS，CA 固定）
  - 数据面：WireGuard。优先 NAT 打洞直连，打不通经服务器 UDP 中继（51820..51883）
  - 典型拓扑：Mac（家宽）+ 手机（5G），两侧对称 NAT → 走中继

一、本地回环 e2e（无需外部服务器，验证协调+数据面基线）
  1. 构建：go build -o /tmp/snet-build/server ./cmd/server
            go build -o /tmp/snet-build/snetd ./cmd/client/snetd
            go build -o /tmp/snet-build/snetctl ./cmd/client/snetctl
  2. 运行（需 root，创建 utun）：
        sudo scripts/e2e.sh
    期望输出 E2E RESULT: PASS（握手非 0、双向有流量）

二、真机验收（Mac ↔ 手机，经 VPS）
  前置
    - VPS 上 snet-server 已部署（HTTPS + 中继），见 deploy/README.md
    - 本机 snetd 已配置：serverAddr=https://snet.uizhi.eu.org:8090，
      serverCaPath=/usr/local/snet/certs/server.pem
    - 手机已导入 deploy/phone-android.conf（Endpoint=snet.uizhi.eu.org:51821，
      PersistentKeepalive=10），且 WireGuard 已被设为不受电池/后台限制

  建网/入网两种方式
    A. 桌面 GUI：创建/加入即连到 VPS（默认 server https://snet.uizhi.eu.org:8090）。
       注意：GUI「创建网络」会在服务器新建一个网络并把本机切过去——手机须重新
       加入同一网络，否则两端在不同网络互相不可见（2026-08-13 已踩坑）。
    B. 命令行（当前实况，网络 B74P7ZZR / 中继端口 51821）：
        1. 启动/确认本机 daemon：
             sudo launchctl load /Library/LaunchDaemons/com.snet.daemon.plist
        2. 建网（Mac）：snetctl --ctl http://127.0.0.1:19432 create \
             --server https://snet.uizhi.eu.org:8090 --ca-path /usr/local/snet/certs/server.pem --port 51820
           记录 link（snet://join?nid=...&code=...）与 Mac 虚拟 IP（10.88.0.1）
        3. 手机（纯 WireGuard 客户端，无 snet 客户端）用配对码走公开 join 接口
           注册为普通节点（配对码可复用 5 次，无需 admin 令牌）：
             curl -sk -X POST https://snet.uizhi.eu.org:8090/api/v1/networks/<nid>/join \
               -d '{"code":"<配对码>","publicKey":"<手机公钥>"}'
           返回的 ip 即手机虚拟 IP（首台设备 10.88.0.1，第二台 10.88.0.2）
        4. 查该网络中继端口：用本机 daemon token 拉 peers，
           peer.endpoint 即 relayHost:中继端口（B74P7ZZR=51821）：
             curl -sk https://snet.uizhi.eu.org:8090/api/v1/networks/<nid>/peers \
               -H "Authorization: Bearer <daemon.json 里的 token>"
        5. 把手机配置 Endpoint 端口改成中继端口，qrencode 重出二维码导入

  验收（Mac 侧）
    - snetctl --ctl http://127.0.0.1:19432 status
      期望 peerStats 中 LastHandshakeSec 非 0、TxBytes/RxBytes 增长
    - ping 手机虚拟 IP（10.88.0.2）：
      ping -c 20 -i 1 10.88.0.2
      期望丢包 <5%（手机处于前台/屏幕常亮时；Doze 挂起期间丢包属手机端问题，
      先检查 WireGuard 电池优化是否已设无限制）

  实况记录（2026-08-13，B74P7ZZR）
    - Mac 10.88.0.1/utun4，手机 10.88.0.2（join 节点 MFGV63XC），中继 51821
    - status：LastHandshakeSec 持续刷新，Tx/Rx 增长 → 隧道可用
    - ping -c 20 -i 1：15/20 收到，丢包 25%，RTT 660~825ms（5G CGNAT 轮换公网 IP
      + 中国 5G→美国中继路径；非配置问题）

  失败排查
    - status 里 peers 为空：确认 daemon.json serverAddr 域名可达
      （curl -sk https://snet.uizhi.eu.org:8090/healthz）
    - 握手超时：服务器日志确认该网络的中继端口（relayHost:端口）已下发给 peer；
      确认两端入站 UDP 未被防火墙拦截
    - IPC error ParseAddr：客户端旧版本不支持域名端点，升级到含 resolveEndpoint 的版本
