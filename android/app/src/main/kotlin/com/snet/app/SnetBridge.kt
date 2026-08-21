package com.snet.app

import android.util.Log
import snetbind.SnetCore
import java.util.concurrent.atomic.AtomicBoolean

object SnetBridge {
    private const val TAG = "SnetBridge"
    private var core: SnetCore? = null
    private var configDir: String = ""
    private val started = AtomicBoolean(false)

    fun init(dir: String) {
        configDir = dir
        try {
            core = SnetCore("$dir/daemon.json")
            core?.setDeviceIDFile("$dir/device.key")
            Log.e(TAG, "SnetCore initialized OK, configDir=$dir")
        } catch (e: UnsatisfiedLinkError) {
            Log.e(TAG, "FATAL: Native library not loaded", e)
        } catch (e: Exception) {
            Log.e(TAG, "FATAL: SnetCore init failed", e)
        }
    }

    fun start(serverAddr: String, serverCA: String) {
        val c = core ?: run { Log.e(TAG, "Core not initialized"); return }
        if (started.get()) return
        val fd = SnetVpnService.tunFd?.fd ?: run { Log.e(TAG, "TUN fd not ready"); return }
        try {
            c.start(fd.toLong(), "$configDir/device.key", serverAddr, serverCA)
            started.set(true)
        } catch (e: Exception) {
            Log.e(TAG, "Failed to start SnetCore", e)
        }
    }

    fun onTunFdReady(fd: Int) {
        if (!started.get()) {
            val c = core ?: return
            try {
                c.start(fd.toLong(), "$configDir/device.key", "", "")
                started.set(true)
            } catch (e: Exception) {
                Log.e(TAG, "Failed to start SnetCore on TUN ready", e)
            }
        }
    }

    fun stop() {
        if (started.compareAndSet(true, false)) core?.stop()
    }

    fun isStarted(): Boolean = started.get()

    fun bind(serverAddr: String, serverCA: String, code: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try {
            val result = c.bindDebug(serverAddr, serverCA, code)
            Log.d(TAG, "bindDebug returned: ${result.take(100)}")
            result
        } catch (e: UnsatisfiedLinkError) {
            """{"error":"native library error: ${e.message}"}"""
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun statusRaw(): String = core?.status() ?: "{}"

    fun status(): String {
        val raw = statusRaw()
        return try {
            val obj = org.json.JSONObject(raw)
            val result = org.json.JSONObject()
            result.put("started", started.get())
            result.put("vpnRunning", SnetVpnService.isRunning)

            val networks = obj.optJSONArray("networks")
            if (networks != null) {
                result.put("networkCount", networks.length())
                val list = org.json.JSONArray()
                for (i in 0 until networks.length()) {
                    val net = networks.getJSONObject(i)
                    val entry = org.json.JSONObject()
                    entry.put("id", net.optString("networkId", ""))
                    entry.put("name", net.optString("name", ""))
                    entry.put("ip", net.optString("ip", ""))
                    entry.put("active", net.optBoolean("active", false))
                    entry.put("owner", net.optBoolean("owner", false))
                    list.put(entry)
                }
                result.put("networks", list)
            } else {
                result.put("networkCount", 0)
                result.put("networks", org.json.JSONArray())
            }
            result.toString(2)
        } catch (e: Exception) {
            raw
        }
    }

    fun joinNetwork(nid: String, code: String, serverAddr: String, port: Int): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try {
            c.joinNetwork(nid, code, serverAddr, port.toLong())
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun createNetwork(name: String, subnet: String, approvalRequired: Boolean, serverAddr: String, port: Int): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try {
            c.createNetwork(name, subnet, approvalRequired, serverAddr, port.toLong())
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun leaveNetwork(nid: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.leaveNetwork(nid); """{"ok":true}""" } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun removeNetwork(nid: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.removeNetwork(nid); """{"ok":true}""" } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun peers(nid: String): String {
        val c = core ?: return """{"peers":[]}"""
        return try { c.peers(nid) } catch (e: Exception) { """{"error":"${e.message}"}""" }
    }

    fun info(nid: String): String {
        val c = core ?: return "{}"
        return try { c.info(nid) } catch (e: Exception) { """{"error":"${e.message}"}""" }
    }

    fun detectLocalSubnets(): String {
        val c = core ?: return "[]"
        return try { c.detectLocalSubnets() } catch (e: Exception) { "[]" }
    }

    fun updateSubnets(nid: String, subnets: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.updateSubnets(nid, subnets); """{"ok":true}""" } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun updateSettings(nid: String, name: String, subnet: String, approvalRequired: Boolean): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try {
            c.updateSettings(nid, name, subnet, approvalRequired)
            """{"ok":true}"""
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun rejoin(nid: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.rejoin(nid); """{"ok":true}""" } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun deleteNetwork(nid: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.deleteNetwork(nid) } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun approvePending(nid: String, pendingID: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.approvePending(nid, pendingID) } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun denyPending(nid: String, pendingID: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.denyPending(nid, pendingID) } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun cancelPending(pendingID: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.cancelPending(pendingID) } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun kick(nid: String, nodeID: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.kick(nid, nodeID) } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun resetCode(nid: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.resetCode(nid) } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    // --- Convenience methods for UI fragments ---

    fun getStatus(@Suppress("UNUSED_PARAMETER") context: android.content.Context): String? {
        return try {
            val raw = core?.status() ?: return null
            val obj = org.json.JSONObject(raw)

            // Go Status() returns: deviceId, serverAddr, bound, wgPort, networks[], pendingJoins[]
            // Go network entries: networkId, name, ip, subnet, port, active, owner(bool),
            //   error, interface, peerStats, allowedSubnets

            val result = org.json.JSONObject()
            result.put("started", started.get())
            result.put("bound", obj.optBoolean("bound", false))

            // "connected" is derived: bound + has active network with tunnel
            val networks = obj.optJSONArray("networks")
            val hasActive = if (networks != null) {
                var found = false
                for (i in 0 until networks.length()) {
                    if (networks.getJSONObject(i).optBoolean("active", false)) { found = true; break }
                }
                found
            } else false
            result.put("connected", obj.optBoolean("bound", false) && hasActive)
            result.put("network_count", networks?.length() ?: 0)

            if (networks != null) {
                val list = org.json.JSONArray()
                for (i in 0 until networks.length()) {
                    val n = networks.getJSONObject(i)
                    val entry = org.json.JSONObject()
                    entry.put("id", n.optString("networkId", ""))
                    entry.put("name", n.optString("name", ""))
                    entry.put("subnet_cidr", n.optString("subnet", "--"))
                    entry.put("my_ip", n.optString("ip", "--"))
                    entry.put("role", if (n.optBoolean("owner", false)) "owner" else "member")
                    entry.put("peer_count", n.optInt("nodeCount", 0))
                    entry.put("server_count", if (n.has("relayPort") && n.optInt("relayPort", 0) > 0) 1 else 0)
                    entry.put("online_count", n.optInt("nodeCount", 0))
                    entry.put("pending_count", n.optInt("pendingCount", 0))
                    list.put(entry)
                }
                result.put("networks", list)
            } else {
                result.put("networks", org.json.JSONArray())
            }

            // Services: detect from config state
            val services = org.json.JSONObject()
            services.put("wireguard_running", started.get())
            services.put("tun_running", SnetVpnService.isRunning)
            result.put("services", services)

            result.toString()
        } catch (e: Exception) {
            Log.e(TAG, "getStatus failed", e)
            null
        }
    }

    fun getDeviceID(@Suppress("UNUSED_PARAMETER") context: android.content.Context): String? {
        return try {
            val raw = core?.status() ?: return null
            val obj = org.json.JSONObject(raw)
            obj.optString("deviceId", null) // Go key: "deviceId"
        } catch (e: Exception) { null }
    }

    fun connect(@Suppress("UNUSED_PARAMETER") context: android.content.Context, networkID: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.rejoin(networkID); """{"ok":true}""" } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun disconnect(@Suppress("UNUSED_PARAMETER") context: android.content.Context, networkID: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.leaveNetwork(networkID); """{"ok":true}""" } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun getInviteCode(@Suppress("UNUSED_PARAMETER") context: android.content.Context, networkID: String): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try { c.info(networkID) } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }

    fun updateNetwork(@Suppress("UNUSED_PARAMETER") context: android.content.Context, networkID: String, name: String, subnets: List<String>): String {
        val c = core ?: return """{"error":"core not initialized"}"""
        return try {
            c.updateSettings(networkID, name, "", false)
            """{"ok":true}"""
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"") ?: "unknown"}"}"""
        }
    }
}
