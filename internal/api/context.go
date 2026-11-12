package api

import (
	"context"
	"net/http"

	"github.com/mst8222/ingot-board/internal/domain"
)

// ctxKey 是本包私有的上下文键类型，避免与其他包的 string 键冲突。
type ctxKey string

const agentCtxKey ctxKey = "agent"

// contextWithAgent 把鉴权得到的 Agent 放入请求上下文。
func contextWithAgent(ctx context.Context, a *domain.Agent) context.Context {
	return context.WithValue(ctx, agentCtxKey, a)
}

// agentFromContext 取出当前请求的 Agent，未鉴权时为 nil（路由层已保证非空）。
func agentFromContext(r *http.Request) *domain.Agent {
	a, _ := r.Context().Value(agentCtxKey).(*domain.Agent)
	return a
}
