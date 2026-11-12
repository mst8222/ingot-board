package app

import (
	"context"

	"github.com/mst8222/ingot-board/internal/domain"
)

// 设备上下文（017）。
//
// 键与读写助手原本在 api 包，但 017 的项目授权判定在 app 层做 ——
// app 不能反向 import api（会循环），所以键下沉到这里。
// api 的 deviceAuth 用 WithDevice 放入设备，app 用例用 DeviceFromContext 取。

type deviceCtxKey struct{}

// WithDevice 把已认证的设备放进上下文。回环路由没有设备，不放。
func WithDevice(ctx context.Context, dev *domain.Device) context.Context {
	return context.WithValue(ctx, deviceCtxKey{}, dev)
}

// DeviceFromContext 取设备；回环路由（无设备）返回 nil。
func DeviceFromContext(ctx context.Context) *domain.Device {
	dev, _ := ctx.Value(deviceCtxKey{}).(*domain.Device)
	return dev
}
