package com.snet.app

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.*
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

class NetworkDetailActivity : AppCompatActivity() {

    private lateinit var networkName: TextView
    private lateinit var networkIP: TextView
    private lateinit var networkStatus: TextView
    private lateinit var subnetList: LinearLayout
    private lateinit var subnetEmpty: TextView
    private lateinit var addSubnetButton: Button
    private lateinit var peerList: LinearLayout
    private lateinit var peerEmpty: TextView
    private lateinit var rejoinButton: Button
    private lateinit var leaveButton: Button

    private var nid: String = ""
    private var netName: String = ""
    private var isOwner: Boolean = false

    companion object {
        const val EXTRA_NID = "network_id"
        const val EXTRA_NAME = "network_name"
        const val EXTRA_IS_OWNER = "is_owner"
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_network_detail)

        nid = intent.getStringExtra(EXTRA_NID) ?: ""
        netName = intent.getStringExtra(EXTRA_NAME) ?: ""
        isOwner = intent.getBooleanExtra(EXTRA_IS_OWNER, false)

        supportActionBar?.apply {
            title = netName
            setDisplayHomeAsUpEnabled(true)
        }

        networkName = findViewById(R.id.detailNetName)
        networkIP = findViewById(R.id.detailNetIP)
        networkStatus = findViewById(R.id.detailNetStatus)
        subnetList = findViewById(R.id.subnetList)
        subnetEmpty = findViewById(R.id.subnetEmpty)
        addSubnetButton = findViewById(R.id.addSubnetButton)
        peerList = findViewById(R.id.peerList)
        peerEmpty = findViewById(R.id.peerEmpty)
        rejoinButton = findViewById(R.id.rejoinButton)
        leaveButton = findViewById(R.id.leaveButton)

        addSubnetButton.visibility = if (isOwner) View.VISIBLE else View.GONE

        addSubnetButton.setOnClickListener { showAddSubnetDialog() }
        rejoinButton.setOnClickListener { onRejoin() }
        leaveButton.setOnClickListener { onLeave() }

        findViewById<Button>(R.id.membersButton)?.setOnClickListener {
            val sheet = MembersBottomSheet.newInstance(nid, isOwner)
            sheet.show(supportFragmentManager, "members")
        }

        findViewById<Button>(R.id.pendingButton)?.setOnClickListener {
            val sheet = PendingBottomSheet.newInstance(nid, isOwner)
            sheet.show(supportFragmentManager, "pending")
        }

        findViewById<Button>(R.id.inviteButton)?.setOnClickListener {
            val sheet = InviteLinkSheet.newInstance(nid, netName)
            sheet.show(supportFragmentManager, "invite_link")
        }

        loadDetails()
    }

    override fun onSupportNavigateUp(): Boolean {
        finish()
        return true
    }

    private fun loadDetails() {
        lifecycleScope.launch {
            val infoJson = withContext(Dispatchers.IO) { SnetBridge.info(nid) }
            val peersJson = withContext(Dispatchers.IO) { SnetBridge.peers(nid) }
            parseInfo(infoJson)
            parsePeers(peersJson)
        }
    }

    private fun parseInfo(json: String) {
        try {
            val obj = JSONObject(json)
            if (obj.has("error")) {
                Toast.makeText(this, obj.getString("error"), Toast.LENGTH_SHORT).show()
                return
            }

            // Go NetworkInfoResp embeds Network: name, subnet, nodeCount, online, approvalRequired, pendingCount
            networkName.text = obj.optString("name", netName)
            networkIP.text = "子网: ${obj.optString("subnet", "?")}"
            val nodeCount = obj.optInt("nodeCount", 0)
            val pendingCount = obj.optInt("pendingCount", 0)
            val online = obj.optBoolean("online", false)
            networkStatus.text = "节点: $nodeCount | 在线: ${if (online) "是" else "否"} | 待审批: $pendingCount"

            // Go Network struct doesn't have 'ip' or 'status' — use allowedSubnets from nodes
            val subnets = obj.optJSONArray("allowedSubnets") ?: obj.optJSONArray("subnets")
            updateSubnetList(subnets)
        } catch (e: Exception) {
            networkStatus.text = "加载失败"
        }
    }

    private fun updateSubnetList(subnets: JSONArray?) {
        subnetList.removeAllViews()

        if (subnets == null || subnets.length() == 0) {
            subnetEmpty.visibility = View.VISIBLE
            subnetEmpty.text = if (isOwner) "暂无子网，点击添加" else "暂无子网"
            return
        }

        subnetEmpty.visibility = View.GONE

        for (i in 0 until subnets.length()) {
            val subnet = subnets.optString(i, "")
            if (subnet.isEmpty()) continue

            val itemView = layoutInflater.inflate(R.layout.item_subnet, subnetList, false)
            val subnetText = itemView.findViewById<TextView>(R.id.subnetText)
            val removeBtn = itemView.findViewById<Button>(R.id.subnetRemove)

            subnetText.text = subnet
            removeBtn.visibility = if (isOwner) View.VISIBLE else View.GONE

            removeBtn.setOnClickListener {
                AlertDialog.Builder(this)
                    .setTitle("删除子网")
                    .setMessage("确定要删除 $subnet 吗？")
                    .setPositiveButton("删除") { _, _ -> removeSubnet(subnet) }
                    .setNegativeButton("取消", null)
                    .show()
            }

            subnetList.addView(itemView)
        }
    }

    private fun parsePeers(json: String) {
        peerList.removeAllViews()
        try {
            val obj = JSONObject(json)
            val peers = obj.optJSONArray("peers") ?: obj.optJSONArray("nodes")

            if (peers == null || peers.length() == 0) {
                peerEmpty.visibility = View.VISIBLE
                return
            }

            peerEmpty.visibility = View.GONE

            for (i in 0 until peers.length()) {
                val peer = peers.getJSONObject(i)
                val itemView = layoutInflater.inflate(R.layout.item_peer, peerList, false)

                val peerName = itemView.findViewById<TextView>(R.id.peerName)
                val peerInfo = itemView.findViewById<TextView>(R.id.peerInfo)
                val peerStatus = itemView.findViewById<TextView>(R.id.peerStatusView)

                peerName.text = peer.optString("id", "unknown")
                peerInfo.text = "${peer.optString("ip", "?")} | ${peer.optString("publicKey", "?").take(16)}..."

                val online = peer.optBoolean("online", false)
                val lastSeen = peer.optLong("lastSeen", 0)
                peerStatus.text = if (online) "在线" else if (lastSeen > 0) "离线 (${formatTime(lastSeen)})" else "离线"
                peerStatus.setTextColor(if (online) 0xFF3fb68b.toInt() else 0xFF6b7690.toInt())

                peerList.addView(itemView)
            }
        } catch (e: Exception) {
            peerEmpty.visibility = View.VISIBLE
            peerEmpty.text = "加载失败"
        }
    }

    private fun showAddSubnetDialog() {
        val input = EditText(this).apply {
            hint = "例如: 192.168.1.0/24"
            inputType = android.text.InputType.TYPE_CLASS_TEXT
            setPadding(48, 32, 48, 16)
        }

        val detectButton = Button(this).apply {
            text = "检测本地子网"
            setPadding(48, 8, 48, 8)
        }

        val detectedText = TextView(this).apply {
            setPadding(48, 8, 48, 8)
            textSize = 12f
            visibility = View.GONE
        }

        detectButton.setOnClickListener {
            lifecycleScope.launch {
                val result = withContext(Dispatchers.IO) { SnetBridge.detectLocalSubnets() }
                try {
                    val arr = JSONArray(result)
                    if (arr.length() == 0) {
                        detectedText.text = "未检测到本地子网"
                    } else {
                        val subnets = mutableListOf<String>()
                        for (i in 0 until arr.length()) {
                            subnets.add(arr.optString(i))
                        }
                        detectedText.text = "检测到: ${subnets.joinToString(", ")}"
                        input.setText(subnets.firstOrNull() ?: "")
                    }
                    detectedText.visibility = View.VISIBLE
                } catch (e: Exception) {
                    detectedText.text = "检测失败"
                    detectedText.visibility = View.VISIBLE
                }
            }
        }

        val container = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(16, 8, 16, 8)
            addView(input)
            addView(detectButton)
            addView(detectedText)
        }

        AlertDialog.Builder(this)
            .setTitle("添加子网路由")
            .setView(container)
            .setPositiveButton("添加") { _, _ ->
                val subnet = input.text.toString().trim()
                if (subnet.isNotEmpty()) {
                    addSubnet(subnet)
                }
            }
            .setNegativeButton("取消", null)
            .show()
    }

    private fun addSubnet(subnet: String) {
        lifecycleScope.launch {
            val currentSubnets = getCurrentSubnets()
            currentSubnets.add(subnet)
            val jsonArray = JSONArray(currentSubnets.toList())

            val result = withContext(Dispatchers.IO) {
                SnetBridge.updateSubnets(nid, jsonArray.toString())
            }

            try {
                val obj = JSONObject(result)
                if (obj.has("error")) {
                    Toast.makeText(this@NetworkDetailActivity, "添加失败: ${obj.getString("error")}", Toast.LENGTH_SHORT).show()
                } else {
                    Toast.makeText(this@NetworkDetailActivity, "子网已添加", Toast.LENGTH_SHORT).show()
                    loadDetails()
                }
            } catch (e: Exception) {
                Toast.makeText(this@NetworkDetailActivity, result, Toast.LENGTH_SHORT).show()
            }
        }
    }

    private fun removeSubnet(subnet: String) {
        lifecycleScope.launch {
            val currentSubnets = getCurrentSubnets()
            currentSubnets.remove(subnet)
            val jsonArray = JSONArray(currentSubnets.toList())

            val result = withContext(Dispatchers.IO) {
                SnetBridge.updateSubnets(nid, jsonArray.toString())
            }

            try {
                val obj = JSONObject(result)
                if (obj.has("error")) {
                    Toast.makeText(this@NetworkDetailActivity, "删除失败: ${obj.getString("error")}", Toast.LENGTH_SHORT).show()
                } else {
                    Toast.makeText(this@NetworkDetailActivity, "子网已删除", Toast.LENGTH_SHORT).show()
                    loadDetails()
                }
            } catch (e: Exception) {
                Toast.makeText(this@NetworkDetailActivity, result, Toast.LENGTH_SHORT).show()
            }
        }
    }

    private suspend fun getCurrentSubnets(): MutableSet<String> {
        val infoJson = withContext(Dispatchers.IO) { SnetBridge.info(nid) }
        val subnets = mutableSetOf<String>()
        try {
            val obj = JSONObject(infoJson)
            val arr = obj.optJSONArray("allowedSubnets") ?: obj.optJSONArray("subnets")
            if (arr != null) {
                for (i in 0 until arr.length()) {
                    val s = arr.optString(i)
                    if (s.isNotEmpty()) subnets.add(s)
                }
            }
        } catch (_: Exception) {}
        return subnets
    }

    private fun onRejoin() {
        lifecycleScope.launch {
            val result = withContext(Dispatchers.IO) { SnetBridge.rejoin(nid) }
            try {
                val obj = JSONObject(result)
                if (obj.has("error")) {
                    Toast.makeText(this@NetworkDetailActivity, "重连失败: ${obj.getString("error")}", Toast.LENGTH_SHORT).show()
                } else {
                    Toast.makeText(this@NetworkDetailActivity, "已重连", Toast.LENGTH_SHORT).show()
                    loadDetails()
                }
            } catch (e: Exception) {
                Toast.makeText(this@NetworkDetailActivity, result, Toast.LENGTH_SHORT).show()
            }
        }
    }

    private fun onLeave() {
        ConfirmHelper.show(this, "确定要离开网络 $netName 吗？") {
            lifecycleScope.launch {
                val result = withContext(Dispatchers.IO) { SnetBridge.leaveNetwork(nid) }
                try {
                    val obj = JSONObject(result)
                    if (obj.has("error")) {
                        Toast.makeText(this@NetworkDetailActivity, "离开失败: ${obj.getString("error")}", Toast.LENGTH_SHORT).show()
                    } else {
                        Toast.makeText(this@NetworkDetailActivity, "已离开网络", Toast.LENGTH_SHORT).show()
                        setResult(Activity.RESULT_OK)
                        finish()
                    }
                } catch (e: Exception) {
                    Toast.makeText(this@NetworkDetailActivity, result, Toast.LENGTH_SHORT).show()
                }
            }
        }
    }

    private fun formatTime(epochSec: Long): String {
        return try {
            val sdf = java.text.SimpleDateFormat("MM-dd HH:mm", java.util.Locale.getDefault())
            sdf.format(java.util.Date(epochSec * 1000))
        } catch (e: Exception) {
            epochSec.toString()
        }
    }
}
