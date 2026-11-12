package io.ingotboard.phone

import android.net.Uri
import java.net.InetAddress

/**
 * 远程只接受 Tailscale Funnel 的 HTTPS。
 * 局域网只接受私网 IPv4 的 HTTP，不接受 Tailscale 的 100.64.0.0/10。
 */
object UrlPolicy {
    fun accept(raw: String): Boolean {
        val uri = Uri.parse(raw.trim())
        val scheme = uri.scheme ?: return false
        val host = uri.host ?: return false
        return when (scheme) {
            "https" -> host == "ts.net" || host.endsWith(".ts.net")
            "http" -> isPrivateLan(host)
            else -> false
        }
    }

    private fun isPrivateLan(host: String): Boolean {
        val addr = runCatching { InetAddress.getByName(host) }.getOrNull() ?: return false
        val bytes = addr.address
        if (bytes.size != 4) return false
        val a = bytes[0].toInt() and 0xff
        val b = bytes[1].toInt() and 0xff
        if (a == 10) return true
        if (a == 192 && b == 168) return true
        if (a == 172 && b in 16..31) return true
        if (a == 100 && b in 64..127) return false
        return false
    }
}
