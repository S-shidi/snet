package com.snet.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.IpPrefix
import android.net.VpnService
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.ParcelFileDescriptor
import android.util.Log
import java.net.InetAddress
import java.util.concurrent.atomic.AtomicBoolean

class SnetVpnService : VpnService() {
    companion object {
        private const val TAG = "SnetVpn"
        private const val NOTIFICATION_ID = 1
        private const val CHANNEL_ID = "snet_vpn"
        private const val KEEPALIVE_INTERVAL = 30_000L
        private const val STATUS_CHECK_INTERVAL = 5_000L

        /** Static host->IP fallback for the coordination server. Map a configured
         *  host to its (stable) public IP so we can exclude the server's route
         *  even when the device's DNS resolver is unhealthy or fails during VPN
         *  establishment. Value is the physical-network-proven server IP. */
        private val knownServerHosts = mapOf(
            "snet.uizhi.eu.org" to "66.187.6.46",
        )

        @Volatile
        var instance: SnetVpnService? = null
            private set

        @Volatile
        var tunFd: ParcelFileDescriptor? = null
            private set

        @Volatile
        var isRunning = false
            private set

        /** Server address set by WebBridge before VPN starts; used for route exclusion. */
        @Volatile
        var serverAddr: String = ""

        /** Daemon config directory (set by SnetBridge.init); serverAddr can also
         *  be recovered from configDir/daemon.json after process restart. */
        @Volatile
        var configDir: String = ""

        /** Pre-resolved server IPv4 addresses (set by WebBridge while the physical
         *  network is still reachable), used as fallback when DNS fails during
         *  VPN establishment. */
        @Volatile
        var serverIps: List<String> = emptyList()

        private val statusCallbacks = mutableListOf<(String) -> Unit>()

        fun addStatusCallback(cb: (String) -> Unit) {
            synchronized(statusCallbacks) { statusCallbacks.add(cb) }
        }

        fun removeStatusCallback(cb: (String) -> Unit) {
            synchronized(statusCallbacks) { statusCallbacks.remove(cb) }
        }

        private fun notifyStatus(status: String) {
            synchronized(statusCallbacks) {
                statusCallbacks.forEach { it(status) }
            }
        }
    }

    private val handler = Handler(Looper.getMainLooper())
    private val started = AtomicBoolean(false)

    /** Set on the start worker thread, read on the main-thread status reporter. */
    @Volatile
    private var vpnStartTime = 0L

    private val tunHealthCheck = object : Runnable {
        override fun run() {
            if (!isRunning) return
            protectKeepAlive()
            handler.postDelayed(this, KEEPALIVE_INTERVAL)
        }
    }

    private val statusCheckRunnable = object : Runnable {
        override fun run() {
            if (!isRunning) return
            checkStatus()
            handler.postDelayed(this, STATUS_CHECK_INTERVAL)
        }
    }

    override fun onBind(intent: Intent?): IBinder? {
        return super.onBind(intent)
    }

    override fun onCreate() {
        super.onCreate()
        instance = this
        createNotificationChannel()
        Log.d(TAG, "VPN service created")
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            "STOP" -> {
                // Stop VPN on a background thread to avoid UI freeze
                Thread { stopVpn() }.start()
                return START_NOT_STICKY
            }
            "START" -> {
                if (isRunning) {
                    Log.d(TAG, "VPN already running")
                    return START_STICKY
                }
                startForeground(NOTIFICATION_ID, buildNotification("正在初始化..."))
                // Do the heavy lifting (DNS resolution during exclusion, route
                // setup, Builder.establish) on a worker thread with progress updates.
                Thread { startVpnWithProgress() }.start()
                return START_STICKY
            }
        }
        return START_NOT_STICKY
    }

    private fun startVpn() {
        if (tunFd != null) {
            Log.d(TAG, "TUN already established")
            return
        }

        // Load persisted server address/IPs (written by WebBridge when the user
        // binds/joins a network) so route exclusion survives process restarts.
        try {
            val prefs = getSharedPreferences("snet_prefs", MODE_PRIVATE)
            if (serverAddr.isEmpty()) serverAddr = prefs.getString("server_addr", "") ?: ""
            if (serverAddr.isEmpty() && configDir.isNotEmpty()) {
                // Third source: daemon's persisted config (contains ServerAddr
                // even before the daemon is started / bound this session).
                try {
                    val f = java.io.File(configDir, "daemon.json")
                    if (f.exists()) {
                        val obj = org.json.JSONObject(f.readText())
                        serverAddr = obj.optString("ServerAddr", "")
                        if (serverAddr.isEmpty()) serverAddr = obj.optString("serverAddr", "")
                    }
                } catch (_: Exception) {}
            }
            val savedIps = prefs.getStringSet("server_ips", null)
            if (savedIps != null) {
                val merged = (serverIps + savedIps).toMutableSet()
                serverIps = merged.toList()
            } else if (serverIps.isEmpty() && serverAddr.isNotEmpty()) {
                // No cached IPs — resolve now before entering the tunnel.
                val host = if (serverAddr.contains("://"))
                    android.net.Uri.parse(serverAddr).host ?: ""
                    else serverAddr.substringBeforeLast(":")
                if (host.isNotEmpty() && host != "localhost" && host != "127.0.0.1") {
                    // Prefer resolving on the physical network (the default
                    // network may already be re-routed into the tunnel, which
                    // makes InetAddress.getAllByName fail or hang). Bind to the
                    // active network explicitly — Network.getAllByName routes
                    // the lookup through that network's DNS servers regardless
                    // of the current default network.
                    try {
                        val resolved = mutableListOf<String>()
                        val cm = getSystemService(android.content.Context.CONNECTIVITY_SERVICE) as android.net.ConnectivityManager
                        val active = cm.activeNetwork
                        val addrs = if (active != null) {
                            try { active.getAllByName(host) } catch (_: Exception) { null }
                                ?: InetAddress.getAllByName(host)
                        } else {
                            InetAddress.getAllByName(host)
                        }
                        for (a in addrs) {
                            val aStr = a.hostAddress ?: continue
                            if (!aStr.contains(":") && !resolved.contains(aStr)) resolved.add(aStr)
                        }
                        if (resolved.isNotEmpty()) serverIps = resolved
                    } catch (e: Exception) {
                        Log.w(TAG, "pre-VPN DNS resolve failed for $host: ${e.message}")
                    }
                }
            }
        } catch (e: Exception) {
            Log.w(TAG, "failed to load server prefs: ${e.message}")
        }
        Log.d(TAG, "startVpn: serverAddr=$serverAddr serverIps=$serverIps")

        try {
            // Register this device's virtual mesh IP(s) on the interface.
            // VpnService.Builder.addAddress makes the kernel treat them as
            // local addresses; without this, incoming tunnel packets addressed
            // to e.g. 10.88.1.5 hit a kernel with no local address for them and
            // are dropped (or re-routed back into the tunnel by the 10.0.0.0/8
            // route), so peers can handshake but never exchange traffic.
            val addedAddresses = mutableSetOf("10.0.0.2")
            try {
                val f = java.io.File(configDir, "daemon.json")
                if (f.exists()) {
                    val obj = org.json.JSONObject(f.readText())
                    val nets = obj.optJSONObject("networks")
                    if (nets != null) {
                        val it = nets.keys()
                        while (it.hasNext()) {
                            val key = it.next()
                            val net = nets.optJSONObject(key) ?: continue
                            val ip = net.optString("ip", "")
                            if (ip.isNotEmpty() && net.optBoolean("active", false)) {
                                addedAddresses.add(ip)
                            }
                        }
                    }
                }
            } catch (_: Exception) {}

            val builder = Builder()
                .setSession("SNET")
                .setMtu(1420)
            for (ip in addedAddresses) {
                try {
                    builder.addAddress(ip, 32)
                    Log.d(TAG, "registered address $ip/32 on VPN interface")
                } catch (e: Exception) {
                    Log.w(TAG, "skip addAddress $ip: ${e.message}")
                }
            }
            builder
                // Route ONLY the virtual mesh range through the tunnel. A
                // 0.0.0.0/0 catch-all made every app lose Internet once the
                // tunnel was up (the virtual LAN has no public NAT exit). By
                // routing just the mesh subnet, physical-network traffic (web,
                // IM, mobile data, ...) keeps working and only peer-to-peer
                // virtual-LAN destinations (10.x) go through WireGuard.
                .addRoute("10.0.0.0", 8)
                .setBlocking(true)

            // Exclude LAN routes so local access keeps working. NOTE: we must
            // NOT exclude 10.0.0.0/8 here — the tunnel subnet and this VPN
            // interface's own address (10.0.0.2) live inside that range, so
            // VpnService rejects the exclusion ("Bad address") and the TUN fd
            // is never created. 10.x is the tunneled mesh range and must stay
            // routed through the VPN (see addRoute("10.0.0.0", 8) above).
            val localSubnets = listOf(
                "192.168.0.0" to 16,
                "172.16.0.0" to 12,
                "169.254.0.0" to 16,  // link-local
                "127.0.0.0" to 8      // loopback
            )
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                for ((addr, prefix) in localSubnets) {
                    // Best-effort only: a single incompatible excludeRoute must
                    // never abort VPN establishment (that would leave the TUN
                    // fd un-created and the network stuck on "未就绪"). Skip
                    // exclusions the device rejects.
                    try {
                        builder.excludeRoute(IpPrefix(InetAddress.getByName(addr), prefix))
                    } catch (e: Exception) {
                        Log.w(TAG, "skip excludeRoute $addr/$prefix: ${e.message}")
                    }
                }

                // Exclude the coordination server IP so daemon HTTP API calls
                // (poll peers, set endpoint, probe on ProbePort) can reach the
                // server via the physical network instead of being routed
                // through the tun0 tunnel which has no WireGuard peers yet.
                // probePublicIP uses the same host (only the port differs), so
                // excluding the resolved /32 covers both API and probe traffic.
                //
                // We only use concrete IPv4 addresses — never resolve DNS here,
                // because the lookup races TUN establishment and gets routed
                // into the tunnel itself (chicken-and-egg). Sources of IPs:
                //   1. serverIps — captured while the physical network was up
                //      and cached in prefs by WebBridge.rememberServer.
                //   2. Any IPv4 literal embedded in serverAddr (some setups use
                //      a numeric server address directly).
                //   3. A static host->IP fallback table (see knownServerHosts)
                //      for hosts whose A record is stable, used when the
                //      device's DNS is unhealthy but the physical network is
                //      still reachable to that IP (proven reachable earlier).
                try {
                    if (serverIps.isEmpty()) {
                        val host = if (serverAddr.contains("://"))
                            android.net.Uri.parse(serverAddr).host ?: ""
                            else serverAddr.substringBeforeLast(":")
                        val fallback = knownServerHosts[host] ?: knownServerHosts[host.removePrefix("www.")]
                        if (fallback != null) {
                            Log.d(TAG, "using known fallback IP $fallback for host $host")
                            serverIps = listOf(fallback)
                        }
                    }

                    // Collect candidate IPs: cached serverIps + any IPv4 literal
                    // in serverAddr (covers the numeric-address case).
                    val excluded = mutableSetOf<String>()
                    val ipSource = (serverIps + serverAddr).toMutableList()

                    val ipv4 = Regex("""\b(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(?:\.(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}\b""")
                    for (token in ipSource) {
                        ipv4.findAll(token).forEach { m ->
                            val ip = m.value
                            if (excluded.contains(ip)) return@forEach
                            try {
                                val addr = InetAddress.getByName(ip)
                                builder.excludeRoute(IpPrefix(addr, 32))
                                excluded.add(ip)
                                Log.d(TAG, "excluded server IP $ip from VPN routes")
                            } catch (e: Exception) {
                                Log.w(TAG, "skip excludeRoute server IP $ip: ${e.message}")
                            }
                        }
                    }
                } catch (e: Exception) {
                    Log.w(TAG, "failed to exclude server address: ${e.message}")
                }
            }

            val pfd = builder.establish()
            if (pfd == null) {
                Log.e(TAG, "Failed to establish VPN")
                notifyStatus("error:VPN 权限被拒绝")
                stopSelf()
                return
            }

            tunFd = pfd
            isRunning = true
            started.set(true)
            vpnStartTime = System.currentTimeMillis()

            notifyStatus("connecting")
            updateNotification("VPN 已建立，正在初始化...")

            SnetBridge.onTunFdReady(pfd.fd)

            handler.postDelayed(tunHealthCheck, KEEPALIVE_INTERVAL)
            handler.postDelayed(statusCheckRunnable, STATUS_CHECK_INTERVAL)

            Log.d(TAG, "VPN established, TUN fd=${pfd.fd}")

        } catch (e: Exception) {
            Log.e(TAG, "Failed to start VPN", e)
            notifyStatus("error:${e.message}")
            stopSelf()
        }
    }

    private fun startVpnWithProgress() {
        try {
            // Stage 1: DNS resolution (can take 5-10s on slow networks)
            updateNotification("正在解析服务器地址...")
            notifyStatus("resolving")

            // Stage 2: Load config
            updateNotification("正在加载配置...")
            Thread.sleep(100) // Small delay to show progress

            // Stage 3: Establish VPN
            updateNotification("正在建立 VPN 连接...")

            startVpn()

        } catch (e: Exception) {
            Log.e(TAG, "startVpnWithProgress failed", e)
            notifyStatus("error:${e.message}")
            updateNotification("VPN 建立失败")
        }
    }

    private fun stopVpn() {
        isRunning = false
        started.set(false)
        notifyStatus("disconnected")

        handler.removeCallbacks(tunHealthCheck)
        handler.removeCallbacks(statusCheckRunnable)

        tunFd?.close()
        tunFd = null
        SnetBridge.stop()

        instance = null
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
        Log.d(TAG, "VPN service stopped")
    }

    private fun protectKeepAlive() {
        try {
            val fd = tunFd ?: return
            if (fd.fd < 0) {
                Log.w(TAG, "TUN fd invalid, stopping")
                stopVpn()
                return
            }
        } catch (e: Exception) {
            Log.e(TAG, "Keepalive check failed", e)
        }
    }

    private fun checkStatus() {
        if (!isRunning) return

        val uptime = (System.currentTimeMillis() - vpnStartTime) / 1000
        val hours = uptime / 3600
        val minutes = (uptime % 3600) / 60
        val secs = uptime % 60

        val timeStr = if (hours > 0) {
            "${hours}h${minutes}m"
        } else if (minutes > 0) {
            "${minutes}m${secs}s"
        } else {
            "${secs}s"
        }

        updateNotification("已连接 | 运行 $timeStr")
    }

    override fun onDestroy() {
        stopVpn()
        super.onDestroy()
    }

    override fun onRevoke() {
        stopVpn()
        super.onRevoke()
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "SNET VPN",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "SNET VPN 连接状态"
                setShowBadge(false)
            }
            val manager = getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(channel)
        }
    }

    private fun buildNotification(text: String): Notification {
        val pendingIntent = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java).apply {
                flags = Intent.FLAG_ACTIVITY_SINGLE_TOP
            },
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )

        val stopIntent = PendingIntent.getService(
            this, 1,
            Intent(this, SnetVpnService::class.java).apply { action = "STOP" },
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )

        val builder = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Notification.Builder(this, CHANNEL_ID)
        } else {
            @Suppress("DEPRECATION")
            Notification.Builder(this)
        }

        return builder
            .setContentTitle("SNET")
            .setContentText(text)
            .setSmallIcon(android.R.drawable.ic_menu_manage)
            .setContentIntent(pendingIntent)
            .addAction(
                Notification.Action.Builder(
                    null, "断开", stopIntent
                ).build()
            )
            .setOngoing(true)
            .setPriority(Notification.PRIORITY_LOW)
            .build()
    }

    private fun updateNotification(text: String) {
        try {
            val manager = getSystemService(NotificationManager::class.java)
            manager.notify(NOTIFICATION_ID, buildNotification(text))
        } catch (e: Exception) {
            Log.e(TAG, "Failed to update notification", e)
        }
    }
}
