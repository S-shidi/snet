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
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.snet.app.model.Member
import com.snet.app.model.Network
import com.snet.app.viewmodel.MainViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle

@Composable
fun MembersDialog(
    network: Network,
    viewModel: MainViewModel,
    onDismiss: () -> Unit
) {
    val members by viewModel.members.collectAsStateWithLifecycle()
    val isLoading by viewModel.isLoading.collectAsStateWithLifecycle()
    
    LaunchedEffect(network) {
        viewModel.showNetworkDetail(network)
    }
    
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("成员列表 - ${network.name.ifEmpty { network.networkId }}") },
        text = {
            if (isLoading) {
                Box(
                    modifier = Modifier.fillMaxWidth().height(200.dp),
                    contentAlignment = Alignment.Center
                ) {
                    CircularProgressIndicator()
                }
            } else if (members.isEmpty()) {
                Box(
                    modifier = Modifier.fillMaxWidth().height(200.dp),
                    contentAlignment = Alignment.Center
                ) {
                    Text("暂无成员")
                }
            } else {
                Column {
                    Text(
                        "${members.count { it.online }}/${members.size} 在线",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                    Spacer(modifier = Modifier.height(12.dp))
                    
                    LazyColumn(
                        modifier = Modifier.height(300.dp),
                        verticalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        items(members) { member ->
                            MemberCard(member, network.owner, viewModel)
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onDismiss) {
                Text("关闭")
            }
        }
    )
}

@Composable
fun MemberCard(
    member: Member,
    isOwner: Boolean,
    viewModel: MainViewModel
) {
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
                    Icon(
                        if (member.online) Icons.Default.CheckCircle else Icons.Default.Cancel,
                        null,
                        tint = if (member.online) Color(0xFF4CAF50) else Color.Gray,
                        modifier = Modifier.size(16.dp)
                    )
                    Spacer(modifier = Modifier.width(8.dp))
                    Text(
                        text = member.deviceName ?: member.deviceId?.take(8) ?: "Unknown",
                        style = MaterialTheme.typography.bodyMedium
                    )
                    if (member.role != "member") {
                        Spacer(modifier = Modifier.width(8.dp))
                        SuggestionChip(
                            onClick = {},
                            label = { Text(member.role) },
                            modifier = Modifier.height(24.dp)
                        )
                    }
                }
                
                Spacer(modifier = Modifier.height(4.dp))
                Text(
                    text = "IP: ${member.ip}",
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
            
            // Owner可以踢出成员
            if (isOwner && member.role != "owner") {
                IconButton(onClick = {
                    // TODO: 实现踢出成员
                }) {
                    Icon(
                        Icons.Default.RemoveCircleOutline,
                        "踢出",
                        tint = MaterialTheme.colorScheme.error
                    )
                }
            }
        }
    }
}

@Composable
fun InviteDialog(
    network: Network,
    viewModel: MainViewModel,
    onDismiss: () -> Unit
) {
    val networkInfo by viewModel.networkInfo.collectAsStateWithLifecycle()
    val isLoading by viewModel.isLoading.collectAsStateWithLifecycle()
    
    LaunchedEffect(network) {
        viewModel.showNetworkDetail(network)
    }
    
    val inviteLink = networkInfo?.link ?: "snet://join?nid=${network.networkId}&code=..."
    
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("邀请成员 - ${network.name.ifEmpty { network.networkId }}") },
        text = {
            Column {
                Text(
                    "分享以下邀请链接给其他设备，即可邀请其加入网络",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                
                Spacer(modifier = Modifier.height(16.dp))
                
                // 邀请链接显示
                Card(
                    colors = CardDefaults.cardColors(
                        containerColor = MaterialTheme.colorScheme.primaryContainer
                    )
                ) {
                    Column(modifier = Modifier.padding(16.dp)) {
                        Text(
                            "邀请链接",
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onPrimaryContainer
                        )
                        Spacer(modifier = Modifier.height(8.dp))
                        Text(
                            inviteLink,
                            style = MaterialTheme.typography.bodyMedium,
                            fontFamily = FontFamily.Monospace,
                            color = MaterialTheme.colorScheme.onPrimaryContainer
                        )
                    }
                }
                
                Spacer(modifier = Modifier.height(16.dp))
                
                // 配对码
                networkInfo?.pairingCode?.let { code ->
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween
                    ) {
                        Column {
                            Text(
                                "配对码",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                            Text(
                                code,
                                style = MaterialTheme.typography.bodyLarge,
                                fontFamily = FontFamily.Monospace
                            )
                        }
                    }
                }
            }
        },
        confirmButton = {
            Button(onClick = {
                // TODO: 复制到剪贴板
            }) {
                Icon(Icons.Default.ContentCopy, null)
                Spacer(modifier = Modifier.width(8.dp))
                Text("复制链接")
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text("关闭")
            }
        }
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NetworkSettingsDialog(
    network: Network,
    viewModel: MainViewModel,
    onDismiss: () -> Unit
) {
    var name by remember { mutableStateOf(network.name) }
    var subnet by remember { mutableStateOf(network.subnet) }
    var approvalRequired by remember { mutableStateOf(network.approvalRequired) }
    var description by remember { mutableStateOf(network.description ?: "") }
    
    val isLoading by viewModel.isLoading.collectAsStateWithLifecycle()
    
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("网络设置 - ${network.name.ifEmpty { network.networkId }}") },
        text = {
            Column {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("网络名称") },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true
                )
                
                Spacer(modifier = Modifier.height(12.dp))
                
                OutlinedTextField(
                    value = subnet,
                    onValueChange = { subnet = it },
                    label = { Text("网段") },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true
                )
                
                Spacer(modifier = Modifier.height(12.dp))
                
                OutlinedTextField(
                    value = description,
                    onValueChange = { description = it },
                    label = { Text("描述（可选）") },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true
                )
                
                Spacer(modifier = Modifier.height(12.dp))
                
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Column {
                        Text("新成员需批准")
                        Text(
                            "开启后，新成员需要创建者批准才能加入",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                    Switch(
                        checked = approvalRequired,
                        onCheckedChange = { approvalRequired = it }
                    )
                }
            }
        },
        confirmButton = {
            Button(
                onClick = {
                    // TODO: 调用viewModel更新设置
                    onDismiss()
                },
                enabled = !isLoading && name.isNotEmpty()
            ) {
                if (isLoading) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(20.dp),
                        color = MaterialTheme.colorScheme.onPrimary
                    )
                } else {
                    Text("保存")
                }
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text("取消")
            }
        }
    )
}

@Composable
fun DeleteNetworkConfirmDialog(
    network: Network,
    viewModel: MainViewModel,
    onDismiss: () -> Unit
) {
    var isLoading by remember { mutableStateOf(false) }
    
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("删除网络") },
        text = {
            Column {
                Text("确定要删除网络 \"${network.name.ifEmpty { network.networkId }}\" 吗？")
                Spacer(modifier = Modifier.height(12.dp))
                Text(
                    "此操作将：",
                    style = MaterialTheme.typography.labelMedium
                )
                Text(
                    "• 所有成员断开连接",
                    style = MaterialTheme.typography.bodySmall
                )
                Text(
                    "• 网段释放",
                    style = MaterialTheme.typography.bodySmall
                )
                Text(
                    "• 不可恢复",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error
                )
            }
        },
        confirmButton = {
            Button(
                onClick = {
                    isLoading = true
                    viewModel.deleteNetwork(network.networkId)
                    onDismiss()
                },
                colors = ButtonDefaults.buttonColors(
                    containerColor = MaterialTheme.colorScheme.error
                ),
                enabled = !isLoading
            ) {
                if (isLoading) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(20.dp),
                        color = MaterialTheme.colorScheme.onError
                    )
                } else {
                    Text("删除")
                }
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text("取消")
            }
        }
    )
}

@Composable
fun LeaveNetworkConfirmDialog(
    network: Network,
    viewModel: MainViewModel,
    onDismiss: () -> Unit
) {
    var isLoading by remember { mutableStateOf(false) }
    
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("退出网络") },
        text = {
            Column {
                Text("确定要退出网络 \"${network.name.ifEmpty { network.networkId }}\" 吗？")
                Spacer(modifier = Modifier.height(12.dp))
                Text(
                    "退出后需要重新扫码或使用邀请链接才能加入",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        },
        confirmButton = {
            Button(
                onClick = {
                    isLoading = true
                    viewModel.leaveNetwork(network.networkId)
                    onDismiss()
                },
                colors = ButtonDefaults.buttonColors(
                    containerColor = MaterialTheme.colorScheme.error
                ),
                enabled = !isLoading
            ) {
                if (isLoading) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(20.dp),
                        color = MaterialTheme.colorScheme.onError
                    )
                } else {
                    Text("退出")
                }
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text("取消")
            }
        }
    )
}