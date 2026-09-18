package com.snet.app.repository

import android.content.Context
import android.util.Log
import com.snet.app.SnetBridge
import com.snet.app.model.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.URL

class SnetRepository(private val appContext: Context) {
    
    companion object {
        private const val TAG = "SnetRepository"
        // 固定服务器地址
        const val SERVER_ADDR = "https://snet.uizhi.eu.org:8090"
        const val SERVER_PORT = 51820
    }
    
    suspend fun getStatus(): SystemStatus = withContext(Dispatchers.IO) {
        try {
            val statusRaw = SnetBridge.statusRaw()
            parseStatus(statusRaw)
        } catch (e: Exception) {
            SystemStatus()
        }
    }
    
    suspend fun createNetwork(name: String, subnet: String): Network = withContext(Dispatchers.IO) {
        val result = SnetBridge.createNetwork(name, subnet, false, SERVER_ADDR, SERVER_PORT, "", "[]", "")
        val json = JSONObject(result)
        Network(
            networkId = json.optString("networkId", ""),
            name = name,
            subnet = subnet,
            owner = true
        )
    }
    
    suspend fun joinNetwork(link: String): Map<String, Any> = withContext(Dispatchers.IO) {
        // 解析链接 snet://join?nid=xxx&code=xxx
        val url = URL(link.replace("snet://", "http://"))
        val params = url.query.split("&").associate {
            val (key, value) = it.split("=")
            key to java.net.URLDecoder.decode(value, "UTF-8")
        }
        
        val nid = params["nid"] ?: ""
        val code = params["code"] ?: ""
        
        // 使用固定服务器地址
        val result = SnetBridge.joinNetwork(nid, code, SERVER_ADDR, SERVER_PORT)
        val json = JSONObject(result)
        
        mapOf(
            "networkId" to json.optString("networkId", nid),
            "ip" to json.optString("ip", ""),
            "status" to json.optString("status", "joined")
        )
    }
    
    suspend fun bindDevice(ca: String, code: String): Boolean = withContext(Dispatchers.IO) {
        // 使用固定服务器地址
        val result = SnetBridge.bind(SERVER_ADDR, ca, code)
        val json = JSONObject(result)
        !json.has("error")
    }
    
    suspend fun toggleNetwork(networkId: String, connect: Boolean): Boolean = withContext(Dispatchers.IO) {
        if (connect) {
            // 开启网络 - 使用 rejoin 方法重新连接
            Log.d(TAG, "重新连接网络: $networkId")
            val result = SnetBridge.rejoin(networkId)
            val json = JSONObject(result)
            Log.d(TAG, "rejoin 结果: $result")
            
            if (json.has("error")) {
                throw Exception(json.getString("error"))
            }
            true
        } else {
            // 断开网络
            Log.d(TAG, "断开网络: $networkId")
            val result = SnetBridge.leaveNetwork(networkId)
            val json = JSONObject(result)
            Log.d(TAG, "leave 结果: $result")
            
            if (json.has("error")) {
                throw Exception(json.getString("error"))
            }
            true
        }
    }
    
    suspend fun deleteNetwork(networkId: String): Boolean = withContext(Dispatchers.IO) {
        SnetBridge.removeNetwork(networkId)
        true
    }
    
    suspend fun leaveNetwork(networkId: String): Boolean = withContext(Dispatchers.IO) {
        SnetBridge.leaveNetwork(networkId)
        true
    }
    
    suspend fun getNetworkInfo(networkId: String): NetworkInfo = withContext(Dispatchers.IO) {
        try {
            val infoRaw = SnetBridge.info(networkId)
            parseNetworkInfo(infoRaw)
        } catch (e: Exception) {
            NetworkInfo(networkId, networkId, "")
        }
    }
    
    suspend fun checkAuthStatus(): AuthStatus = withContext(Dispatchers.IO) {
        try {
            val result = SnetBridge.getAuthStatus()
            val json = JSONObject(result)
            AuthStatus(
                bound = json.optBoolean("bound", false),
                expired = json.optBoolean("expired", false),
                authCodeId = json.optString("authCodeId", "")
            )
        } catch (e: Exception) {
            AuthStatus(bound = false, expired = false)
        }
    }
    
    private fun parseStatus(statusRaw: String): SystemStatus {
        try {
            val json = JSONObject(statusRaw)
            return SystemStatus(
                deviceId = json.optString("deviceId", ""),
                serverAddr = json.optString("serverAddr", SERVER_ADDR),
                wgPort = json.optInt("wgPort", SERVER_PORT),
                interfaceName = json.optString("interface", null),
                bound = json.optBoolean("bound", false),
                networks = parseNetworks(json.optJSONArray("networks")),
                pendingJoins = emptyList()
            )
        } catch (e: Exception) {
            return SystemStatus()
        }
    }
    
    private fun parseNetworks(networksArray: org.json.JSONArray?): List<Network> {
        if (networksArray == null) return emptyList()
        
        return (0 until networksArray.length()).map { i ->
            val net = networksArray.getJSONObject(i)
            Network(
                networkId = net.optString("networkId", ""),
                name = net.optString("name", ""),
                subnet = net.optString("subnet", ""),
                ip = net.optString("ip", null),
                isActive = net.optBoolean("active", false) || net.optBoolean("isActive", false),
                owner = net.optBoolean("owner", false),
                memberCount = net.optInt("memberCount", 0),
                onlineCount = net.optInt("onlineCount", 0),
                rxBytes = net.optLong("rxBytes", 0),
                txBytes = net.optLong("txBytes", 0),
                allowedSubnets = parseStringList(net.optJSONArray("allowedSubnets")),
                description = net.optString("description", null),
                tags = parseStringList(net.optJSONArray("tags")),
                visibility = net.optString("visibility", null),
                approvalRequired = net.optBoolean("approvalRequired", false),
                serverState = net.optString("serverState", null)
            )
        }
    }
    
    private fun parseNetworkInfo(infoRaw: String): NetworkInfo {
        try {
            val json = JSONObject(infoRaw)
            return NetworkInfo(
                networkId = json.optString("networkId", ""),
                name = json.optString("name", ""),
                subnet = json.optString("subnet", ""),
                pairingCode = json.optString("pairingCode", null),
                link = json.optString("link", null),
                nodes = parseMembers(json.optJSONArray("nodes")),
                pending = emptyList()
            )
        } catch (e: Exception) {
            return NetworkInfo("", "", "")
        }
    }
    
    private fun parseMembers(membersArray: org.json.JSONArray?): List<Member> {
        if (membersArray == null) return emptyList()
        
        return (0 until membersArray.length()).map { i ->
            val member = membersArray.getJSONObject(i)
            Member(
                id = member.optString("id", ""),
                deviceId = member.optString("deviceId", null),
                deviceName = member.optString("deviceName", null),
                ip = member.optString("ip", ""),
                publicKey = member.optString("publicKey", ""),
                online = member.optBoolean("online", false),
                role = member.optString("role", "member"),
                allowedSubnets = parseStringList(member.optJSONArray("allowedSubnets"))
            )
        }
    }
    
    private fun parseStringList(array: org.json.JSONArray?): List<String> {
        if (array == null) return emptyList()
        return (0 until array.length()).map { array.getString(it) }
    }
}