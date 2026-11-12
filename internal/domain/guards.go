package domain

// 流转守卫（transition guards）
//
// 为什么需要这一层：CanTransition 只回答"这条边存不存在"，
// 但有些边"存在"还要求任务本身满足额外条件。
//
// 真实踩到的坑：idea → todo 在 CanTransition 里是合法边（人类可以派发），
// 但派发的前提是任务已经指派了某个 Agent —— 因为 Agent 端 Claim 要求
// AssigneeAgentID 匹配自己，指派为空的任务一旦进了 todo 就永远没人能领，
// 变成一条死任务。
//
// 而这条"必须先指派"的规则原先只写在 store.statusTransition 里，
// 于是 meta.human_transitions 告诉前端"idea 可以拖到 todo"，
// 用户拖过去，服务端返回 409，界面弹回并报错 —— 拖拽功能实质上是坏的，
// 而且前端无论如何都修不了，因为它拿不到这条规则。
//
// 处理原则：**守卫属于状态机，不属于存储层**。
// 前置条件在这里定义一次，由 BuildMeta 暴露给前端做预检，
// store 也复用同一份判定，于是"契约说的"和"服务端做的"不可能漂移。
// 由 TestTransitionGuardsMatchStore 强制这一点。

// Guard 表示一次流转的前置条件类型。
type Guard string

const (
	// GuardRequiresAssignee 要求任务已指派给一个**已启用**的 Agent。
	//
	// 语义是"派发"：把任务交给某个具体 Agent 去执行。
	// 缺少指派、或指派给了已禁用的 Agent（Claim 会拒绝），任务进入 todo 后都无人能领，属于死任务。
	GuardRequiresAssignee Guard = "requires_assignee"

	// GuardRequiresComment 要求必须填写原因。
	//
	// 用于驳回（review → todo）：没有理由的驳回对 Agent 毫无价值，
	// 它只知道被打回了，不知道该补什么，下次提交大概率原样再被打回。
	GuardRequiresComment Guard = "requires_comment"
)

// TransitionGuards 返回 from → to 这次流转的全部前置条件。
//
// 只对 human 视角的流转返回守卫：Agent 侧的领取/提交由各自的
// 身份校验（Claim 查AssigneeAgentID、Submit 查租约持有者）保证，
// 不需要再声明一遍。
//
// 无守卫时返回空切片而非 nil，保证前端 `?? []` 与 JSON 序列化的稳定性。
func TransitionGuards(from, to Status, human bool) []Guard {
	if !human || !CanTransition(from, to, human) {
		return []Guard{}
	}
	var guards []Guard
	switch {
	case from == StatusIdea && to == StatusTodo:
		// 派发：必须先指派，否则无人能领。
		guards = append(guards, GuardRequiresAssignee)
	case from == StatusReview && to == StatusTodo:
		// 驳回：必须说明要补什么。
		guards = append(guards, GuardRequiresComment)
	}
	if guards == nil {
		return []Guard{}
	}
	return guards
}

// CheckTransitionGuards 校验一次流转的前置条件是否满足。
//
// satisfied 由调用方提供，因为不同前置条件要查的字段不同：
// hasClaimableAssignee 看 Task.AssigneeAgentID 及该 Agent 是否启用，驳回原因看传入的 comment。
// 这样 domain 不必知道 Task 与 Agent 的结构。
//
// 返回第一个不满足的守卫；全部满足返回 nil。
func CheckTransitionGuards(guards []Guard, hasClaimableAssignee bool, comment string) Guard {
	for _, g := range guards {
		switch g {
		case GuardRequiresAssignee:
			if !hasClaimableAssignee {
				return g
			}
		case GuardRequiresComment:
			if !hasText(comment) {
				return g
			}
		}
	}
	return ""
}

// GuardErrorMessage 把守卫翻译成给人类看的一句话。
//
// 前端拖拽落位前的预检提示与服务端拒绝时的提示都取这里，
// 保证两端文案一致 —— 用户看到的永远是同一句解释。
func GuardErrorMessage(g Guard) string {
	switch g {
	case GuardRequiresAssignee:
		return "派发前必须先指派一个已启用的 Agent，否则任务进了「待执行」也没人能领"
	case GuardRequiresComment:
		return "驳回必须填写原因，Agent 需要知道该补什么"
	}
	return ""
}

// hasText 判断字符串去除空白后是否还有内容。
func hasText(s string) bool {
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r':
		default:
			return true
		}
	}
	return false
}
