package com.snet.app.model

data class Network(
    val networkId: String,
    val name: String,
    val subnet: String,
    val ip: String? = null,
    val isActive: Boolean = false,
    val owner: Boolean = false,
    val memberCount: Int = 0,
    val onlineCount: Int = 0,
    val rxBytes: Long = 0,
    val txBytes: Long = 0,
    val allowedSubnets: List<String> = emptyList(),
    val description: String? = null,
    val tags: List<String> = emptyList(),
    val visibility: String? = null,
    val approvalRequired: Boolean = false,
    val serverState: String? = null
)

data class Member(
    val id: String,
    val deviceId: String? = null,
    val deviceName: String? = null,
    val ip: String,
    val publicKey: String,
    val online: Boolean = false,
    val role: String = "member", // owner/admin/member
    val allowedSubnets: List<String> = emptyList()
)

data class PendingDevice(
    val id: String,
    val deviceId: String? = null,
    val publicKey: String,
    val error: String? = null
)

data class NetworkInfo(
    val networkId: String,
    val name: String,
    val subnet: String,
    val pairingCode: String? = null,
    val link: String? = null,
    val nodes: List<Member> = emptyList(),
    val pending: List<PendingDevice> = emptyList()
)

data class NetworkSettings(
    val name: String? = null,
    val subnet: String? = null,
    val approvalRequired: Boolean? = null,
    val description: String? = null,
    val tags: List<String>? = null,
    val visibility: String? = null
)

data class SystemStatus(
    val deviceId: String = "",
    val serverAddr: String = "",
    val wgPort: Int = 51820,
    val interfaceName: String? = null,
    val bound: Boolean = false,
    val networks: List<Network> = emptyList(),
    val pendingJoins: List<PendingDevice> = emptyList()
)

data class AuthStatus(
    val bound: Boolean = false,
    val expired: Boolean = false,
    val authCodeId: String = ""
)
