package phone

import "fmt"

// listenPlan 是一次手机监听要绑定的地址。
// FunnelAddr 在回环上，只因为 tailscaled 从本机把公网流量转进来；
// 它和桌面免登录端口不是同一个监听，处理器仍然要求设备令牌。
type listenPlan struct {
	LANAddr    string
	FunnelAddr string
	TLSAddr    string
	PublicURL  string
	LANURL     string
}

func planPhoneListeners(port int, lanIP, tsIP, dns string, lan, remote bool) listenPlan {
	var p listenPlan
	if port <= 0 {
		return p
	}
	if lan && lanIP != "" {
		p.LANAddr = fmt.Sprintf("%s:%d", lanIP, port)
		p.LANURL = "http://" + p.LANAddr
	}
	if remote && tsIP != "" && dns != "" {
		p.TLSAddr = fmt.Sprintf("%s:%d", tsIP, port)
		p.FunnelAddr = fmt.Sprintf("127.0.0.1:%d", port)
		p.PublicURL = fmt.Sprintf("https://%s:%d", dns, port)
	}
	return p
}
