// Package dispatcher 是 032 的总管：确定性硬门禁与打分负责选人，
// LLM 只通过 routing.Decision.CostBand 提供成本档位，绝不指定 Agent。
//
// 刻意不扩展 internal/routing：Pick 是 011 的纯函数且被回归测试直接断言，
// 这里把它当成不可改动的选人原语，预算门禁放在 Pick **之后**。
package dispatcher

import (
	"errors"
	"math"
	"sort"

	"github.com/mst8222/ingot-board/internal/domain"
	"github.com/mst8222/ingot-board/internal/routing"
)

const (
	ModeObserve = "observe"
	ModeAuto    = "auto"
)

var (
	errIneligible  = errors.New("执行端不可用或额度已尽")
	errCapability  = errors.New("执行端能力标签不匹配")
)

// Candidate 是总管眼里的一个执行端。
// Balance 为 nil 表示「余额未录入」，硬门禁跳过，只在建议里提示。
type Candidate struct {
	ID            int64
	Name          string
	Enabled       bool
	Quota         string
	Serves        string
	CostRank      int
	Capabilities  []string
	Online        bool
	Load          int
	Balance       *float64
}

func (c Candidate) routing() routing.Candidate {
	return routing.Candidate{
		ID: c.ID, Name: c.Name, Enabled: c.Enabled,
		Quota: c.Quota, Serves: c.Serves, CostRank: c.CostRank,
	}
}

// PlanInput 一次派发决策的全部输入，不含 IO。
type PlanInput struct {
	Category, Tier, Reason, CostBand string
	Prefer                           *int64
	Candidates                       []Candidate
	// EstimatedCost 非空时覆盖启发式估算（031 计划可预置）。
	EstimatedCost float64
}

// Score 是建议卡上展示的评分明细。
type Score struct {
	AgentID int64              `json:"agent_id"`
	Name    string             `json:"name"`
	Total   float64            `json:"total"`
	Parts   map[string]float64 `json:"parts"`
	Note    string             `json:"note"`
}

// Decision 是一次总管决策的结果快照。
type Decision struct {
	Category, Tier, Reason, CostBand string
	EstimatedCost                    float64
	Chosen                           *Candidate
	PickReason                       string
	Scores                           []Score
	Err                              error
}

// SuccessRateProvider 留给 018-run-log / 030-agent-memory；本轮权重恒为 0。
type SuccessRateProvider interface {
	Rate(agentID int64) (float64, bool)
}

// Service 无状态，可每次派发新建。
type Service struct {
	rate SuccessRateProvider
}

func New() *Service { return &Service{} }

func (s *Service) WithSuccessRate(p SuccessRateProvider) *Service {
	s.rate = p
	return s
}

// EstimateCost 把档位与模型给出的成本档位折成一个 credit 数。
// band 为空、非法或模型没连上时一律走启发式，不新增网络往返。
func EstimateCost(tier, band string) float64 {
	base := 2.0
	switch tier {
	case domain.TierSmall:
		base = 1
	case domain.TierLarge:
		base = 5
	}
	switch band {
	case routing.CostBandLow:
		base *= 0.7
	case routing.CostBandHigh:
		base *= 1.5
	}
	if base < 0.5 {
		base = 0.5
	}
	return math.Round(base*100) / 100
}

// HardGate 是零 LLM 的准入判定。能力标签为空表示未声明，向后兼容。
func HardGate(c Candidate, category string, estimate float64) error {
	if !c.Enabled || c.Quota == domain.QuotaExhausted {
		return errIneligible
	}
	if len(c.Capabilities) > 0 && category != "" && !contains(c.Capabilities, category) {
		return errCapability
	}
	if c.Balance != nil && *c.Balance < estimate {
		return domain.ErrInsufficientBudget
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Plan 生成决策：先打分用于建议卡排序，再循环调用 011 的 Pick 选人，
// 每次 Pick 结果若被硬门禁淘汰就排除后重选，直到找到可用者或池空。
// 这样 large→floor 降级、人类指定优先、CostRank 排序全部照旧。
func (s *Service) Plan(in PlanInput) Decision {
	d := Decision{
		Category: in.Category, Tier: in.Tier,
		Reason: in.Reason, CostBand: in.CostBand,
		Scores: make([]Score, 0, len(in.Candidates)),
	}
	d.EstimatedCost = in.EstimatedCost
	if d.EstimatedCost <= 0 {
		d.EstimatedCost = EstimateCost(in.Tier, in.CostBand)
	}
	if d.CostBand == "" {
		d.Reason += "；成本按启发式估算"
	}

	byID := make(map[int64]Candidate, len(in.Candidates))
	pool := make([]routing.Candidate, 0, len(in.Candidates))
	for _, c := range in.Candidates {
		byID[c.ID] = c
		pool = append(pool, c.routing())
		if err := HardGate(c, in.Category, d.EstimatedCost); err != nil {
			continue
		}
		sc := score(c, in.Tier, d.EstimatedCost, s.rate)
		if c.Balance == nil {
			sc.Note = "余额未录入，预算门禁跳过"
		}
		d.Scores = append(d.Scores, sc)
	}
	sort.Slice(d.Scores, func(i, j int) bool {
		if d.Scores[i].Total != d.Scores[j].Total {
			return d.Scores[i].Total > d.Scores[j].Total
		}
		return d.Scores[i].AgentID < d.Scores[j].AgentID
	})

	exclude := map[int64]bool{}
	for len(pool) > 0 {
		chosen, reason, err := routing.Pick(in.Category, in.Tier, in.Prefer, exclude, pool)
		if err != nil {
			d.Err = err
			return d
		}
		c := byID[chosen.ID]
		if gateErr := HardGate(c, in.Category, d.EstimatedCost); gateErr == nil {
			cc := c
			d.Chosen = &cc
			d.PickReason = reason
			return d
		}
		exclude[chosen.ID] = true
		next := pool[:0]
		for _, p := range pool {
			if p.ID != chosen.ID {
				next = append(next, p)
			}
		}
		pool = next
	}
	d.Err = routing.ErrNoAgent
	return d
}

// score 权重合计 100：档位 40 / 成本序 20 / 余额余量 20 / 在线 10 / 负载 10。
// 历史成功率只留接口，本轮权重为 0（等 018/030）。
func score(c Candidate, tier string, estimate float64, rate SuccessRateProvider) Score {
	parts := map[string]float64{}
	tierScore := 20.0
	switch tier {
	case domain.TierSmall:
		if c.Serves == domain.ServesSmall {
			tierScore = 40
		}
	case domain.TierFloor:
		if c.Serves == domain.ServesFloor {
			tierScore = 40
		}
	case domain.TierLarge:
		if c.Serves == domain.ServesLarge {
			tierScore = 40
		}
	}
	parts["tier"] = tierScore

	cost := 20.0 * (1 - math.Min(float64(c.CostRank), 100)/100)
	parts["cost_rank"] = math.Round(cost*100) / 100

	balance := 10.0 // 未知：中位分，不因未录入而惩罚
	if c.Balance != nil {
		if *c.Balance <= 0 {
			balance = 0
		} else if estimate <= 0 || *c.Balance/estimate >= 2 {
			balance = 20
		} else {
			balance = 15
		}
	}
	parts["balance"] = balance

	online := 0.0
	if c.Online {
		online = 10
	}
	parts["online"] = online

	load := 10.0 / (1 + float64(c.Load))
	parts["load"] = math.Round(load*100) / 100

	// 预留：本轮固定 0，不影响排序。
	if rate != nil {
		_, _ = rate.Rate(c.ID)
	}
	parts["success_rate"] = 0

	total := 0.0
	for _, v := range parts {
		total += v
	}
	return Score{
		AgentID: c.ID, Name: c.Name,
		Total: math.Round(total*100) / 100, Parts: parts,
	}
}
