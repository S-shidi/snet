package com.snet.app

import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.lifecycle.viewmodel.compose.viewModel
import com.snet.app.ui.MainScreen
import com.snet.app.viewmodel.MainViewModel

class MainActivity : ComponentActivity() {
    
    private lateinit var viewModel: MainViewModel
    
    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == RESULT_OK) {
            viewModel.onVpnPermissionGranted()
        } else {
            // 用户拒绝VPN权限
            viewModel.refreshStatus()
        }
    }
    
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        
        setContent {
            SnetTheme {
                viewModel = viewModel()
                
                // 监听VPN权限请求
                val needVpnPermission by viewModel.needVpnPermission.collectAsState()
                
                LaunchedEffect(needVpnPermission) {
                    if (needVpnPermission != null) {
                        // 请求VPN权限
                        val intent = VpnService.prepare(this@MainActivity)
                        if (intent != null) {
                            vpnPermissionLauncher.launch(intent)
                        } else {
                            // 已有权限，直接继续
                            viewModel.onVpnPermissionGranted()
                        }
                    }
                }
                
                MainScreen(viewModel = viewModel)
            }
        }
        
        // 处理 Intent
        handleIntent(intent)
    }
    
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }
    
    private fun handleIntent(intent: Intent) {
        when (intent.action) {
            Intent.ACTION_VIEW -> {
                val data = intent.data
                if (data != null && data.scheme == "snet") {
                    viewModel.joinNetwork(data.toString())
                }
            }
        }
    }
}

@Composable
fun SnetTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = darkColorScheme(
            primary = Color(0xFF1E88E5),
            background = Color(0xFF0A0E14),
            surface = Color(0xFF1A1F26),
            onSurface = Color(0xFFECEFF1)
        ),
        content = content
    )
}
