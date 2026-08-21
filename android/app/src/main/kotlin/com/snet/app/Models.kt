package com.snet.app

data class NetworkInfo(
    val id: String,
    val name: String,
    val subnet: String,
    val myIP: String,
    val role: String,
    val nodeCount: Int,
    val serverCount: Int,
    val onlineCount: Int,
    val pendingCount: Int,
    val peerCount: Int,
    val connected: Boolean,
    val allowedSubnets: List<String>
)

data class PeerInfo(
    val deviceID: String,
    val hostname: String,
    val ip: String,
    val online: Boolean,
    val latencyMs: Int,
    val publicIP: String
)

data class ServerInfo(
    val serverIP: String,
    val hostname: String,
    val region: String,
    val latencyMs: Int,
    val connected: Boolean,
    val direction: String
)

data class LogEntry(
    val ts: String,
    val event: String,
    val message: String
)
