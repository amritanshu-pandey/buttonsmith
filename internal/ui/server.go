// Package ui serves the settings page: a local web app that edits the
// config file, controls the systemd user service and detects buttons.
package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/amritanshu-pandey/buttonsmith/internal/access"
	"github.com/amritanshu-pandey/buttonsmith/internal/action"
	"github.com/amritanshu-pandey/buttonsmith/internal/app"
	"github.com/amritanshu-pandey/buttonsmith/internal/config"
	"github.com/amritanshu-pandey/buttonsmith/internal/evdev"
)

//go:embed index.html
var indexHTML string

//go:embed icon.svg
var iconSVG []byte

//go:embed window.py
var windowPy string

const unit = app.ID + ".service"

type Server struct {
	cfgPath string
	token   string
	host    string // expected Host header, guards against DNS rebinding

	clients  atomic.Int32 // open /api/events streams (open windows)
	detectMu sync.Mutex   // one detect session at a time
	saveMu   sync.Mutex
}

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// Run serves the UI on addr (normally 127.0.0.1:0) and opens it unless
// noOpen is set. It returns once the last window has been closed for a
// while, or on error.
func Run(cfgPath, addr string, noOpen bool) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s := &Server{cfgPath: cfgPath, token: newToken(), host: ln.Addr().String()}
	url := fmt.Sprintf("http://%s/?token=%s", s.host, s.token)
	fmt.Fprintf(os.Stderr, app.ID+" ui: %s\n", url)

	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go s.exitWhenClosed(srv)
	if !noOpen {
		openWindow(url, func() {
			fmt.Fprintln(os.Stderr, app.ID+" ui: window closed, exiting")
			srv.Shutdown(context.Background())
		})
	}
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// exitWhenClosed shuts the server down once no window has been connected
// for a grace period (long enough to survive a page reload).
func (s *Server) exitWhenClosed(srv *http.Server) {
	const grace = 10 * time.Second
	connected := false
	var idleSince time.Time
	for range time.Tick(time.Second) {
		if s.clients.Load() > 0 {
			connected, idleSince = true, time.Time{}
			continue
		}
		if !connected {
			continue // still waiting for the first window
		}
		if idleSince.IsZero() {
			idleSince = time.Now()
		} else if time.Since(idleSince) > grace {
			fmt.Fprintln(os.Stderr, app.ID+" ui: window closed, exiting")
			srv.Shutdown(context.Background())
			return
		}
	}
}

// openWindow shows the UI in the native Qt window (closed is called when it
// exits), falling back to a Chromium app window or the default browser.
func openWindow(url string, closed func()) {
	if cmd, err := startQtWindow(url); err == nil {
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		select {
		case err := <-exited:
			if err == nil {
				closed() // closed straight away by the user
				return
			}
			fmt.Fprintf(os.Stderr, "%s ui: Qt window unavailable (%v), using a browser\n", app.ID, err)
		case <-time.After(3 * time.Second):
			go func() { <-exited; closed() }()
			return
		}
	}
	for _, b := range []string{"google-chrome", "chromium-browser", "chromium", "brave-browser", "microsoft-edge"} {
		if path, err := exec.LookPath(b); err == nil {
			cmd := exec.Command(path, "--app="+url, "--window-size=1000,760")
			if cmd.Start() == nil {
				go cmd.Wait()
				return
			}
		}
	}
	cmd := exec.Command("xdg-open", url)
	if cmd.Start() == nil {
		go cmd.Wait()
		return
	}
	fmt.Fprintln(os.Stderr, app.ID+" ui: open the URL above in a browser")
}

// startQtWindow runs the embedded PyQt6 WebEngine window. The icon is
// written to the cache directory as a fallback for when the icon theme
// doesn't have it (for example when running from the source tree).
func startQtWindow(url string) (*exec.Cmd, error) {
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, err
	}
	iconPath := ""
	if dir, err := os.UserCacheDir(); err == nil {
		iconPath = filepath.Join(dir, app.ID, "icon.svg")
		if os.MkdirAll(filepath.Dir(iconPath), 0o755) != nil || os.WriteFile(iconPath, iconSVG, 0o644) != nil {
			iconPath = ""
		}
	}
	cmd := exec.Command(python, "-I", "-c", windowPy, app.AppID, app.Name, iconPath, url)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd, cmd.Start()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /icon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write(iconSVG)
	})
	mux.HandleFunc("GET /api/config", s.getConfig)
	mux.HandleFunc("PUT /api/config", s.putConfig)
	mux.HandleFunc("GET /api/meta", s.meta)
	mux.HandleFunc("POST /api/allow-device/{id}", s.allowDevice)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("POST /api/service/{op}", s.service)
	mux.HandleFunc("GET /api/detect", s.detect)
	return s.guard(mux)
}

// guard only allows requests addressed to our own host:port and carrying
// the session token, so other local users and web pages can't use the API.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Token")
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		if r.Host != s.host || subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'")
	page := strings.ReplaceAll(indexHTML, "__TOKEN__", s.token)
	page = strings.Replace(page, "__CONFIG_PATH__", htmlEscape(s.cfgPath), 1)
	page = strings.ReplaceAll(page, "__APP_NAME__", app.Name)
	page = strings.ReplaceAll(page, "__APP_ID__", app.ID)
	fmt.Fprint(w, page)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// --- config ---------------------------------------------------------------

type configResponse struct {
	Path   string        `json:"path"`
	Exists bool          `json:"exists"`
	Model  *config.Model `json:"model"`
	// Error is set when the file on disk is invalid; Model is then the
	// built-in default.
	Error string `json:"error,omitempty"`
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	resp := configResponse{Path: s.cfgPath}
	text, err := os.ReadFile(s.cfgPath)
	switch {
	case err == nil:
		resp.Exists = true
		resp.Model, err = config.ModelFromText(string(text))
		if err != nil {
			resp.Error = err.Error()
		}
	case !errors.Is(err, os.ErrNotExist):
		resp.Error = err.Error()
	}
	if resp.Model == nil {
		resp.Model, _ = config.ModelFromText(config.Example)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var m config.Model
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&m); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	text, err := m.Encode()
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err)
		return
	}

	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	backup, err := s.backupOnce()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := writeAtomic(s.cfgPath, []byte(text)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"backup": backup})
}

// backupOnce keeps a copy of a hand-written config (with its comments)
// before the UI first rewrites it. It returns the backup path if it made one.
func (s *Server) backupOnce() (string, error) {
	old, err := os.ReadFile(s.cfgPath)
	if errors.Is(err, os.ErrNotExist) || strings.HasPrefix(string(old), config.ModelHeader[:40]) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	bak := s.cfgPath + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return "", nil
	}
	return bak, os.WriteFile(bak, old, 0o644)
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// --- metadata -------------------------------------------------------------

type deviceInfo struct {
	Vendor   string   `json:"vendor"`
	Product  string   `json:"product"`
	Names    []string `json:"names"`
	Mouse    bool     `json:"mouse"`
	Readable bool     `json:"readable"`
	Buttons  []string `json:"buttons"` // config names of the mouse buttons it reports
}

type metaResponse struct {
	Presets       []action.Preset `json:"presets"`
	KwinShortcuts []string        `json:"kwin_shortcuts"`
	Devices       []deviceInfo    `json:"devices"`
	ServiceUnit   string          `json:"service_unit"`
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, metaResponse{
		Presets:       action.Presets,
		KwinShortcuts: kwinShortcuts(r.Context()),
		Devices:       devices(),
		ServiceUnit:   unit,
	})
}

func kwinShortcuts(ctx context.Context) []string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "busctl", "--user", "--json=short", "call", "org.kde.kglobalaccel",
		"/component/kwin", "org.kde.kglobalaccel.Component", "shortcutNames").Output()
	if err != nil {
		return []string{}
	}
	var resp struct {
		Data [][]string `json:"data"`
	}
	if json.Unmarshal(out, &resp) != nil || len(resp.Data) == 0 {
		return []string{}
	}
	names := resp.Data[0]
	sort.Strings(names)
	return names
}

// devices groups event nodes by vendor:product.
func devices() []deviceInfo {
	byID := map[string]*deviceInfo{}
	var order []string
	for _, n := range evdev.ListNodes() {
		if strings.HasPrefix(n.Name, evdev.VirtualPrefix) || n.Vendor == 0 {
			continue
		}
		id := fmt.Sprintf("%04x:%04x", n.Vendor, n.Product)
		d, ok := byID[id]
		if !ok {
			d = &deviceInfo{Vendor: fmt.Sprintf("%04x", n.Vendor), Product: fmt.Sprintf("%04x", n.Product), Names: []string{}, Buttons: []string{}}
			byID[id] = d
			order = append(order, id)
		}
		if !contains(d.Names, n.Name) {
			d.Names = append(d.Names, n.Name)
		}
		for _, b := range n.Buttons {
			if name := action.InputName(b); !contains(d.Buttons, name) {
				d.Buttons = append(d.Buttons, name)
			}
		}
		if n.Motion {
			d.Mouse = true
			d.Readable = unix.Access(n.Path, unix.R_OK) == nil
		}
	}
	out := []deviceInfo{}
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// --- status and service control ------------------------------------------

type serviceStatus struct {
	Installed bool   `json:"installed"`
	Active    string `json:"active"`  // active, inactive, failed, activating…
	Enabled   string `json:"enabled"` // enabled, disabled…
}

type deviceStatus struct {
	Configured bool   `json:"configured"`
	Present    bool   `json:"present"`
	Readable   bool   `json:"readable"`
	Name       string `json:"name"`
}

type status struct {
	Service serviceStatus `json:"service"`
	Device  deviceStatus  `json:"device"`
}

func getServiceStatus() serviceStatus {
	out, err := exec.Command("systemctl", "--user", "show", unit,
		"--property=LoadState,ActiveState,UnitFileState").Output()
	st := serviceStatus{Active: "unknown"}
	if err != nil {
		return st
	}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "LoadState":
			st.Installed = v == "loaded"
		case "ActiveState":
			st.Active = v
		case "UnitFileState":
			st.Enabled = v
		}
	}
	return st
}

func (s *Server) deviceStatus() deviceStatus {
	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		cfg, _ = config.Parse(config.Example)
	}
	st := deviceStatus{Configured: !cfg.Device.Empty()}
	if !st.Configured {
		return st
	}
	for _, n := range evdev.ListNodes() {
		if n.Motion && !strings.HasPrefix(n.Name, evdev.VirtualPrefix) && cfg.Device.Matches(n.Vendor, n.Product, n.Name) {
			st.Present, st.Name = true, n.Name
			st.Readable = unix.Access(n.Path, unix.R_OK) == nil
			break
		}
	}
	return st
}

// events streams status every two seconds. Open streams also tell us a
// window is still open.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	s.clients.Add(1)
	defer s.clients.Add(-1)
	sse, ok := newSSE(w)
	if !ok {
		return
	}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		if !sse.send("status", status{Service: getServiceStatus(), Device: s.deviceStatus()}) {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) service(w http.ResponseWriter, r *http.Request) {
	var args []string
	switch op := r.PathValue("op"); op {
	case "start", "stop", "restart":
		args = []string{op, unit}
	case "enable", "disable":
		args = []string{op, "--now", unit}
	default:
		writeErr(w, http.StatusNotFound, fmt.Errorf("unknown operation %q", op))
		return
	}
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out))))
		return
	}
	writeJSON(w, http.StatusOK, getServiceStatus())
}

// allowDevice grants read access to a mouse. pkexec shows the desktop's
// password dialog, so this blocks until the user answers it.
func (s *Server) allowDevice(w http.ResponseWriter, r *http.Request) {
	vendor, product, err := access.ParseID(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := access.Request(vendor, product); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, devices())
}

// --- detection ------------------------------------------------------------

type press struct {
	Code   uint16 `json:"code"`
	Name   string `json:"name"`   // config table name, e.g. "back" or "KEY_F13"
	Evdev  string `json:"evdev"`  // raw evdev name
	Device string `json:"device"` // node name
}

// detect streams button presses from the device described by the query
// (vendor, product, name). A running service grabs the device and would hide
// the events, so it is paused for the session and resumed afterwards.
func (s *Server) detect(w http.ResponseWriter, r *http.Request) {
	if !s.detectMu.TryLock() {
		writeErr(w, http.StatusConflict, errors.New("detection is already running in another window"))
		return
	}
	defer s.detectMu.Unlock()

	q := r.URL.Query()
	match := config.DeviceMatch{Vendor: q.Get("vendor"), Product: q.Get("product"), Name: q.Get("name")}
	sse, ok := newSSE(w)
	if !ok {
		return
	}

	if getServiceStatus().Active == "active" {
		if err := exec.Command("systemctl", "--user", "stop", unit).Run(); err != nil {
			sse.send("fail", map[string]string{"error": "could not pause the background service: " + err.Error()})
			return
		}
		sse.send("paused", nil)
		defer exec.Command("systemctl", "--user", "start", unit).Run()
	}

	devs, denied := evdev.OpenNodes(func(n evdev.NodeInfo) bool {
		return !match.Empty() && match.Matches(n.Vendor, n.Product, n.Name)
	})
	defer func() {
		for _, d := range devs {
			d.Close()
		}
	}()
	if len(devs) == 0 {
		msg := "the selected device is not connected"
		if len(denied) > 0 {
			msg = "no permission to read the device; allow access on the Device page"
		}
		sse.send("fail", map[string]string{"error": msg})
		return
	}
	sse.send("ready", map[string]int{"nodes": len(devs)})

	pfds := make([]unix.PollFd, len(devs))
	for i, d := range devs {
		pfds[i] = unix.PollFd{Fd: int32(d.Fd()), Events: unix.POLLIN}
	}
	for r.Context().Err() == nil {
		if _, err := unix.Poll(pfds, 200); err != nil && !errors.Is(err, unix.EINTR) {
			sse.send("fail", map[string]string{"error": err.Error()})
			return
		}
		for i, p := range pfds {
			if p.Revents == 0 {
				continue
			}
			evs, err := devs[i].Read()
			for _, ev := range evs {
				if ev.Type == evdev.EV_KEY && ev.Value == 1 {
					sse.send("press", press{Code: ev.Code, Name: action.InputName(ev.Code), Evdev: evdev.KeyName(ev.Code), Device: devs[i].Name})
				}
			}
			if err != nil {
				sse.send("fail", map[string]string{"error": "device disconnected"})
				return
			}
		}
	}
}

// --- server-sent events ---------------------------------------------------

type sseWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func newSSE(w http.ResponseWriter) (*sseWriter, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &sseWriter{w, f}, true
}

func (s *sseWriter) send(event string, data any) bool {
	b, _ := json.Marshal(data)
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return false
	}
	s.f.Flush()
	return true
}
