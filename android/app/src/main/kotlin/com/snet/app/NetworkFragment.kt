package com.snet.app

import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.TextView
import androidx.fragment.app.Fragment
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import com.google.android.material.button.MaterialButton
import org.json.JSONArray
import org.json.JSONObject

class NetworkFragment : Fragment() {

    private val handler = Handler(Looper.getMainLooper())
    private val pollRunnable = object : Runnable {
        override fun run() { refreshNetworks(); handler.postDelayed(this, 3000L) }
    }

    private lateinit var networkList: RecyclerView
    private lateinit var networkEmpty: LinearLayout
    private lateinit var refreshLayout: SwipeRefreshLayout
    private lateinit var adapter: NetworkAdapter

    private val networks = mutableListOf<NetworkInfo>()
    private var activeNetworkID: String? = null

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View? {
        return inflater.inflate(R.layout.fragment_network, container, false)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)

        networkList = view.findViewById(R.id.networkList)
        networkEmpty = view.findViewById(R.id.networkEmpty)
        refreshLayout = view.findViewById(R.id.networkRefresh)

        adapter = NetworkAdapter(
            onToggle = { net, wantOn -> toggleConnection(net, wantOn) },
            onClick = { net -> openDetail(net) },
            onSettings = { net -> openSettings(net) }
        )

        networkList.layoutManager = LinearLayoutManager(requireContext())
        networkList.adapter = adapter

        refreshLayout.setColorSchemeColors(requireContext().getColor(R.color.snet_accent))
        refreshLayout.setOnRefreshListener { refreshNetworks() }

        view.findViewById<MaterialButton>(R.id.btnCreateNetwork).setOnClickListener {
            (activity as? MainActivity)?.showCreateNetwork()
        }

        view.findViewById<MaterialButton>(R.id.btnJoinNetwork).setOnClickListener {
            (activity as? MainActivity)?.showJoinNetwork()
        }

        handler.post { refreshNetworks() }
    }

    override fun onResume() {
        super.onResume()
        handler.removeCallbacks(pollRunnable)
        handler.post(pollRunnable)
    }

    override fun onPause() {
        super.onPause()
        handler.removeCallbacks(pollRunnable)
    }

    fun refreshNetworks() {
        val act = activity ?: return
        Thread {
            val statusJson = SnetBridge.getStatus(act) ?: return@Thread
            try {
                val obj = JSONObject(statusJson)

                // Find active network (Go: network.active == true means connected)
                val arr: JSONArray = if (obj.has("networks") && !obj.isNull("networks"))
                    obj.getJSONArray("networks") else JSONArray()

                activeNetworkID = null
                for (i in 0 until arr.length()) {
                    val n = arr.getJSONObject(i)
                    if (n.optBoolean("active", false)) {
                        activeNetworkID = n.optString("id", null)
                        break
                    }
                }

                val list = mutableListOf<NetworkInfo>()
                for (i in 0 until arr.length()) {
                    val n = arr.getJSONObject(i)
                    val netID = n.optString("id", "")
                    val connected = netID == activeNetworkID

                    list.add(
                        NetworkInfo(
                            id = netID,
                            name = n.optString("name", netID),
                            subnet = n.optString("subnet_cidr", "--"),
                            myIP = n.optString("my_ip", "--"),
                            role = n.optString("role", "member"),
                            nodeCount = n.optInt("node_count", 0),
                            serverCount = n.optInt("server_count", 0),
                            onlineCount = n.optInt("online_count", 0),
                            pendingCount = n.optInt("pending_count", 0),
                            peerCount = n.optInt("peer_count", n.optInt("node_count", 0)),
                            connected = connected,
                            allowedSubnets = emptyList()
                        )
                    )
                }

                act.runOnUiThread {
                    networks.clear()
                    networks.addAll(list)
                    adapter.submitList(networks.toList())
                    networkEmpty.visibility = if (networks.isEmpty()) View.VISIBLE else View.GONE
                    networkList.visibility = if (networks.isEmpty()) View.GONE else View.VISIBLE
                    refreshLayout.isRefreshing = false
                }
            } catch (e: Exception) {
                act.runOnUiThread { refreshLayout.isRefreshing = false }
            }
        }.start()
    }

    private fun toggleConnection(net: NetworkInfo, wantOn: Boolean) {
        val ctx = context ?: return
        Thread {
            if (wantOn) {
                SnetBridge.connect(ctx, net.id)
            } else {
                SnetBridge.disconnect(ctx, net.id)
            }
            Thread.sleep(500)
            activity?.runOnUiThread { refreshNetworks() }
        }.start()
    }

    private fun openDetail(net: NetworkInfo) {
        val ctx = context ?: return
        val intent = Intent(ctx, NetworkDetailActivity::class.java).apply {
            putExtra(NetworkDetailActivity.EXTRA_NID, net.id)
            putExtra(NetworkDetailActivity.EXTRA_NAME, net.name)
            putExtra(NetworkDetailActivity.EXTRA_IS_OWNER, net.role == "owner")
        }
        startActivity(intent)
    }

    private fun openSettings(net: NetworkInfo) {
        val sheet = NetworkSettingsSheet.newInstance(net.id, net.name, net.role)
        sheet.show(childFragmentManager, "network_settings")
    }
}
