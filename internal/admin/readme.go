package admin

import "net/http"

func (s *Server) handleReadme(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write(s.readme)
}
