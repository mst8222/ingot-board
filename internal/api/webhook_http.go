package api

import "net/http"

func (s *Server) getReviewWebhook(w http.ResponseWriter, r *http.Request) {
	url, err := s.svc.ReviewWebhook()
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]string{"url": url})
}

func (s *Server) updateReviewWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	url, err := s.svc.SetReviewWebhook(req.URL)
	if err != nil {
		fail(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]string{"url": url})
}
