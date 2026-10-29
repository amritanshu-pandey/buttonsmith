package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amritanshu-pandey/buttonsmith/internal/config"
)

func newTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s := &Server{cfgPath: filepath.Join(t.TempDir(), "app", "config.toml"), token: "secret", host: "127.0.0.1:1234"}
	return s, s.Handler()
}

func do(h http.Handler, method, path, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "127.0.0.1:1234"
	r.Header.Set("X-Token", "secret")
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestGuard(t *testing.T) {
	_, h := newTestServer(t)
	cases := map[string]func(*http.Request){
		"no token":   func(r *http.Request) { r.Header.Del("X-Token") },
		"bad token":  func(r *http.Request) { r.Header.Set("X-Token", "nope") },
		"wrong host": func(r *http.Request) { r.Host = "evil.example:1234" },
	}
	for name, mod := range cases {
		if w := do(h, "GET", "/api/config", "", mod); w.Code != http.StatusForbidden {
			t.Errorf("%s: got %d, want 403", name, w.Code)
		}
	}
	w := do(h, "GET", "/?token=secret", "", func(r *http.Request) { r.Header.Del("X-Token") })
	if w.Code != 200 || !strings.Contains(w.Body.String(), `const TOKEN = "secret"`) {
		t.Errorf("index with query token: %d", w.Code)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	s, h := newTestServer(t)

	// No file yet: defaults are served.
	w := do(h, "GET", "/api/config", "", nil)
	var got configResponse
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Exists || got.Model == nil || len(got.Model.Buttons) != 3 {
		t.Fatalf("defaults: %d %+v", w.Code, got)
	}

	// Save an edited model.
	m := got.Model
	m.Buttons[0].Click = "key:ctrl+w"
	body, _ := json.Marshal(m)
	if w := do(h, "PUT", "/api/config", string(body), nil); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	cfg, err := config.Load(s.cfgPath)
	if err != nil || len(cfg.Buttons) != 3 {
		t.Fatalf("saved file doesn't load: %v", err)
	}

	// Invalid model is rejected and the file left alone.
	before, _ := os.ReadFile(s.cfgPath)
	m.Buttons[0].Click = "warp-drive"
	body, _ = json.Marshal(m)
	if w := do(h, "PUT", "/api/config", string(body), nil); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "warp-drive") {
		t.Errorf("invalid save: %d %s", w.Code, w.Body)
	}
	after, _ := os.ReadFile(s.cfgPath)
	if string(before) != string(after) {
		t.Error("invalid save modified the file")
	}
	if _, err := os.Stat(s.cfgPath + ".bak"); err == nil {
		t.Error("no backup expected for a UI-written file")
	}
}

func TestBackupOfHandWrittenConfig(t *testing.T) {
	s, h := newTestServer(t)
	os.MkdirAll(filepath.Dir(s.cfgPath), 0o755)
	hand := "# my notes\n[device]\nvendor = \"25a7\"\n"
	os.WriteFile(s.cfgPath, []byte(hand), 0o644)

	m, _ := config.ModelFromText(hand)
	body, _ := json.Marshal(m)
	w := do(h, "PUT", "/api/config", string(body), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), ".bak") {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	bak, _ := os.ReadFile(s.cfgPath + ".bak")
	if string(bak) != hand {
		t.Errorf("backup = %q", bak)
	}
	// Second save keeps the original backup.
	do(h, "PUT", "/api/config", string(body), nil)
	if bak, _ := os.ReadFile(s.cfgPath + ".bak"); string(bak) != hand {
		t.Error("backup overwritten")
	}
}

func TestMeta(t *testing.T) {
	_, h := newTestServer(t)
	w := do(h, "GET", "/api/meta", "", nil)
	var m metaResponse
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || len(m.Presets) == 0 || m.Presets[0].Name != "back" {
		t.Errorf("meta: %v %s", err, w.Body.String()[:min(200, w.Body.Len())])
	}
}
