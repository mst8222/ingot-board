package dispatcher

import (
	"fmt"
	"sort"
)

// HandoffCandidate 是 028 转交面板上的一个推荐目标。
// 总管只排序、只给理由，绝不调用 ApplyRoute / Reassign —— 最终目标由人选定。
type HandoffCandidate struct {
	AgentID int64   `json:"agent_id"`
	Name    string  `json:"name"`
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
}

// RecommendHandoff 按能力与额度给出排序候选，排除当前指派者与硬门禁不合格者。
// 纯函数：不读写数据库、不发 SSE、不改任务。
func RecommendHandoff(category, tier string, current *int64, candidates []Candidate) []HandoffCandidate {
	estimate := EstimateCost(tier, "")
	out := make([]HandoffCandidate, 0, len(candidates))
	for _, c := range candidates {
		if current != nil && c.ID == *current {
			continue
		}
		if err := HardGate(c, category, estimate); err != nil {
			continue
		}
		balanceNote := "余额未录入"
		if c.Balance != nil {
			balanceNote = fmt.Sprintf("余额 %.2f", *c.Balance)
		}
		out = append(out, HandoffCandidate{
			AgentID: c.ID,
			Name:    c.Name,
			Score:   score(c, tier, estimate, nil).Total,
			Reason: fmt.Sprintf("档位 %s · 成本序 %d · 额度 %s · %s",
				c.Serves, c.CostRank, c.Quota, balanceNote),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].AgentID < out[j].AgentID
	})
	return out
}
