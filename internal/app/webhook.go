package app

import "github.com/mst8222/ingot-board/internal/webhook"

// ReviewWebhook 返回当前待评审通知地址。
func (s *Service) ReviewWebhook() (string, error) {
	return s.store.GetReviewWebhookURL()
}

// SetReviewWebhook 校验并保存待评审通知地址。空字符串表示关闭。
func (s *Service) SetReviewWebhook(raw string) (string, error) {
	normalized, err := webhook.Validate(raw)
	if err != nil {
		return "", err
	}
	if err := s.store.SetReviewWebhookURL(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}
