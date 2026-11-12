package store

// GetReviewWebhookURL 读取待评审通知地址。空字符串表示不发。
func (s *Store) GetReviewWebhookURL() (string, error) {
	var url string
	err := s.db.QueryRow(`SELECT url FROM review_webhook WHERE id=1`).Scan(&url)
	return url, err
}

// SetReviewWebhookURL 写回待评审通知地址。调用方负责校验格式。
func (s *Store) SetReviewWebhookURL(url string) error {
	_, err := s.db.Exec(`UPDATE review_webhook SET url=? WHERE id=1`, url)
	return err
}
