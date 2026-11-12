package main

import (
	"errors"
	"strconv"
	"strings"
)

// Action 是一条已经拆开的人类指令。
type Action struct {
	Kind   string
	TaskID int64
	Text   string
}

// ParseCommand 识别通过、驳回、备注、新建。前导斜杠会去掉。
func ParseCommand(text string) (Action, error) {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "/")
	verb, rest, _ := strings.Cut(text, " ")
	verb = strings.TrimSpace(verb)
	rest = strings.TrimSpace(rest)
	switch verb {
	case "通过":
		id, err := parseID(rest)
		if err != nil {
			return Action{}, errors.New("用法：通过 <任务ID>")
		}
		return Action{Kind: "approve", TaskID: id}, nil
	case "驳回":
		id, comment, err := splitIDText(rest)
		if err != nil || comment == "" {
			return Action{}, errors.New("用法：驳回 <任务ID> <原因>")
		}
		return Action{Kind: "reject", TaskID: id, Text: comment}, nil
	case "备注":
		id, note, err := splitIDText(rest)
		if err != nil || note == "" {
			return Action{}, errors.New("用法：备注 <任务ID> <文本>")
		}
		return Action{Kind: "note", TaskID: id, Text: note}, nil
	case "新建":
		if rest == "" {
			return Action{}, errors.New("用法：新建 <标题>")
		}
		return Action{Kind: "create", Text: rest}, nil
	default:
		return Action{}, errors.New("用法：通过、驳回、备注、新建")
	}
}

func parseID(raw string) (int64, error) {
	if strings.Contains(raw, " ") {
		return 0, errors.New("任务 ID 无效")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("任务 ID 无效")
	}
	return id, nil
}

func splitIDText(rest string) (int64, string, error) {
	idText, text, ok := strings.Cut(rest, " ")
	if !ok {
		return 0, "", errors.New("缺少文本")
	}
	id, err := parseID(idText)
	if err != nil {
		return 0, "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, "", errors.New("缺少文本")
	}
	return id, text, nil
}
