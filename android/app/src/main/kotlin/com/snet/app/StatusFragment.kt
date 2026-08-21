package com.snet.app

import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.fragment.app.Fragment
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import org.json.JSONObject

class StatusFragment : Fragment() {

    private val handler = Handler(Looper.getMainLooper())
    private val pollRunnable = object : Runnable {
        override fun run() { refreshStatus(); handler.postDelayed(this, 3000L) }
    }

    private lateinit var trafficUp: TextView
    private lateinit var trafficDown: TextView
    private lateinit var totalTraffic: TextView
    private lateinit var refreshLayout: SwipeRefreshLayout

    private lateinit var svcWireGuardDot: View
    private lateinit var svcWireGuardText: TextView
    private lateinit var svcBindDot: View
    private lateinit var svcBindText: TextView
    private lateinit var svcTunDot: View
    private lateinit var svcTunText: TextView

    override fun onCreateView(inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?): View? {
        return inflater.inflate(R.layout.fragment_status, container, false)
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)

        trafficUp = view.findViewById(R.id.trafficUp)
        trafficDown = view.findViewById(R.id.trafficDown)
        totalTraffic = view.findViewById(R.id.totalTraffic)
        refreshLayout = view.findViewById(R.id.statusRefresh)

        svcWireGuardDot = view.findViewById(R.id.svcWireGuardDot)
        svcWireGuardText = view.findViewById(R.id.svcWireGuardText)
        svcBindDot = view.findViewById(R.id.svcBindDot)
        svcBindText = view.findViewById(R.id.svcBindText)
        svcTunDot = view.findViewById(R.id.svcTunDot)
        svcTunText = view.findViewById(R.id.svcTunText)

        refreshLayout.setColorSchemeColors(requireContext().getColor(R.color.snet_accent))
        refreshLayout.setOnRefreshListener { refreshStatus() }

        handler.post { refreshStatus() }
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

    private fun formatBytes(bytes: Long): String {
        if (bytes < 1024) return "$bytes B"
        if (bytes < 1024 * 1024) return "${bytes / 1024} KB"
        return "${"%.1f".format(bytes / (1024.0 * 1024.0))} MB"
    }

    private fun refreshStatus() {
        val act = activity ?: return
        Thread {
            val statusJson = SnetBridge.getStatus(act) ?: return@Thread
            try {
                val status = JSONObject(statusJson)

                // Go status doesn't have bytes_sent/bytes_recv at top level
                // Traffic is per-tunnel in peerStats — show 0 for now
                val sent: Long = 0
                val recv: Long = 0

                val services = status.optJSONObject("services")
                val wgRunning = services?.optBoolean("wireguard_running", false) ?: false
                val tunRunning = services?.optBoolean("tun_running", false) ?: false
                val bound = status.optBoolean("bound", false)

                act.runOnUiThread {
                    trafficUp.text = "↑ ${formatBytes(sent)}"
                    trafficDown.text = "↓ ${formatBytes(recv)}"
                    totalTraffic.text = "共 ${formatBytes(sent + recv)}"

                    setServiceStatus(svcWireGuardDot, svcWireGuardText, wgRunning)
                    setServiceStatus(svcBindDot, svcBindText, bound)
                    setServiceStatus(svcTunDot, svcTunText, tunRunning)

                    refreshLayout.isRefreshing = false
                }
            } catch (e: Exception) {
                act.runOnUiThread { refreshLayout.isRefreshing = false }
            }
        }.start()
    }

    private fun setServiceStatus(dot: View, text: TextView, running: Boolean) {
        val ctx = requireContext()
        if (running) {
            dot.backgroundTintList = android.content.res.ColorStateList.valueOf(ctx.getColor(R.color.snet_ok))
            text.text = "运行中"
            text.setTextColor(ctx.getColor(R.color.snet_ok))
        } else {
            dot.backgroundTintList = android.content.res.ColorStateList.valueOf(ctx.getColor(R.color.snet_err))
            text.text = "未运行"
            text.setTextColor(ctx.getColor(R.color.snet_err))
        }
    }
}
