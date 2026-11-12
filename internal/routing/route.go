// Package routing 按类别、档位和额度选一个 Agent，并调用决策模型做分类。
package routing

import (
	"errors"
	"fmt"
	"sort"

	"github.com/mst8222/ingot-board/internal/domain"
)

// ErrNoAgent 没有任何还能接单的 Agent。
var ErrNoAgent = errors.New("没有可用额度，无法指派")

// Decision 分类结果。
//
// CostBand 是 032 总管让分类模型顺带给出的成本档位（low/mid/high）。
// 它只影响预算预估，**不参与选人**；空值表示模型没给，由启发式兜底。
type Decision struct {
	Category string
	Tier     string
	Reason   string
	CostBand string
}

// Candidate 参与选人的 Agent。
type Candidate struct {
	ID       int64
	Name     string
	Enabled  bool
	Quota    string
	Serves   string
	CostRank int
}

// Normalize 把模型给出的类别和档位收成合法值。
// 编程任务若被标成 small，抬到 floor。
func Normalize(category, tier string) (Decision, error) {
	if !domain.ValidCategory(category) || !domain.ValidTier(tier) {
		return Decision{}, fmt.Errorf("分类结果不合法: %s/%s", category, tier)
	}
	if domain.ProgrammingCategory(category) && tier == domain.TierSmall {
		tier = domain.TierFloor
	}
	return Decision{Category: category, Tier: tier}, nil
}

// Keywords 在决策服务都没连上时，按标题和描述里的词给默认档。
func Keywords(text string) Decision {
	switch {
	case containsAny(text, "游戏", "godot", "unity", "关卡"):
		return Decision{Category: domain.CategoryCodeGame, Tier: domain.TierLarge, Reason: "未连上决策模型，按关键词定为做游戏"}
	case containsAny(text, "文案", "文档", "翻译", "本地化"):
		return Decision{Category: domain.CategoryCopy, Tier: domain.TierSmall, Reason: "未连上决策模型，按关键词定为文案"}
	case containsAny(text, "安装", "配置", "整理", "本机"):
		return Decision{Category: domain.CategoryLocalOps, Tier: domain.TierSmall, Reason: "未连上决策模型，按关键词定为本机操作"}
	default:
		return Decision{Category: domain.CategoryCodeTool, Tier: domain.TierFloor, Reason: "未连上决策模型，按关键词定为做工具"}
	}
}

func containsAny(text string, words ...string) bool {
	for _, w := range words {
		if w != "" && indexFold(text, w) >= 0 {
			return true
		}
	}
	return false
}

func indexFold(s, sub string) int {
	rs := []rune(s)
	rw := []rune(sub)
	if len(rw) == 0 || len(rs) < len(rw) {
		return -1
	}
	for i := 0; i+len(rw) <= len(rs); i++ {
		ok := true
		for j := range rw {
			a, b := rs[i+j], rw[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// Pick 选一个 Agent。Prefer 有值且额度没用尽时尊重人类指定。
func Pick(category, tier string, prefer *int64, exclude map[int64]bool, agents []Candidate) (Candidate, string, error) {
	d, err := Normalize(category, tier)
	if err != nil {
		return Candidate{}, "", err
	}
	tier = d.Tier
	eligible := func(a Candidate) bool {
		if !a.Enabled || a.Quota == domain.QuotaExhausted {
			return false
		}
		if exclude[a.ID] {
			return false
		}
		return true
	}
	if prefer != nil {
		for _, a := range agents {
			if a.ID == *prefer && eligible(a) {
				return a, "人类指定", nil
			}
		}
	}
	pool := make([]Candidate, 0, len(agents))
	for _, a := range agents {
		if eligible(a) && servesTier(a.Serves, tier) {
			pool = append(pool, a)
		}
	}
	reason := "按档位与成本选择"
	if len(pool) == 0 && tier == domain.TierLarge {
		for _, a := range agents {
			if eligible(a) && a.Serves == domain.ServesFloor {
				pool = append(pool, a)
			}
		}
		reason = "大模型额度不可用，降到 DeepSeek V4.1 Flash"
	}
	if len(pool) == 0 && tier == domain.TierSmall {
		for _, a := range agents {
			if eligible(a) && a.Serves == domain.ServesFloor {
				pool = append(pool, a)
			}
		}
		reason = "小模型执行端不可用，交给底线"
	}
	if len(pool) == 0 {
		return Candidate{}, "", ErrNoAgent
	}
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].CostRank != pool[j].CostRank {
			return pool[i].CostRank < pool[j].CostRank
		}
		return pool[i].ID < pool[j].ID
	})
	return pool[0], reason, nil
}

func servesTier(serves, tier string) bool {
	switch tier {
	case domain.TierSmall:
		return serves == domain.ServesSmall || serves == domain.ServesFloor
	case domain.TierFloor:
		return serves == domain.ServesFloor
	case domain.TierLarge:
		return serves == domain.ServesLarge
	default:
		return false
	}
}
