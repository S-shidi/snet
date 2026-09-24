package com.snet.app.viewmodel

import android.app.Application
import android.util.Log
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.snet.app.SnetBridge
import com.snet.app.SnetVpnService
import com.snet.app.model.*
import com.snet.app.repository.SnetRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

class MainViewModel(application: Application) : AndroidViewModel(application) {
    
    companion object {
        private const val TAG = "MainViewModel"
    }
    
    private val repository = SnetRepository(application.applicationContext)
    
    // UI State
    private val _networks = MutableStateFlow<List<Network>>(emptyList())
    val networks: StateFlow<List<Network>> = _networks.asStateFlow()
    
    private val _isLoading = MutableStateFlow(false)
    val isLoading: StateFlow<Boolean> = _isLoading.asStateFlow()
    
    private val _vpnRunning = MutableStateFlow(false)
    val vpnRunning: StateFlow<Boolean> = _vpnRunning.asStateFlow()
    
    private val _daemonStarted = MutableStateFlow(false)
    val daemonStarted: StateFlow<Boolean> = _daemonStarted.asStateFlow()
    
    private val _deviceId = MutableStateFlow("")
    val deviceId: StateFlow<String> = _deviceId.asStateFlow()
    
    private val _isBound = MutableStateFlow(false)
    val isBound: StateFlow<Boolean> = _isBound.asStateFlow()
    
    private val _message = MutableStateFlow<String?>(null)
    val message: StateFlow<String?> = _message.asStateFlow()
    
    // VPN Permission State
    private val _needVpnPermission = MutableStateFlow<String?>(null)
    val needVpnPermission: StateFlow<String?> = _needVpnPermission.asStateFlow()
    
    // Dialog State
    private val _showBindDialog = MutableStateFlow(false)
    val showBindDialog: StateFlow<Boolean> = _showBindDialog.asStateFlow()
    
    private val _showJoinDialog = MutableStateFlow(false)
    val showJoinDialog: StateFlow<Boolean> = _showJoinDialog.asStateFlow()
    
    // Detail State
    private val _selectedNetwork = MutableStateFlow<Network?>(null)
    val selectedNetwork: StateFlow<Network?> = _selectedNetwork.asStateFlow()
    
    private val _networkInfo = MutableStateFlow<NetworkInfo?>(null)
    val networkInfo: StateFlow<NetworkInfo?> = _networkInfo.asStateFlow()
    
    private val _members = MutableStateFlow<List<Member>>(emptyList())
    val members: StateFlow<List<Member>> = _members.asStateFlow()
    
    private val _authExpired = MutableStateFlow(false)
    val authExpired: StateFlow<Boolean> = _authExpired.asStateFlow()
    
    init {
        // 初始化 SnetBridge
        viewModelScope.launch {
            val ctx = getApplication<Application>().applicationContext
            val dir = ctx.filesDir.absolutePath
            Log.d(TAG, "初始化 SnetBridge, dir=$dir")
            SnetBridge.init(ctx, dir)
            _daemonStarted.value = true
            
            // 检查是否已绑定，如果已绑定则自动启动守护进程
            refreshStatus()
            
            // 如果已绑定且守护进程未启动，则启动
            if (_isBound.value && !SnetBridge.isStarted()) {
                Log.d(TAG, "已绑定设备，自动启动守护进程...")
                SnetBridge.start(SnetRepository.SERVER_ADDR, "")
            }
            
            startAuthCheck()
        }
    }
    
    private fun startAuthCheck() {
        viewModelScope.launch {
            while (true) {
                kotlinx.coroutines.delay(60 * 60 * 1000L) // 每小时检查一次
                try {
                    val status = repository.checkAuthStatus()
                    if (status.expired) {
                        _authExpired.value = true
                        showMessage("授权码已过期，所有网络已离线，请续期或更换授权码")
                    }
                } catch (e: Exception) {
                    Log.e(TAG, "授权状态检查失败: ${e.message}")
                }
            }
        }
    }
    
    fun refreshStatus() {
        viewModelScope.launch {
            _isLoading.value = true
            try {
                Log.d(TAG, "刷新状态...")
                val status = repository.getStatus()
                _deviceId.value = status.deviceId
                _isBound.value = status.bound
                _networks.value = status.networks
                _vpnRunning.value = SnetVpnService.isRunning
                Log.d(TAG, "状态刷新成功: deviceId=${status.deviceId}, bound=${status.bound}, networks=${status.networks.size}, vpnRunning=${_vpnRunning.value}")
            } catch (e: Exception) {
                Log.e(TAG, "刷新失败: ${e.message}", e)
                showMessage("刷新失败: ${e.message}")
            } finally {
                _isLoading.value = false
            }
        }
    }
    
    fun bindDevice(ca: String, code: String) {
        viewModelScope.launch {
            _isLoading.value = true
            try {
                Log.d(TAG, "绑定设备: ca=$ca, code=$code")
                repository.bindDevice(ca, code)
                
                // 绑定成功后启动守护进程
                Log.d(TAG, "启动守护进程...")
                SnetBridge.start(SnetRepository.SERVER_ADDR, ca)
                
                showMessage("设备绑定成功")
                hideBindDialog()
                refreshStatus()
            } catch (e: Exception) {
                Log.e(TAG, "绑定失败: ${e.message}", e)
                showMessage("绑定失败: ${e.message}")
            } finally {
                _isLoading.value = false
            }
        }
    }
    
    fun createNetwork(name: String, subnet: String) {
        viewModelScope.launch {
            _isLoading.value = true
            try {
                Log.d(TAG, "创建网络: name=$name, subnet=$subnet")
                repository.createNetwork(name, subnet)
                showMessage("网络创建成功")
                refreshStatus()
            } catch (e: Exception) {
                Log.e(TAG, "创建失败: ${e.message}", e)
                showMessage("创建失败: ${e.message}")
            } finally {
                _isLoading.value = false
            }
        }
    }
    
    fun joinNetwork(link: String) {
        viewModelScope.launch {
            _isLoading.value = true
            try {
                Log.d(TAG, "加入网络: link=$link")
                val result = repository.joinNetwork(link)
                val status = result["status"] as? String ?: ""
                if (status == "pending") {
                    showMessage("已提交加入请求，等待网络创建者批准")
                } else {
                    showMessage("已加入网络")
                }
                hideJoinDialog()
                refreshStatus()
            } catch (e: Exception) {
                Log.e(TAG, "加入失败: ${e.message}", e)
                showMessage("加入失败: ${e.message}")
            } finally {
                _isLoading.value = false
            }
        }
    }
    
    fun toggleNetwork(networkId: String, connect: Boolean) {
        viewModelScope.launch {
            _isLoading.value = true
            try {
                Log.d(TAG, "切换网络: networkId=$networkId, connect=$connect")
                
                if (connect) {
                    // 开启网络 - 检查VPN权限
                    val context = getApplication<Application>()
                    val vpnIntent = android.net.VpnService.prepare(context)
                    
                    if (vpnIntent != null) {
                        // 需要请求VPN权限 - 通知UI
                        Log.d(TAG, "需要请求VPN权限")
                        _needVpnPermission.value = networkId // 保存待连接的网络ID
                        showMessage("请在弹出的对话框中允许VPN连接")
                        _isLoading.value = false
                        return@launch
                    }
                    
                    // 已有权限，直接启动VPN
                    startVpnServiceAndConnect(networkId)
                } else {
                    // 断开网络
                    Log.d(TAG, "断开网络...")
                    repository.toggleNetwork(networkId, false)
                    showMessage("已断开网络")
                }
                
                refreshStatus()
            } catch (e: Exception) {
                Log.e(TAG, "切换网络失败: ${e.message}", e)
                showMessage("操作失败: ${e.message}")
            } finally {
                _isLoading.value = false
            }
        }
    }
    
    fun onVpnPermissionGranted() {
        // VPN权限已授予，继续启动VPN
        val networkId = _needVpnPermission.value
        if (networkId != null) {
            viewModelScope.launch {
                _isLoading.value = true
                try {
                    startVpnServiceAndConnect(networkId)
                    refreshStatus()
                } catch (e: Exception) {
                    showMessage("连接失败: ${e.message}")
                } finally {
                    _isLoading.value = false
                    _needVpnPermission.value = null
                }
            }
        }
    }
    
    private suspend fun startVpnServiceAndConnect(networkId: String) {
        Log.d(TAG, "启动 VPN 服务...")
        startVpnService()
        
        // 等待 VPN 启动
        kotlinx.coroutines.delay(1000)
        
        // 使用 rejoin 方法重新连接
        Log.d(TAG, "调用 rejoin...")
        repository.toggleNetwork(networkId, true)
        Log.d(TAG, "rejoin 成功")
        
        showMessage("已连接网络")
    }
    
    fun showNetworkDetail(network: Network) {
        _selectedNetwork.value = network
        viewModelScope.launch {
            _isLoading.value = true
            try {
                val info = repository.getNetworkInfo(network)
                _networkInfo.value = info
                _members.value = info.nodes
            } catch (e: Exception) {
                showMessage("获取网络详情失败: ${e.message}")
            } finally {
                _isLoading.value = false
            }
        }
    }
    
    fun deleteNetwork(networkId: String) {
        viewModelScope.launch {
            _isLoading.value = true
            try {
                repository.deleteNetwork(networkId)
                showMessage("网络已删除")
                _selectedNetwork.value = null
                refreshStatus()
            } catch (e: Exception) {
                showMessage("删除失败: ${e.message}")
            } finally {
                _isLoading.value = false
            }
        }
    }
    
    fun leaveNetwork(networkId: String) {
        viewModelScope.launch {
            _isLoading.value = true
            try {
                repository.leaveNetwork(networkId)
                showMessage("已退出网络")
                _selectedNetwork.value = null
                refreshStatus()
            } catch (e: Exception) {
                showMessage("退出失败: ${e.message}")
            } finally {
                _isLoading.value = false
            }
        }
    }
    
    fun startVpnService() {
        val context = getApplication<Application>()
        val intent = android.content.Intent(context, SnetVpnService::class.java)
        intent.action = "START"
        
        // 使用 startForegroundService 确保服务启动
        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.O) {
            context.startForegroundService(intent)
        } else {
            context.startService(intent)
        }
        
        _vpnRunning.value = true
        Log.d(TAG, "VPN 服务已启动")
    }
    
    fun stopVpnService() {
        val context = getApplication<Application>()
        val intent = android.content.Intent(context, SnetVpnService::class.java)
        context.stopService(intent)
        _vpnRunning.value = false
        Log.d(TAG, "VPN 服务已停止")
    }
    
    // Dialog Controls
    fun showBindDialog() {
        _showBindDialog.value = true
    }
    
    fun hideBindDialog() {
        _showBindDialog.value = false
    }
    
    fun showJoinDialog() {
        _showJoinDialog.value = true
    }
    
    fun hideJoinDialog() {
        _showJoinDialog.value = false
    }
    
    private fun showMessage(msg: String) {
        _message.value = msg
    }
    
    fun clearMessage() {
        _message.value = null
    }
}