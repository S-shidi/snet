package com.snet.app

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.ImageButton
import android.widget.TextView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView

class NetworkAdapter(
    private val onToggle: (NetworkInfo, Boolean) -> Unit,
    private val onClick: (NetworkInfo) -> Unit,
    private val onSettings: (NetworkInfo) -> Unit
) : ListAdapter<NetworkInfo, NetworkAdapter.NetworkViewHolder>(NetworkDiffCallback()) {

    class NetworkViewHolder(view: View) : RecyclerView.ViewHolder(view) {
        val nameText: TextView = view.findViewById(R.id.netName)
        val statusText: TextView = view.findViewById(R.id.netStatus)
        val toggle: FrameLayout = view.findViewById(R.id.netToggle)
        val toggleTrack: View = view.findViewById(R.id.netToggleTrack)
        val toggleThumb: View = view.findViewById(R.id.netToggleThumb)
        val subnetText: TextView = view.findViewById(R.id.netSubnet)
        val peersText: TextView = view.findViewById(R.id.netPeers)
        val serversText: TextView = view.findViewById(R.id.netServers)
        val settingsBtn: ImageButton = view.findViewById(R.id.netSettingsBtn)
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): NetworkViewHolder {
        val view = LayoutInflater.from(parent.context)
            .inflate(R.layout.item_network_card, parent, false)
        return NetworkViewHolder(view)
    }

    override fun onBindViewHolder(holder: NetworkViewHolder, position: Int) {
        val network = getItem(position)
        val ctx = holder.itemView.context

        holder.nameText.text = network.name
        holder.subnetText.text = "${network.subnet}  ·  我的 IP: ${network.myIP}"

        // Status pill
        val statusText: String
        val statusBg: Int
        val statusColor: Int
        when {
            network.connected -> {
                statusText = "已连接"
                statusBg = R.drawable.bg_pill_ok
                statusColor = R.color.snet_ok
            }
            network.pendingCount > 0 && network.role == "owner" -> {
                statusText = "待审批 ${network.pendingCount}"
                statusBg = R.drawable.bg_pill_warn
                statusColor = R.color.snet_warn
            }
            else -> {
                statusText = "已断开"
                statusBg = R.drawable.bg_pill_off
                statusColor = R.color.snet_text_faint
            }
        }
        holder.statusText.text = statusText
        holder.statusText.setBackgroundResource(statusBg)
        holder.statusText.setTextColor(ctx.getColor(statusColor))

        // Toggle state
        holder.toggleTrack.setBackgroundResource(
            if (network.connected) R.drawable.bg_toggle_track_active else R.drawable.bg_toggle_track
        )
        val thumbMarginStart = if (network.connected) 22 else 2
        (holder.toggleThumb.layoutParams as FrameLayout.LayoutParams).marginStart =
            ctx.resources.displayMetrics.density.toInt() * thumbMarginStart
        holder.toggleThumb.requestLayout()

        // Peer/server counts
        holder.peersText.text = "${network.peerCount} 节点"
        holder.serversText.text = "中继 ${network.serverCount}"

        // Click handlers
        holder.toggle.setOnClickListener {
            onToggle(network, !network.connected)
        }

        holder.itemView.setOnClickListener {
            onClick(network)
        }

        holder.settingsBtn.setOnClickListener {
            onSettings(network)
        }
    }
}

class NetworkDiffCallback : DiffUtil.ItemCallback<NetworkInfo>() {
    override fun areItemsTheSame(oldItem: NetworkInfo, newItem: NetworkInfo) = oldItem.id == newItem.id
    override fun areContentsTheSame(oldItem: NetworkInfo, newItem: NetworkInfo) = oldItem == newItem
}
