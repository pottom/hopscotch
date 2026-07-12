package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeReconnecter implements both TunnelReconnecter and VPNReconnecter for
// handler tests, tracking which names were paused/resumed.
type fakeReconnecter struct {
	names       map[string]bool
	pauseCalls  []string
	resumeCalls []string
}

func (f *fakeReconnecter) ForceReconnect(name string) bool { return f.names[name] }

func (f *fakeReconnecter) Pause(name string) bool {
	if !f.names[name] {
		return false
	}
	f.pauseCalls = append(f.pauseCalls, name)
	return true
}

func (f *fakeReconnecter) Resume(name string) bool {
	if !f.names[name] {
		return false
	}
	f.resumeCalls = append(f.resumeCalls, name)
	return true
}

func newPauseTestRequest(path, name string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.SetPathValue("name", name)
	return req
}

func TestHandleTunnelPauseResume(t *testing.T) {
	fr := &fakeReconnecter{names: map[string]bool{"foo": true}}
	s := &Server{reconnecter: fr}

	w := httptest.NewRecorder()
	s.handleTunnelPause(w, newPauseTestRequest("/api/tunnels/foo/pause", "foo"))
	if w.Code != http.StatusNoContent {
		t.Errorf("pause known tunnel: status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if len(fr.pauseCalls) != 1 || fr.pauseCalls[0] != "foo" {
		t.Errorf("Pause calls = %v, want [foo]", fr.pauseCalls)
	}

	w = httptest.NewRecorder()
	s.handleTunnelPause(w, newPauseTestRequest("/api/tunnels/bar/pause", "bar"))
	if w.Code != http.StatusNotFound {
		t.Errorf("pause unknown tunnel: status = %d, want %d", w.Code, http.StatusNotFound)
	}

	w = httptest.NewRecorder()
	s.handleTunnelResume(w, newPauseTestRequest("/api/tunnels/foo/resume", "foo"))
	if w.Code != http.StatusNoContent {
		t.Errorf("resume known tunnel: status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if len(fr.resumeCalls) != 1 || fr.resumeCalls[0] != "foo" {
		t.Errorf("Resume calls = %v, want [foo]", fr.resumeCalls)
	}

	w = httptest.NewRecorder()
	s.handleTunnelResume(w, newPauseTestRequest("/api/tunnels/bar/resume", "bar"))
	if w.Code != http.StatusNotFound {
		t.Errorf("resume unknown tunnel: status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleVPNPauseResume(t *testing.T) {
	fr := &fakeReconnecter{names: map[string]bool{"office": true}}
	s := &Server{vpnReconnecter: fr}

	w := httptest.NewRecorder()
	s.handleVPNPause(w, newPauseTestRequest("/api/vpns/office/pause", "office"))
	if w.Code != http.StatusNoContent {
		t.Errorf("pause known vpn: status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if len(fr.pauseCalls) != 1 || fr.pauseCalls[0] != "office" {
		t.Errorf("Pause calls = %v, want [office]", fr.pauseCalls)
	}

	w = httptest.NewRecorder()
	s.handleVPNResume(w, newPauseTestRequest("/api/vpns/unknown/resume", "unknown"))
	if w.Code != http.StatusNotFound {
		t.Errorf("resume unknown vpn: status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleVPNPauseResumeNoVPNsConfigured(t *testing.T) {
	s := &Server{} // vpnReconnecter left nil — no VPNs configured

	w := httptest.NewRecorder()
	s.handleVPNPause(w, newPauseTestRequest("/api/vpns/x/pause", "x"))
	if w.Code != http.StatusNotFound {
		t.Errorf("pause with no vpns: status = %d, want %d", w.Code, http.StatusNotFound)
	}

	w = httptest.NewRecorder()
	s.handleVPNResume(w, newPauseTestRequest("/api/vpns/x/resume", "x"))
	if w.Code != http.StatusNotFound {
		t.Errorf("resume with no vpns: status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
