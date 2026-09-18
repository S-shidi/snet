package com.snet.app

import android.util.Log
import android.webkit.JavascriptInterface
import java.net.InetAddress

/**
 * JavaScript interface for the WebView-based UI.
 * Exposes Go/SnetBridge functions to JavaScript as window.WebBridge.*
 */
class WebBridge(private val activity: MainActivity) {
    /** Serial executor for toggle-class operations (rejoin/leave/remove/…).
     *  These do heavy Go/JNI work that must not block the Android UI thread
     *  (which is where @JavascriptInterface methods run). Serializing keeps
     *  ordering guarantees (e.g. leave after rejoin) without UI stalls. */
    private val bgExecutor = java.util.concurrent.Executors.newSingleThreadExecutor()

    /** Pushes a refresh to the WebView UI after a background toggle op finishes,
     *  so the optimistic switch state reconciles without waiting the 5s poll. */
    private fun notifyUi() {
        try {
            activity.runOnUiThread {
                activity.refreshWebView()
            }
        } catch (e: Exception) {
            Log.w(TAG, "notifyUi failed", e)
        }
    }

    /** Marks a network as desired for auto-connect on process restart / boot.
     *  Consumed by MainActivity.maybeAutoConnect() and BootReceiver. */
    private fun setAutoConnect(enabled: Boolean) {
        try {
            val ed = activity.getSharedPreferences("snet_prefs", android.content.Context.MODE_PRIVATE).edit()
            ed.putBoolean("auto_connect", enabled)
            ed.apply()
        } catch (e: Exception) {
            Log.w(TAG, "setAutoConnect failed", e)
        }
    }

    /** True when the Go daemon still reports at least one active joined network. */
    private fun hasActiveNetwork(): Boolean {
        return try {
            val raw = SnetBridge.statusRaw()
            val obj = org.json.JSONObject(raw)
            val arr = obj.optJSONArray("networks") ?: return false
            for (i in 0 until arr.length()) {
                if (arr.getJSONObject(i).optBoolean("active", false)) return true
            }
            false
        } catch (e: Exception) {
            false
        }
    }

    /** Async version of hasActiveNetwork() to avoid blocking UI thread */
    private fun hasActiveNetworkAsync(callback: (Boolean) -> Unit) {
        Thread {
            try {
                val hasActive = hasActiveNetwork()
                activity.runOnUiThread { callback(hasActive) }
            } catch (e: Exception) {
                Log.e(TAG, "hasActiveNetworkAsync failed", e)
                activity.runOnUiThread { callback(false) }
            }
        }.start()
    }

    private companion object {
        const val TAG = "WebBridge"

        fun rememberServer(server: String) {
            if (server.isEmpty()) return
            SnetVpnService.serverAddr = server
            // Persist so SnetVpnService can read it even after process restart.
            saveServerPrefs(server, null)
            // Resolve while the physical network is still reachable (VPN not
            // established yet), so route exclusion can use concrete IPs later.
            val hosts = listOf(server, server.replaceFirst("^[a-z]+://".toRegex(), ""))
                .mapNotNull { s ->
                    if (s.contains("://")) android.net.Uri.parse(s).host
                    else s.substringBeforeLast(":")
                }.filter { it.isNotEmpty() && it != "localhost" && it != "127.0.0.1" }
            val ips = mutableListOf<String>()
            for (h in hosts) {
                try {
                    InetAddress.getAllByName(h).forEach { a ->
                        if (a.hostAddress != null && a !is java.net.Inet6Address)
                            if (!ips.contains(a.hostAddress)) ips.add(a.hostAddress)
                    }
                } catch (e: Exception) {
                    Log.d(TAG, "rememberServer resolve $h failed: ${e.message}")
                }
                if (ips.isNotEmpty()) break
            }
            SnetVpnService.serverIps = ips
            // Persist resolved IPs for the same reason.
            saveServerPrefs(server, ips)
            Log.d(TAG, "rememberServer: addr=$server ips=$ips")
        }

        private fun saveServerPrefs(server: String, ips: List<String>?) {
            try {
                val ctx = SnetBridge.app
                    ?: SnetVpnService.instance
                    ?: return
                val ed = ctx.getSharedPreferences("snet_prefs", android.content.Context.MODE_PRIVATE)
                    .edit().putString("server_addr", server)
                if (ips != null) ed.putStringSet("server_ips", ips.toSet())
                ed.apply()
            } catch (_: Exception) {}
        }
    }

    @JavascriptInterface
    fun status(): String {
        // Run SnetBridge.statusRaw() in a background thread with timeout
        // to avoid blocking the WebView's JavaScript thread
        return try {
            val future = java.util.concurrent.CompletableFuture.supplyAsync {
                try {
                    SnetBridge.statusRaw() ?: "{}"
                } catch (e: Exception) {
                    Log.e(TAG, "statusRaw failed", e)
                    "{}"
                }
            }
            
            // Wait with timeout (max 2000ms for more reliable status)
            val raw = future.get(2000, java.util.concurrent.TimeUnit.MILLISECONDS)
            val obj = org.json.JSONObject(raw)
            obj.put("started", SnetBridge.isStarted())
            obj.put("vpnRunning", SnetVpnService.isRunning)
            obj.toString()
        } catch (e: java.util.concurrent.TimeoutException) {
            Log.w(TAG, "status() timeout, returning basic status")
            // Return a minimal status instead of empty to avoid UI flickering
            val obj = org.json.JSONObject()
            obj.put("started", SnetBridge.isStarted())
            obj.put("vpnRunning", SnetVpnService.isRunning)
            obj.put("networks", org.json.JSONArray())
            obj.toString()
        } catch (e: Exception) {
            Log.e(TAG, "status failed", e)
            "{}"
        }
    }

    @JavascriptInterface
    fun create(params: String): String {
        return try {
            val p = org.json.JSONObject(params)
            val name = p.optString("name", "")
            val subnet = p.optString("subnet", "")
            val approvalRequired = p.optBoolean("approvalRequired", false)
            val server = p.optString("server", "")
            val port = p.optInt("port", 51820)
            val ca = p.optString("ca", "")
            val description = p.optString("description", "")
            val tags = p.optJSONArray("tags")?.toString() ?: "[]"
            val visibility = p.optString("visibility", "")
            rememberServer(server)
            SnetBridge.createNetwork(name, subnet, approvalRequired, server, port, description, tags, visibility)
        } catch (e: Exception) {
            Log.e(TAG, "create failed", e)
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun join(params: String): String {
        return try {
            val p = org.json.JSONObject(params)
            var link = p.optString("link", "")
            val server = p.optString("server", "")
            val port = p.optInt("port", 51820)
            val ca = p.optString("ca", "")
            rememberServer(server)
            // Extract nid and code from link or from direct params
            var nid = p.optString("nid", "")
            var code = p.optString("code", "")
            if (link.isNotEmpty() && (nid.isEmpty() || code.isEmpty())) {
                // Parse snet://join?nid=...&code=... URL
                val query = if (link.contains("?")) link.substringAfter("?") else link
                for (param in query.split("&")) {
                    val kv = param.split("=", limit = 2)
                    if (kv.size == 2) {
                        when (kv[0]) {
                            "nid" -> if (nid.isEmpty()) nid = kv[1]
                            "code" -> if (code.isEmpty()) code = kv[1]
                        }
                    }
                }
            }
            Log.d(TAG, "join nid=$nid server=$server port=$port")
            // Ensure VPN service (and thus the TUN fd) is running before
            // joining, routing through VPN permission so establish() can obtain
            // the fd (first time shows the system consent dialog).
            if (!SnetVpnService.isRunning) {
                Log.d(TAG, "VPN not running, requesting before join")
                activity.requestVpnPermission()
            }
            SnetBridge.joinNetwork(nid, code, server, port)
            setAutoConnect(true)
            """{"ok":true}"""
        } catch (e: Exception) {
            Log.e(TAG, "join failed", e)
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun bind(params: String): String {
        return try {
            val p = org.json.JSONObject(params)
            val server = p.optString("server", "")
            val ca = p.optString("ca", "")
            val code = p.optString("code", "")
            rememberServer(server)
            SnetBridge.bind(server, ca, code)
            setAutoConnect(true)
            """{"ok":true}"""
        } catch (e: Exception) {
            Log.e(TAG, "bind failed", e)
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun rejoin(nid: String): String {
        // Return immediately for responsive UI; work happens in background.
        // The shared UI finishes the interaction optimistically and its
        // periodic refresh() reflects the real outcome.
        bgExecutor.execute {
            try {
                // Request VPN permission if needed (non-blocking)
                if (!SnetVpnService.isRunning) {
                    Log.d(TAG, "VPN not running, requesting before rejoin")
                    activity.requestVpnPermission()
                    // Don't wait - the VPN will start asynchronously
                    // The network will be rejoined when VPN is ready
                    return@execute
                }
                
                // Quick check without busy waiting
                if (SnetBridge.isStarted()) {
                    SnetBridge.rejoin(nid)
                    setAutoConnect(true)
                    notifyUi()
                } else {
                    Log.w(TAG, "rejoin($nid) core not ready yet")
                    notifyProgress("请稍后重试")
                }
            } catch (e: Exception) {
                Log.e(TAG, "rejoin($nid) bg failed", e)
                notifyProgress("连接失败")
            }
        }
        return """{"ok":true}"""
    }

    /** Optimized wait with shorter timeout and early return. */
    private fun waitForCoreReadyOptimized(): Boolean {
        val deadline = System.currentTimeMillis() + 5_000 // Reduced from 15s to 5s
        var lastStatus = ""
        var lastNotifyTime = 0L
        while (System.currentTimeMillis() < deadline) {
            if (SnetVpnService.isRunning && SnetBridge.isStarted()) return true
            // Report progress every 500ms to UI
            val now = System.currentTimeMillis()
            if (now - lastNotifyTime > 500) {
                val status = when {
                    !SnetVpnService.isRunning -> "等待 VPN 启动..."
                    !SnetBridge.isStarted() -> "等待守护进程就绪..."
                    else -> "就绪"
                }
                if (status != lastStatus) {
                    lastStatus = status
                    lastNotifyTime = now
                    Log.d(TAG, "waitForCoreReady: $status")
                    notifyProgress(status)
                }
            }
            try {
                Thread.sleep(100)
            } catch (e: InterruptedException) {
                Thread.currentThread().interrupt()
                return false
            }
        }
        Log.w(TAG, "waitForCoreReadyOptimized timed out")
        return false
    }

    private fun runOnUiThread(action: () -> Unit) {
        activity.runOnUiThread(action)
    }

    private fun notifyProgress(status: String) {
        activity.runOnUiThread {
            activity.evaluateJs("if (typeof snetProgress === 'function') snetProgress('$status');")
        }
    }

    @JavascriptInterface
    fun leave(nid: String): String {
        bgExecutor.execute {
            try {
                SnetBridge.leaveNetwork(nid)
                
                // Check active networks asynchronously
                Thread {
                    val hasActive = hasActiveNetwork()
                    setAutoConnect(hasActive)
                    checkIfAllLeftStopVpn()
                    
                    // Notify UI to refresh
                    activity.runOnUiThread {
                        activity.refreshWebView()
                    }
                }.start()
            } catch (e: Exception) {
                Log.e(TAG, "leave($nid) bg failed", e)
                notifyProgress("断开失败")
            }
        }
        return """{"ok":true}"""
    }

    @JavascriptInterface
    fun remove(nid: String): String {
        bgExecutor.execute {
            try {
                SnetBridge.removeNetwork(nid)
                
                // Check active networks asynchronously
                Thread {
                    val hasActive = hasActiveNetwork()
                    setAutoConnect(hasActive)
                    checkIfAllLeftStopVpn()
                    
                    // Notify UI to refresh
                    activity.runOnUiThread {
                        activity.refreshWebView()
                    }
                }.start()
            } catch (e: Exception) {
                Log.e(TAG, "remove($nid) bg failed", e)
                notifyProgress("移除失败")
            }
        }
        return """{"ok":true}"""
    }

    @JavascriptInterface
    fun deleteNet(nid: String): String {
        bgExecutor.execute {
            try {
                SnetBridge.deleteNetwork(nid)
                notifyUi()
            } catch (e: Exception) {
                Log.e(TAG, "deleteNet($nid) bg failed", e)
                notifyUi()
            }
        }
        return """{"ok":true}"""
    }

    @JavascriptInterface
    fun netinfo(nid: String): String {
        return try {
            SnetBridge.info(nid)
        } catch (e: Exception) {
            """{"error":"${e.message}"}"""
        }
    }

    @JavascriptInterface
    fun peers(nid: String): String {
        return try {
            SnetBridge.peers(nid)
        } catch (e: Exception) {
            """{"peers":[]}"""
        }
    }

    @JavascriptInterface
    fun updateSettings(params: String): String {
        return try {
            val p = org.json.JSONObject(params)
            val nid = p.optString("nid", "")
            val name = p.optString("name", "")
            val subnet = p.optString("subnet", "")
            val approvalRequired = p.optBoolean("approvalRequired", false)
            val description = p.optString("description", "")
            val tags = p.optJSONArray("tags")?.toString() ?: "[]"
            val visibility = p.optString("visibility", "")
            SnetBridge.updateSettings(nid, name, subnet, approvalRequired, description, tags, visibility)
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun setRole(params: String): String {
        return try {
            val p = org.json.JSONObject(params)
            val nid = p.optString("nid", "")
            val nodeId = p.optString("nodeId", "")
            val role = p.optString("role", "")
            SnetBridge.setNodeRole(nid, nodeId, role)
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun updateSubnets(params: String): String {
        return try {
            val p = org.json.JSONObject(params)
            val nid = p.optString("nid", "")
            val subnets = p.optString("subnets", "[]")
            SnetBridge.updateSubnets(nid, subnets)
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun kick(params: String): String {
        return try {
            val p = org.json.JSONObject(params)
            val nid = p.optString("nid", "")
            val nodeId = p.optString("nodeId", "")
            SnetBridge.kick(nid, nodeId)
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun approve(params: String): String {
        return try {
            val p = org.json.JSONObject(params)
            val nid = p.optString("nid", "")
            val pendingId = p.optString("pendingId", "")
            SnetBridge.approvePending(nid, pendingId)
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun deny(params: String): String {
        return try {
            val p = org.json.JSONObject(params)
            val nid = p.optString("nid", "")
            val pendingId = p.optString("pendingId", "")
            SnetBridge.denyPending(nid, pendingId)
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun cancelPending(pendingId: String): String {
        return try {
            SnetBridge.cancelPending(pendingId)
            """{"ok":true}"""
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun resetCode(nid: String): String {
        return try {
            SnetBridge.resetCode(nid)
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun detectLocalSubnets(): String {
        return try {
            SnetBridge.detectLocalSubnets()
        } catch (e: Exception) {
            "[]"
        }
    }

    @JavascriptInterface
    fun ensureDaemon(): String {
        // On Android, the VPN service manages the daemon
        return try {
            val intent = android.content.Intent(activity, SnetVpnService::class.java)
            intent.action = "START"
            activity.startForegroundService(intent)
            """{"ok":true}"""
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun scanQRCode(): String {
        // Starts a native QR scan; the decoded text is delivered back to JS
        // asynchronously via window.__snetScanResolve (see MainActivity.safeScanResolve).
        return try {
            activity.startScanQR()
            """{"ok":true}"""
        } catch (e: Exception) {
            Log.e(TAG, "scanQRCode failed", e)
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun startVpn(): String {
        return try {
            // Route through VPN permission so VpnService.establish() can
            // obtain a TUN fd (first time shows the system consent dialog).
            activity.requestVpnPermission()
            """{"ok":true}"""
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun stopVpn(): String {
        return try {
            val intent = android.content.Intent(activity, SnetVpnService::class.java)
            intent.action = "STOP"
            activity.startService(intent)
            """{"ok":true}"""
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    /** Stops the VpnService once the last member network has been left, so the
     *  status-bar VPN indicator disappears. Reads active network count from the
     *  daemon status. */
    private fun checkIfAllLeftStopVpn() {
        if (!SnetVpnService.isRunning) return
        try {
            val raw = SnetBridge.statusRaw()
            val obj = org.json.JSONObject(raw)
            val nets = obj.optJSONArray("networks")
            var active = 0
            if (nets != null) {
                for (i in 0 until nets.length()) {
                    val net = nets.getJSONObject(i)
                    if (net.optBoolean("active", false)) active++
                }
            }
            if (active == 0) {
                Log.d(TAG, "No active networks left, stopping VPN")
                val intent = android.content.Intent(activity, SnetVpnService::class.java)
                intent.action = "STOP"
                activity.startService(intent)
            }
        } catch (e: Exception) {
            Log.w(TAG, "checkIfAllLeftStopVpn failed", e)
        }
    }

    @JavascriptInterface
    fun getDeviceId(): String {
        return try {
            SnetBridge.getDeviceID(activity) ?: ""
        } catch (e: Exception) {
            ""
        }
    }
}
