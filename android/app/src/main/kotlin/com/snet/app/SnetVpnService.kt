package com.snet.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.ParcelFileDescriptor
import android.util.Log
import java.util.concurrent.atomic.AtomicBoolean

class SnetVpnService : VpnService() {
    companion object {
        private const val TAG = "SnetVpn"
        private const val NOTIFICATION_ID = 1
        private const val CHANNEL_ID = "snet_vpn"
        private const val KEEPALIVE_INTERVAL = 30_000L
        private const val STATUS_CHECK_INTERVAL = 5_000L

        var instance: SnetVpnService? = null
            private set

        var tunFd: ParcelFileDescriptor? = null
            private set

        var isRunning = false
            private set

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
    private var vpnStartTime = 0L

    private val keepAliveRunnable = object : Runnable {
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
                stopVpn()
                return START_NOT_STICKY
            }
            "START" -> {
                if (isRunning) {
                    Log.d(TAG, "VPN already running")
                    return START_STICKY
                }
                startForeground(NOTIFICATION_ID, buildNotification("正在连接..."))
                startVpn()
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

        try {
            val builder = Builder()
                .setSession("SNET")
                .setMtu(1420)
                .addAddress("10.0.0.2", 32)
                .addRoute("0.0.0.0", 0)
                .setBlocking(true)

            // Exclude local subnets so LAN access and DNS resolution to local
            // servers continue to work. These ranges are excluded from the VPN
            // tunnel and route through the physical interface instead.
            val localSubnets = listOf(
                "192.168.0.0" to 16,
                "10.0.0.0" to 8,
                "172.16.0.0" to 12,
                "169.254.0.0" to 16,  // link-local
                "127.0.0.0" to 8      // loopback
            )
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                for ((addr, prefix) in localSubnets) {
                    builder.excludeRoute(java.net.InetNetwork(addr, prefix))
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

            handler.postDelayed(keepAliveRunnable, KEEPALIVE_INTERVAL)
            handler.postDelayed(statusCheckRunnable, STATUS_CHECK_INTERVAL)

            Log.d(TAG, "VPN established, TUN fd=${pfd.fd}")

        } catch (e: Exception) {
            Log.e(TAG, "Failed to start VPN", e)
            notifyStatus("error:${e.message}")
            stopSelf()
        }
    }

    private fun stopVpn() {
        isRunning = false
        started.set(false)
        notifyStatus("disconnected")

        handler.removeCallbacks(keepAliveRunnable)
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
