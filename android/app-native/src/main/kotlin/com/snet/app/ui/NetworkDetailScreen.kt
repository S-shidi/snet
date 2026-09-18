package com.snet.app.ui

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
import androidx.compose.ui.unit.dp
import com.snet.app.model.*
import com.snet.app.viewmodel.MainViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NetworkDetailScreen(
    network: Network,
    viewModel: MainViewModel,
    onDismiss: () -> Unit
) {
    val networkInfo by viewModel.networkInfo.collectAsStateWithLifecycle()
    val members by viewModel.members.collectAsStateWithLifecycle()
    val isLoading by viewModel.isLoading.collectAsStateWithLifecycle()
    
    var showDeleteConfirm by remember { mutableStateOf(false) }
    
    LaunchedEffect(network) {
        viewModel.showNetworkDetail(network)
    }
    
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(network.name.ifEmpty { network.networkId }) },
                navigationIcon = {
                    IconButton(onClick = onDismiss) {
                        Icon(
                            Icons.Default.ArrowBack,
                            contentDescription = "返回",
                            modifier = Modifier.size(24.dp)
                        )
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(
                    navigationIconContentColor = MaterialTheme.colorScheme.primary
                ),
                actions = {
                    // 删除/退出按钮
                    IconButton(onClick = { showDeleteConfirm = true }) {
                        Icon(
                            if (network.owner) Icons.Default.Delete else Icons.Default.Logout,
                            if (network.owner) "删除" else "退出"
                        )
                    }
                }
            )
        }
    ) { padding ->
        if (isLoading) {
            Box(
                modifier = Modifier.fillMaxSize(),
                contentAlignment = Alignment.Center
            ) {
                CircularProgressIndicator()
            }
        } else {
            LazyColumn(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(padding),
                contentPadding = PaddingValues(16.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp)
            ) {
                // 网络信息
                item {
                    Card(
                        modifier = Modifier.fillMaxWidth(),
                        elevation = CardDefaults.cardElevation(defaultElevation = 2.dp)
                    ) {
                        Column(modifier = Modifier.padding(16.dp)) {
                            Text(
                                text = "网络信息",
                                style = MaterialTheme.typography.titleMedium
                            )
                            Spacer(modifier = Modifier.height(12.dp))
                            
                            Row(
                                modifier = Modifier.fillMaxWidth(),
                                horizontalArrangement = Arrangement.SpaceBetween
                            ) {
                                Column {
                                    Text(
                                        text = "网络 ID",
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant
                                    )
                                    Text(
                                        text = network.networkId,
                                        style = MaterialTheme.typography.bodyMedium
                                    )
                                }
                                
                                Column {
                                    Text(
                                        text = "状态",
                                        style = MaterialTheme.typography.labelSmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant
                                    )
                                    Text(
                                        text = if (network.isActive) "已连接" else "未连接",
                                        style = MaterialTheme.typography.bodyMedium,
                                        color = if (network.isActive) Color(0xFF4CAF50) else Color.Gray
                                    )
                                }
                            }
                            
                            Spacer(modifier = Modifier.height(8.dp))
                            
                            Text(
                                text = "IP: ${network.ip ?: "-"}",
                                style = MaterialTheme.typography.bodyMedium
                            )
                            Text(
                                text = "子网: ${network.subnet}",
                                style = MaterialTheme.typography.bodyMedium
                            )
                            
                            if (network.description != null) {
                                Spacer(modifier = Modifier.height(8.dp))
                                Text(
                                    text = "描述: ${network.description}",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant
                                )
                            }
                        }
                    }
                }
                
                // 成员列表
                if (members.isNotEmpty()) {
                    item {
                        Card(
                            modifier = Modifier.fillMaxWidth(),
                            elevation = CardDefaults.cardElevation(defaultElevation = 2.dp)
                        ) {
                            Column(modifier = Modifier.padding(16.dp)) {
                                Row(
                                    modifier = Modifier.fillMaxWidth(),
                                    horizontalArrangement = Arrangement.SpaceBetween,
                                    verticalAlignment = Alignment.CenterVertically
                                ) {
                                    Text(
                                        text = "成员 (${members.size})",
                                        style = MaterialTheme.typography.titleMedium
                                    )
                                    Text(
                                        text = "${members.count { it.online }} 在线",
                                        style = MaterialTheme.typography.bodySmall,
                                        color = Color(0xFF4CAF50)
                                    )
                                }
                            }
                        }
                    }
                    
                    items(members) { member ->
                        MemberCard(member = member)
                    }
                }
            }
        }
        
        // 删除确认对话框
        if (showDeleteConfirm) {
            AlertDialog(
                onDismissRequest = { showDeleteConfirm = false },
                title = { Text(if (network.owner) "删除网络" else "退出网络") },
                text = {
                    Text(
                        if (network.owner)
                            "确定要删除网络 ${network.name}? 此操作不可恢复。"
                        else
                            "确定要退出网络 ${network.name}?"
                    )
                },
                confirmButton = {
                    Button(
                        onClick = {
                            if (network.owner) {
                                viewModel.deleteNetwork(network.networkId)
                            } else {
                                viewModel.leaveNetwork(network.networkId)
                            }
                            showDeleteConfirm = false
                            onDismiss()
                        },
                        colors = ButtonDefaults.buttonColors(
                            containerColor = MaterialTheme.colorScheme.error
                        )
                    ) {
                        Text(if (network.owner) "删除" else "退出")
                    }
                },
                dismissButton = {
                    TextButton(onClick = { showDeleteConfirm = false }) {
                        Text("取消")
                    }
                }
            )
        }
    }
}

@Composable
fun MemberCard(member: Member) {
    Card(
        modifier = Modifier.fillMaxWidth(),
        elevation = CardDefaults.cardElevation(defaultElevation = 1.dp)
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(12.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Column(modifier = Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        text = member.deviceName ?: member.deviceId?.take(8) ?: "Unknown",
                        style = MaterialTheme.typography.bodyMedium
                    )
                    Spacer(modifier = Modifier.width(8.dp))
                    SuggestionChip(
                        onClick = {},
                        label = { Text(member.role) },
                        modifier = Modifier.height(24.dp)
                    )
                }
                
                Spacer(modifier = Modifier.height(4.dp))
                Text(
                    text = "IP: ${member.ip}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                
                Text(
                    text = if (member.online) "在线" else "离线",
                    style = MaterialTheme.typography.bodySmall,
                    color = if (member.online) Color(0xFF4CAF50) else Color.Gray
                )
            }
        }
    }
}
