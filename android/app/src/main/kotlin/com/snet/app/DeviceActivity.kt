package com.snet.app

import android.os.Bundle
import android.widget.Button
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File
import java.text.SimpleDateFormat
import java.util.*

class DeviceActivity : AppCompatActivity() {

    private lateinit var deviceID: TextView
    private lateinit var publicKey: TextView
    private lateinit var serverAddr: TextView
    private lateinit var boundStatus: TextView
    private lateinit var networkCount: TextView
    private lateinit var configPath: TextView
    private lateinit var version: TextView
    private lateinit var refreshButton: Button

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_device)

        supportActionBar?.apply {
            title = "设备信息"
            setDisplayHomeAsUpEnabled(true)
        }

        deviceID = findViewById(R.id.deviceID)
        publicKey = findViewById(R.id.devicePublicKey)
        serverAddr = findViewById(R.id.deviceServer)
        boundStatus = findViewById(R.id.deviceBoundStatus)
        networkCount = findViewById(R.id.deviceNetworkCount)
        configPath = findViewById(R.id.deviceConfigPath)
        version = findViewById(R.id.deviceVersion)
        refreshButton = findViewById(R.id.deviceRefresh)

        refreshButton.setOnClickListener { loadDeviceInfo() }
        loadDeviceInfo()
    }

    override fun onSupportNavigateUp(): Boolean {
        finish()
        return true
    }

    private fun loadDeviceInfo() {
        lifecycleScope.launch {
            val statusJson = withContext(Dispatchers.IO) { SnetBridge.status() }
            val configJson = withContext(Dispatchers.IO) { loadConfig() }

            parseDeviceInfo(statusJson, configJson)
        }
    }

    private fun loadConfig(): String {
        return try {
            val configFile = File(filesDir, "daemon.json")
            if (configFile.exists()) {
                configFile.readText()
            } else {
                "{}"
            }
        } catch (e: Exception) {
            "{}"
        }
    }

    private fun parseDeviceInfo(statusJson: String, configJson: String) {
        try {
            val config = JSONObject(configJson)
            val status = JSONObject(statusJson)

            // Go Config fields: deviceId, privateKey, serverAddr, boundServer
            deviceID.text = config.optString("deviceId", "未绑定")
            publicKey.text = config.optString("privateKey", "未生成").let {
                if (it.length > 32) "${it.take(32)}..." else it
            }
            serverAddr.text = config.optString("serverAddr", "未配置")
            boundStatus.text = if (config.has("boundServer") && config.getString("boundServer").isNotEmpty()) {
                "已绑定"
            } else {
                "未绑定"
            }
            networkCount.text = status.optInt("networkCount", 0).toString()

            val path = filesDir.absolutePath
            configPath.text = path

            version.text = try {
                val pm = packageManager
                val pInfo = pm.getPackageInfo(packageName, 0)
                val code = if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.P) {
                    pInfo.longVersionCode
                } else {
                    @Suppress("DEPRECATION")
                    pInfo.versionCode.toLong()
                }
                "${pInfo.versionName} ($code)"
            } catch (e: Exception) {
                "0.1.0"
            }

        } catch (e: Exception) {
            Toast.makeText(this, "加载设备信息失败", Toast.LENGTH_SHORT).show()
        }
    }
}
