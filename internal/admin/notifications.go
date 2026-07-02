package admin

import (
	"encoding/json"
	"net/http"

	"github.com/pottom/hopscotch/internal/config"
)

// NotificationsJSON mirrors config.NotificationsConfig for the /status
// response and the PUT /api/notifications body — both TUI and web UI use it
// to render and edit the Settings tab.
type NotificationsJSON struct {
	Enabled      bool `json:"enabled"`
	OnDisconnect bool `json:"on_disconnect"`
	OnReconnect  bool `json:"on_reconnect"`
	OnAutoPause  bool `json:"on_auto_pause"`
	Sound        bool `json:"sound"`
}

func notificationsJSON(cfg config.NotificationsConfig) NotificationsJSON {
	return NotificationsJSON{
		Enabled:      cfg.Enabled,
		OnDisconnect: cfg.OnDisconnect,
		OnReconnect:  cfg.OnReconnect,
		OnAutoPause:  cfg.OnAutoPause,
		Sound:        cfg.Sound,
	}
}

// handlePutNotifications updates the live notification settings: applied to
// the running Notifier immediately (no restart) and persisted to
// config.yaml, following the same pattern as handleRules.
func (s *Server) handlePutNotifications(w http.ResponseWriter, r *http.Request) {
	if s.notifyCtl == nil {
		http.Error(w, "notifications not available", http.StatusNotFound)
		return
	}

	var body NotificationsJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	newCfg := config.NotificationsConfig{
		Enabled:      body.Enabled,
		OnDisconnect: body.OnDisconnect,
		OnReconnect:  body.OnReconnect,
		OnAutoPause:  body.OnAutoPause,
		Sound:        body.Sound,
	}

	s.cfgMu.Lock()
	s.cfg.Notifications = newCfg
	path := s.cfg.Path
	cfgCopy := *s.cfg
	s.cfgMu.Unlock()

	if err := config.WriteConfig(&cfgCopy, path); err != nil {
		http.Error(w, "failed to persist config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.notifyCtl.SetConfig(newCfg)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}
