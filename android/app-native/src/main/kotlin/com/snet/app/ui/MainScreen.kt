package com.snet.app.ui

import android.content.Intent
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import com.snet.app.model.*
import com.snet.app.viewmodel.MainViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.launch
import kotlinx.coroutines.delay

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MainScreen(viewModel: MainViewModel) {
    val networks by viewModel.networks.collectAsStateWithLifecycle()
    val isLoading by viewModel.isLoading.collectAsStateWithLifecycle()
    val vpnRunning by viewModel.vpnRunning.collectAsStateWithLifecycle()
    val daemonStarted by viewModel.daemonStarted.collectAsStateWithLifecycle()
    val deviceId by viewModel.deviceId.collectAsStateWithLifecycle()
    val isBound by viewModel.isBound.collectAsStateWithLifecycle()
    val message by viewModel.message.collectAsStateWithLifecycle()
    val showBindDialog by viewModel.showBindDialog.collectAsStateWithLifecycle()
    val showJoinDialog by viewModel.showJoinDialog.collectAsStateWithLifecycle()
    val authExpired by viewModel.authExpired.collectAsStateWithLifecycle()
    
    var selectedTabIndex by remember { mutableStateOf(0) }
    var showCreateDialog by remember { mutableStateOf(false) }
    var showSettings by remember { mutableStateOf(false) }
    var selectedNetwork by remember { mutableStateOf<Network?>(null) }
    
    // 底部浮动提示
    val snackbarHostState = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    
    LaunchedEffect(message) {
        message?.let {
            scope.launch {
                snackbarHostState.showSnackbar(message = it, duration = SnackbarDuration.Short)
                delay(2000)
                viewModel.clearMessage()
            }
        }
    }
    
    // 拦截系统返回
    BackHandler(enabled = showSettings || selectedNetwork != null) {
        if (showSettings) {
            showSettings = false
        }
        if (selectedNetwork != null) {
            selectedNetwork = null
        }
    }
    
    // 条件渲染：优先渲染二级页面
    if (showSettings) {
        SettingsScreen(viewModel) {
            showSettings = false
        }
        return
    }
    
    selectedNetwork?.let { network ->
        NetworkDetailScreen(network, viewModel) {
            selectedNetwork = null
        }
        return
    }
    
    // 主页面 Scaffold
    Scaffold(
        snackbarHost = { 
            SnackbarHost(snackbarHostState) { data ->
                Snackbar(
                    modifier = Modifier.padding(16.dp),
                    containerColor = MaterialTheme.colorScheme.inverseSurface,
                    contentColor = MaterialTheme.colorScheme.inverseOnSurface
                ) {
                    Text(data.visuals.message)
                }
            }
        },
        topBar = {
            TopAppBar(
                title = {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Column {
                            Text("Snet", style = MaterialTheme.typography.titleMedium)
                            Text("虚拟组网", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                },
                actions = {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Box(modifier = Modifier.size(8.dp)) {
                            Surface(
                                modifier = Modifier.fillMaxSize(),
                                shape = MaterialTheme.shapes.small,
                                color = if (daemonStarted) Color(0xFF4CAF50) else Color.Gray
                            ) {}
                        }
                        Spacer(modifier = Modifier.width(4.dp))
                        Text("snetd", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    IconButton(onClick = { viewModel.refreshStatus() }) {
                        Icon(Icons.Default.Refresh, "刷新")
                    }
                    IconButton(onClick = { showSettings = true }) {
                        Icon(Icons.Default.Settings, "设置")
                    }
                }
            )
        }
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding)) {
            TabRow(selectedTabIndex = selectedTabIndex) {
                Tab(selected = selectedTabIndex == 0, onClick = { selectedTabIndex = 0 }, text = { Text("网络") })
                Tab(selected = selectedTabIndex == 1, onClick = { selectedTabIndex = 1 }, text = { Text("状态") })
            }
            
            if (authExpired) {
                Card(
                    modifier = Modifier.fillMaxWidth().padding(16.dp),
                    colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer)
                ) {
                    Row(
                        modifier = Modifier.padding(16.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Icon(
                            Icons.Default.Warning,
                            contentDescription = "警告",
                            tint = MaterialTheme.colorScheme.error,
                            modifier = Modifier.size(24.dp)
                        )
                        Spacer(modifier = Modifier.width(12.dp))
                        Column {
                            Text(
                                "授权码已过期",
                                style = MaterialTheme.typography.titleMedium,
                                color = MaterialTheme.colorScheme.onErrorContainer
                            )
                            Text(
                                "所有网络已离线，请联系管理员续期或更换授权码",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onErrorContainer
                            )
                        }
                    }
                }
            }
            
            if (isLoading) {
                LinearProgressIndicator(modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp))
            }
            
            when (selectedTabIndex) {
                0 -> NetworkTab(networks, isBound, vpnRunning, { showCreateDialog = true }, { viewModel.showJoinDialog() }, { nid, conn -> viewModel.toggleNetwork(nid, conn) }, { selectedNetwork = it }, viewModel)
                1 -> StatusTab(deviceId, isBound, daemonStarted, vpnRunning)
            }
        }
        
        if (showBindDialog) {
            BindDeviceDialog({ viewModel.hideBindDialog() }, { ca, code -> viewModel.bindDevice(ca, code) })
        }
        
        if (showJoinDialog) {
            JoinNetworkDialog({ viewModel.hideJoinDialog() }, { viewModel.joinNetwork(it) })
        }
        
        if (showCreateDialog) {
            CreateNetworkDialog({ showCreateDialog = false }, { name, subnet -> viewModel.createNetwork(name, subnet) })
        }
    }
}

@Composable
fun NetworkTab(
    networks: List<Network>,
    isBound: Boolean,
    vpnRunning: Boolean,
    onCreate: () -> Unit,
    onJoin: () -> Unit,
    onToggle: (String, Boolean) -> Unit,
    onNetworkClick: (Network) -> Unit,
    viewModel: MainViewModel
) {
    Column {
        Row(
            modifier = Modifier.fillMaxWidth().padding(16.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            Button(onClick = onCreate, enabled = isBound && networks.none { it.owner }) {
                Icon(Icons.Default.Add, null)
                Spacer(modifier = Modifier.width(4.dp))
                Text("创建")
            }
            OutlinedButton(onClick = onJoin) {
                Icon(Icons.Default.Add, null)
                Spacer(modifier = Modifier.width(4.dp))
                Text("加入")
            }
        }
        
        if (networks.isEmpty()) {
            Column(
                modifier = Modifier.fillMaxSize().padding(32.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center
            ) {
                Icon(Icons.Default.NetworkCheck, null, Modifier.size(64.dp), MaterialTheme.colorScheme.primary)
                Spacer(modifier = Modifier.height(16.dp))
                Text(if (!isBound) "请先绑定设备" else "还没有加入任何网络", style = MaterialTheme.typography.titleMedium)
                Spacer(modifier = Modifier.height(8.dp))
                Text(
                    if (!isBound) "在设置中绑定设备以开始使用" else "创建一个网络，或使用邀请链接加入",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        } else {
            LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp)
            ) {
                items(networks, key = { it.networkId }) { network ->
                    NetworkCard(network, vpnRunning, { onToggle(network.networkId, it) }, { onNetworkClick(network) }, viewModel)
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NetworkCard(
    network: Network,
    vpnRunning: Boolean,
    onToggle: (Boolean) -> Unit,
    onClick: () -> Unit,
    viewModel: MainViewModel
) {
    var showMenu by remember { mutableStateOf(false) }
    var showMembers by remember { mutableStateOf(false) }
    var showInvite by remember { mutableStateOf(false) }
    var showSettings by remember { mutableStateOf(false) }
    var showDeleteConfirm by remember { mutableStateOf(false) }
    var showLeaveConfirm by remember { mutableStateOf(false) }
    
    // 使用实际VPN运行状态，而不是配置状态
    val isActuallyConnected = network.isActive && vpnRunning
    
    Card(modifier = Modifier.fillMaxWidth(), elevation = CardDefaults.cardElevation(defaultElevation = 2.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.Top) {
                Column(modifier = Modifier.weight(1f).clickable(onClick = onClick)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(network.name.ifEmpty { network.networkId }, style = MaterialTheme.typography.titleMedium)
                        if (network.owner) {
                            Spacer(modifier = Modifier.width(8.dp))
                            SuggestionChip(onClick = {}, label = { Text("owner") }, modifier = Modifier.height(24.dp))
                        }
                    }
                    Spacer(modifier = Modifier.height(8.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                        Text("IP ${network.ip ?: "-"}", style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                        Text("网段 ${network.subnet}", style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
                    }
                    if (network.memberCount > 0) {
                        Spacer(modifier = Modifier.height(8.dp))
                        Text("${network.onlineCount}/${network.memberCount} 在线", style = MaterialTheme.typography.bodySmall, color = if (network.onlineCount > 0) Color(0xFF4CAF50) else Color.Gray)
                    }
                    if (network.rxBytes > 0 || network.txBytes > 0) {
                        Spacer(modifier = Modifier.height(8.dp))
                        Text("收 ${formatBytes(network.rxBytes)} · 发 ${formatBytes(network.txBytes)}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                Column(horizontalAlignment = Alignment.End) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        SuggestionChip(onClick = {}, label = { Text(if (isActuallyConnected) "已连接" else "未连接") }, modifier = Modifier.height(24.dp))
                        Spacer(modifier = Modifier.width(8.dp))
                        
                        // 菜单按钮
                        Box {
                            IconButton(onClick = { showMenu = true }) {
                                Icon(Icons.Default.MoreVert, "更多")
                            }
                            
                            DropdownMenu(expanded = showMenu, onDismissRequest = { showMenu = false }) {
                                if (network.owner) {
                                    // Owner 菜单
                                    DropdownMenuItem(
                                        text = { Text("详情") },
                                        onClick = { showMenu = false; onClick() },
                                        leadingIcon = { Icon(Icons.Default.Info, null) }
                                    )
                                    DropdownMenuItem(
                                        text = { Text("查看成员") },
                                        onClick = { showMenu = false; showMembers = true },
                                        leadingIcon = { Icon(Icons.Default.People, null) }
                                    )
                                    DropdownMenuItem(
                                        text = { Text("邀请") },
                                        onClick = { showMenu = false; showInvite = true },
                                        leadingIcon = { Icon(Icons.Default.PersonAdd, null) }
                                    )
                                    DropdownMenuItem(
                                        text = { Text("设置") },
                                        onClick = { showMenu = false; showSettings = true },
                                        leadingIcon = { Icon(Icons.Default.Settings, null) }
                                    )
                                    HorizontalDivider()
                                    DropdownMenuItem(
                                        text = { Text("删除网络") },
                                        onClick = { showMenu = false; showDeleteConfirm = true },
                                        leadingIcon = { Icon(Icons.Default.Delete, null, tint = MaterialTheme.colorScheme.error) }
                                    )
                                } else {
                                    // Member 菜单
                                    DropdownMenuItem(
                                        text = { Text("查看成员") },
                                        onClick = { showMenu = false; showMembers = true },
                                        leadingIcon = { Icon(Icons.Default.People, null) }
                                    )
                                    HorizontalDivider()
                                    DropdownMenuItem(
                                        text = { Text("退出网络") },
                                        onClick = { showMenu = false; showLeaveConfirm = true },
                                        leadingIcon = { Icon(Icons.Default.Logout, null, tint = MaterialTheme.colorScheme.error) }
                                    )
                                }
                            }
                        }
                    }
                    Spacer(modifier = Modifier.height(8.dp))
                    Switch(checked = isActuallyConnected, onCheckedChange = onToggle)
                }
            }
        }
    }
    
    // 对话框
    if (showMembers) {
        MembersDialog(network, viewModel, { showMembers = false })
    }
    
    if (showInvite) {
        InviteDialog(network, viewModel, { showInvite = false })
    }
    
    if (showSettings) {
        NetworkSettingsDialog(network, viewModel, { showSettings = false })
    }
    
    if (showDeleteConfirm) {
        DeleteNetworkConfirmDialog(network, viewModel, { showDeleteConfirm = false })
    }
    
    if (showLeaveConfirm) {
        LeaveNetworkConfirmDialog(network, viewModel, { showLeaveConfirm = false })
    }
}

@Composable
fun StatusTab(deviceId: String, isBound: Boolean, daemonStarted: Boolean, vpnRunning: Boolean) {
    LazyColumn(modifier = Modifier.fillMaxSize(), contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        item {
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                StatCard("设备ID", deviceId.take(12), Modifier.weight(1f))
                StatCard("状态", if (daemonStarted) "运行中" else "未运行", Modifier.weight(1f), if (daemonStarted) Color(0xFF4CAF50) else Color.Gray)
            }
        }
        item {
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                StatCard("绑定", if (isBound) "已绑定" else "未绑定", Modifier.weight(1f), if (isBound) Color(0xFF4CAF50) else Color.Gray)
                StatCard("VPN", if (vpnRunning) "运行中" else "未运行", Modifier.weight(1f), if (vpnRunning) Color(0xFF4CAF50) else Color.Gray)
            }
        }
    }
}

@Composable
fun StatCard(title: String, value: String, modifier: Modifier = Modifier, valueColor: Color = MaterialTheme.colorScheme.onSurface) {
    Card(modifier = modifier, elevation = CardDefaults.cardElevation(defaultElevation = 1.dp)) {
        Column(modifier = Modifier.padding(16.dp)) {
            Text(title, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(modifier = Modifier.height(4.dp))
            Text(value, style = MaterialTheme.typography.bodyLarge, color = valueColor, fontFamily = FontFamily.Monospace)
        }
    }
}

fun formatBytes(bytes: Long): String {
    if (bytes < 1024) return "$bytes B"
    val kb = bytes / 1024.0
    if (kb < 1024) return "%.1f KB".format(kb)
    val mb = kb / 1024.0
    if (mb < 1024) return "%.1f MB".format(mb)
    val gb = mb / 1024.0
    return "%.1f GB".format(gb)
}

@Composable
fun BindDeviceDialog(onDismiss: () -> Unit, onBind: (String, String) -> Unit) {
    var caInput by remember { mutableStateOf("") }
    var codeInput by remember { mutableStateOf("") }
    AlertDialog(onDismissRequest = onDismiss, title = { Text("绑定设备") }, text = {
        Column {
            Text("输入设备授权码以绑定到服务器", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(modifier = Modifier.height(16.dp))
            OutlinedTextField(value = caInput, onValueChange = { caInput = it }, label = { Text("CA 证书（可选）") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
            Spacer(modifier = Modifier.height(8.dp))
            OutlinedTextField(value = codeInput, onValueChange = { codeInput = it }, label = { Text("设备授权码") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
        }
    }, confirmButton = { Button(onClick = { onBind(caInput, codeInput) }, enabled = codeInput.isNotEmpty()) { Text("绑定") } }, dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } })
}

@Composable
fun JoinNetworkDialog(onDismiss: () -> Unit, onJoin: (String) -> Unit) {
    var linkInput by remember { mutableStateOf("") }
    var nidInput by remember { mutableStateOf("") }
    var codeInput by remember { mutableStateOf("") }
    AlertDialog(onDismissRequest = onDismiss, title = { Text("加入网络") }, text = {
        Column {
            Text("粘贴邀请链接或输入网络 ID 和配对码", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(modifier = Modifier.height(16.dp))
            OutlinedTextField(value = linkInput, onValueChange = { linkInput = it }, label = { Text("邀请链接") }, modifier = Modifier.fillMaxWidth(), placeholder = { Text("snet://join?nid=xxx&code=xxx") }, singleLine = true)
            Spacer(modifier = Modifier.height(16.dp))
            Text("或手动输入", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(modifier = Modifier.height(8.dp))
            OutlinedTextField(value = nidInput, onValueChange = { nidInput = it }, label = { Text("网络 ID") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
            Spacer(modifier = Modifier.height(8.dp))
            OutlinedTextField(value = codeInput, onValueChange = { codeInput = it }, label = { Text("配对码") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
        }
    }, confirmButton = { Button(onClick = { if (linkInput.isNotEmpty()) onJoin(linkInput) else if (nidInput.isNotEmpty() && codeInput.isNotEmpty()) onJoin("snet://join?nid=$nidInput&code=$codeInput") }, enabled = linkInput.isNotEmpty() || (nidInput.isNotEmpty() && codeInput.isNotEmpty())) { Text("加入") } }, dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } })
}

@Composable
fun CreateNetworkDialog(onDismiss: () -> Unit, onCreate: (String, String) -> Unit) {
    var nameInput by remember { mutableStateOf("") }
    var subnetInput by remember { mutableStateOf("10.88.0.0/24") }
    AlertDialog(onDismissRequest = onDismiss, title = { Text("创建网络") }, text = {
        Column {
            OutlinedTextField(value = nameInput, onValueChange = { nameInput = it }, label = { Text("网络名称") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
            Spacer(modifier = Modifier.height(8.dp))
            OutlinedTextField(value = subnetInput, onValueChange = { subnetInput = it }, label = { Text("子网") }, modifier = Modifier.fillMaxWidth(), singleLine = true)
        }
    }, confirmButton = { Button(onClick = { onCreate(nameInput, subnetInput) }, enabled = nameInput.isNotEmpty()) { Text("创建") } }, dismissButton = { TextButton(onClick = onDismiss) { Text("取消") } })
}