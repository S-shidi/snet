package com.snet.app

import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.ImageButton
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import com.google.android.material.tabs.TabLayout
import org.json.JSONObject

class MainActivity : AppCompatActivity() {

    private lateinit var headerStatusDot: View
    private lateinit var headerStatusText: TextView
    private lateinit var headerDeviceId: TextView
    private lateinit var fragmentNetwork: View
    private lateinit var fragmentStatus: View
    private lateinit var tabLayout: TabLayout
    private var activeTab = 0

    private var networkFragment = NetworkFragment()
    private var statusFragment = StatusFragment()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        // CRITICAL: Initialize SnetBridge before any fragment loads
        if (SnetBridge.getStatus(this) == null) {
            SnetBridge.init(filesDir.absolutePath)
        }

        headerStatusDot = findViewById(R.id.headerStatusDot)
        headerStatusText = findViewById(R.id.headerStatusText)
        headerDeviceId = findViewById(R.id.headerDeviceId)
        fragmentNetwork = findViewById(R.id.fragmentNetwork)
        fragmentStatus = findViewById(R.id.fragmentStatus)
        tabLayout = findViewById(R.id.tabLayout)

        // Setup tabs
        tabLayout.addTab(tabLayout.newTab().setText("网络"))
        tabLayout.addTab(tabLayout.newTab().setText("状态"))

        tabLayout.addOnTabSelectedListener(object : TabLayout.OnTabSelectedListener {
            override fun onTabSelected(tab: TabLayout.Tab) {
                activeTab = tab.position
                when (tab.position) {
                    0 -> { fragmentNetwork.visibility = View.VISIBLE; fragmentStatus.visibility = View.GONE }
                    1 -> { fragmentNetwork.visibility = View.GONE; fragmentStatus.visibility = View.VISIBLE }
                }
            }
            override fun onTabUnselected(tab: TabLayout.Tab) {}
            override fun onTabReselected(tab: TabLayout.Tab) {}
        })

        // Setup fragments
        supportFragmentManager.beginTransaction()
            .replace(R.id.fragmentNetwork, networkFragment)
            .replace(R.id.fragmentStatus, statusFragment)
            .commit()

        // Settings button
        findViewById<ImageButton>(R.id.settingsButton).setOnClickListener {
            startActivity(Intent(this, DeviceActivity::class.java))
        }
    }

    override fun onResume() {
        super.onResume()
        refreshHeader()
    }

    fun refreshHeader() {
        Thread {
            val act = this@MainActivity
            val statusJson = SnetBridge.getStatus(act) ?: return@Thread
            try {
                val status = JSONObject(statusJson)
                val bound = status.optBoolean("bound", false)
                val deviceID = SnetBridge.getDeviceID(act)
                val networkCount = status.optInt("network_count", 0)
                val connected = status.optBoolean("connected", false)

                val statusLabel: String
                val statusColor: Int
                val statusDotColor: Int
                when {
                    bound && connected -> {
                        statusLabel = "已连接 · $networkCount 网络"
                        statusColor = R.color.snet_ok
                        statusDotColor = R.color.snet_ok
                    }
                    bound -> {
                        statusLabel = "已绑定 · $networkCount 网络"
                        statusColor = R.color.snet_warn
                        statusDotColor = R.color.snet_warn
                    }
                    else -> {
                        statusLabel = "未绑定"
                        statusColor = R.color.snet_err
                        statusDotColor = R.color.snet_err
                    }
                }

                runOnUiThread {
                    headerStatusText.text = statusLabel
                    headerStatusText.setTextColor(getColor(statusColor))
                    headerDeviceId.text = deviceID ?: ""
                    headerStatusDot.backgroundTintList =
                        android.content.res.ColorStateList.valueOf(getColor(statusDotColor))
                }
            } catch (_: Exception) {}
        }.start()
    }

    fun showCreateNetwork() {
        val sheet = CreateNetworkSheet.newInstance()
        sheet.show(supportFragmentManager, "create_network")
    }

    fun showJoinNetwork() {
        val sheet = JoinNetworkSheet.newInstance()
        sheet.show(supportFragmentManager, "join_network")
    }
}
