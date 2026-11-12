package phone

import "net"

// suitableLAN 判断这个地址能不能给同一 Wi-Fi 上的手机用。
// Tailscale 的 100.64.0.0/10 不算局域网，远程走 HTTPS 主机名。
func suitableLAN(ip net.IP) bool {
	ip = ip.To4()
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127 {
		return false
	}
	return ip.IsPrivate()
}

// findLANIPv4 挑一块网卡上的私网 IPv4。
func findLANIPv4() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if suitableLAN(ip) {
				return ip.String(), nil
			}
		}
	}
	return "", errNoLAN
}
