package com.snet.app

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView

class LogAdapter : ListAdapter<LogEntry, LogAdapter.LogViewHolder>(LogDiffCallback()) {

    class LogViewHolder(view: View) : RecyclerView.ViewHolder(view) {
        val timeText: TextView = view.findViewById(android.R.id.text1)
        val messageText: TextView = view.findViewById(android.R.id.text2)
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): LogViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(
            android.R.layout.simple_list_item_2, parent, false
        )
        return LogViewHolder(view)
    }

    override fun onBindViewHolder(holder: LogViewHolder, position: Int) {
        val entry = getItem(position)
        val ctx = holder.itemView.context

        val eventIcon = when (entry.event) {
            "connected" -> "🟢"
            "disconnected" -> "🔴"
            "peer_joined" -> "💙"
            "peer_left" -> "⏹"
            "config_changed" -> "🔧"
            "error" -> "🔴"
            "keep_alive" -> "🟢"
            else -> "·"
        }

        holder.timeText.text = "${entry.ts}  $eventIcon"
        holder.timeText.setTextColor(ctx.getColor(R.color.snet_text_faint))
        holder.timeText.textSize = 11f

        holder.messageText.text = "${entry.event}: ${entry.message}"
        holder.messageText.setTextColor(ctx.getColor(R.color.snet_text_dim))
        holder.messageText.textSize = 13f
    }
}

class LogDiffCallback : DiffUtil.ItemCallback<LogEntry>() {
    override fun areItemsTheSame(oldItem: LogEntry, newItem: LogEntry) =
        oldItem.ts == newItem.ts && oldItem.event == newItem.event
    override fun areContentsTheSame(oldItem: LogEntry, newItem: LogEntry) = oldItem == newItem
}
