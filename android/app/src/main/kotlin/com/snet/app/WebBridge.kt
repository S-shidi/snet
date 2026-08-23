package com.snet.app

import android.util.Log
import android.webkit.JavascriptInterface

/**
 * JavaScript interface for the WebView-based UI.
 * Exposes Go/SnetBridge functions to JavaScript as window.WebBridge.*
 */
class WebBridge(private val activity: MainActivity) {
    private companion object {
        const val TAG = "WebBridge"
    }

    @JavascriptInterface
    fun status(): String {
        return try {
            val raw = SnetBridge.statusRaw() ?: "{}"
            val obj = org.json.JSONObject(raw)
            obj.put("started", SnetBridge.isStarted())
            obj.put("vpnRunning", SnetVpnService.isRunning)
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
            SnetBridge.createNetwork(name, subnet, approvalRequired, server, port)
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
            SnetBridge.joinNetwork(nid, code, server, port)
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
            SnetBridge.bind(server, ca, code)
        } catch (e: Exception) {
            Log.e(TAG, "bind failed", e)
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun rejoin(nid: String): String {
        return try {
            // Ensure VPN service (and daemon) is running before rejoin
            if (!SnetVpnService.isRunning) {
                Log.d(TAG, "VPN not running, starting before rejoin")
                val intent = android.content.Intent(activity, SnetVpnService::class.java)
                intent.action = "START"
                activity.startForegroundService(intent)
                // Wait for VPN service to establish TUN and init daemon
                Thread.sleep(1500)
            }
            SnetBridge.rejoin(nid)
            """{"ok":true}"""
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun leave(nid: String): String {
        return try {
            SnetBridge.leaveNetwork(nid)
            """{"ok":true}"""
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun remove(nid: String): String {
        return try {
            SnetBridge.removeNetwork(nid)
            """{"ok":true}"""
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
    }

    @JavascriptInterface
    fun deleteNet(nid: String): String {
        return try {
            SnetBridge.deleteNetwork(nid)
            """{"ok":true}"""
        } catch (e: Exception) {
            """{"error":"${e.message?.replace("\"", "\\\"")}"}"""
        }
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
            SnetBridge.updateSettings(nid, name, subnet, approvalRequired)
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
    fun startVpn(): String {
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

    @JavascriptInterface
    fun getDeviceId(): String {
        return try {
            SnetBridge.getDeviceID(activity) ?: ""
        } catch (e: Exception) {
            ""
        }
    }
}
