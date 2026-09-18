package com.snet.app.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.snet.app.viewmodel.MainViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    viewModel: MainViewModel,
    onDismiss: () -> Unit
) {
    val isBound by viewModel.isBound.collectAsStateWithLifecycle()
    val isLoading by viewModel.isLoading.collectAsStateWithLifecycle()
    
    // 设置状态
    var serverAddr by remember { mutableStateOf("https://snet.uizhi.eu.org:8090") }
    var authCode by remember { mutableStateOf("") }
    var caCert by remember { mutableStateOf("") }
    var wgPort by remember { mutableStateOf("51820") }
    
    var message by remember { mutableStateOf<String?>(null) }
    
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("设置") },
                navigationIcon = {
                    IconButton(onClick = onDismiss) {
                        Icon(Icons.Default.ArrowBack, "返回")
                    }
                }
            )
        }
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp)
        ) {
            // 服务器配置区块
            Card(
                modifier = Modifier.fillMaxWidth(),
                elevation = CardDefaults.cardElevation(defaultElevation = 2.dp)
            ) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text(
                        text = "服务器配置",
                        style = MaterialTheme.typography.titleMedium
                    )
                    Spacer(modifier = Modifier.height(16.dp))
                    
                    // 设备授权码
                    OutlinedTextField(
                        value = authCode,
                        onValueChange = { authCode = it },
                        label = { Text("设备授权码") },
                        placeholder = { Text("管理端生成的授权码") },
                        modifier = Modifier.fillMaxWidth(),
                        singleLine = true
                    )
                    
                    Spacer(modifier = Modifier.height(12.dp))
                    
                    // CA证书
                    OutlinedTextField(
                        value = caCert,
                        onValueChange = { caCert = it },
                        label = { Text("CA 证书路径") },
                        placeholder = { Text("公共证书留空；自签名服务器填证书路径") },
                        modifier = Modifier.fillMaxWidth(),
                        singleLine = true
                    )
                    
                    Spacer(modifier = Modifier.height(12.dp))
                    
                    // 链接按钮
                    Button(
                        onClick = {
                            if (authCode.isEmpty()) {
                                message = "请输入设备授权码"
                                return@Button
                            }
                            
                            val port = wgPort.toIntOrNull()
                            if (port == null || port < 1024 || port > 65535) {
                                message = "WireGuard 端口必须在 1024-65535 之间"
                                return@Button
                            }
                            
                            viewModel.bindDevice(caCert, authCode)
                            message = "绑定成功"
                        },
                        modifier = Modifier.fillMaxWidth(),
                        enabled = !isLoading && authCode.isNotEmpty()
                    ) {
                        if (isLoading) {
                            CircularProgressIndicator(
                                modifier = Modifier.size(20.dp),
                                color = MaterialTheme.colorScheme.onPrimary
                            )
                        } else {
                            Text(if (isBound) "更换授权码" else "链接服务器")
                        }
                    }
                    
                    // 绑定状态
                    if (isBound) {
                        Spacer(modifier = Modifier.height(8.dp))
                        Row(
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            Icon(
                                Icons.Default.CheckCircle,
                                "已绑定",
                                tint = MaterialTheme.colorScheme.primary,
                                modifier = Modifier.size(20.dp)
                            )
                            Spacer(modifier = Modifier.width(8.dp))
                            Text(
                                "已绑定到服务器",
                                style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.primary
                            )
                        }
                    }
                }
            }
            
            // WireGuard 配置
            Card(
                modifier = Modifier.fillMaxWidth(),
                elevation = CardDefaults.cardElevation(defaultElevation = 2.dp)
            ) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text(
                        text = "WireGuard 配置",
                        style = MaterialTheme.typography.titleMedium
                    )
                    Spacer(modifier = Modifier.height(16.dp))
                    
                    OutlinedTextField(
                        value = wgPort,
                        onValueChange = { 
                            if (it.isEmpty() || it.all { c -> c.isDigit() }) {
                                wgPort = it
                            }
                        },
                        label = { Text("WireGuard 端口") },
                        modifier = Modifier.fillMaxWidth(),
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        supportingText = { Text("端口范围：1024-65535") }
                    )
                }
            }
            
            // 提示消息
            message?.let { msg ->
                Card(
                    colors = CardDefaults.cardColors(
                        containerColor = if (msg.contains("成功")) 
                            MaterialTheme.colorScheme.primaryContainer 
                        else 
                            MaterialTheme.colorScheme.errorContainer
                    )
                ) {
                    Row(
                        modifier = Modifier.padding(16.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Icon(
                            if (msg.contains("成功")) Icons.Default.CheckCircle else Icons.Default.Error,
                            null,
                            tint = if (msg.contains("success"))
                                MaterialTheme.colorScheme.onPrimaryContainer
                            else
                                MaterialTheme.colorScheme.onErrorContainer
                        )
                        Spacer(modifier = Modifier.width(12.dp))
                        Text(
                            msg,
                            color = if (msg.contains("成功"))
                                MaterialTheme.colorScheme.onPrimaryContainer
                            else
                                MaterialTheme.colorScheme.onErrorContainer
                        )
                    }
                }
            }
        }
    }
}