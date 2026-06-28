package admin

import (
	"encoding/json"
	"net/http"

	"hopscotch/internal/config"
)

// RuleUpdater applies a new rule set to the running proxy router.
type RuleUpdater interface {
	UpdateRules(rules []config.Rule)
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rules []config.Rule `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	for _, rule := range body.Rules {
		if rule.Pattern == "" {
			http.Error(w, "pattern is required for every rule", http.StatusBadRequest)
			return
		}
		if rule.Tunnel == "" && rule.Via == "" {
			http.Error(w, "tunnel or via is required for every rule", http.StatusBadRequest)
			return
		}
		if err := config.ValidatePattern(rule.Pattern); err != nil {
			http.Error(w, "invalid pattern "+rule.Pattern+": "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	s.cfgMu.Lock()
	s.cfg.Proxy.Rules = body.Rules
	path := s.cfg.Path
	cfgCopy := *s.cfg
	s.cfgMu.Unlock()

	if err := config.WriteConfig(&cfgCopy, path); err != nil {
		http.Error(w, "failed to persist config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.ruleUpdater.UpdateRules(body.Rules)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleValidatePattern(w http.ResponseWriter, r *http.Request) {
	pattern := r.URL.Query().Get("p")
	w.Header().Set("Content-Type", "application/json")
	if err := config.ValidatePattern(pattern); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"valid": false, "error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"valid": true})
}
