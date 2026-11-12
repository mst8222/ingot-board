package domain

import "time"

// Device 一台已配对的手机。令牌明文不进这个结构。
type Device struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	// Scopes 令牌范围（017）。nil = 0008 之前的存量行，视为全量放行；
	// 新签发设备一律携带显式范围。
	Scopes     *DeviceScopes `json:"scopes,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	LastSeenAt *time.Time    `json:"last_seen_at"`
}
