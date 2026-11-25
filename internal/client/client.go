// Package client is the Windows side of Household Hub: the saved choice (host the data on this PC or connect to
// a server), the device tokens, and the background notifier that turns new alerts into Windows notifications.
package client

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AppID is the Windows notification identity (registered under HKCU by RegisterAppID).
const AppID = "AllOrNothing.HouseholdHub"

// ControlPort is where the background process answers the window (127.0.0.1 only).
const ControlPort = 18788

// Device is one sign-in that gets Windows notifications on this PC.
type Device struct {
	Server string `json:"server"`
	User   string `json:"user"`
	Token  string `json:"token"`
	Latest int64  `json:"latest"`
}

// Config is saved in %LOCALAPPDATA%\HouseholdHub\client.json.
type Config struct {
	Mode      string   `json:"mode"`       // "host" or "connect"; empty = not chosen yet
	ServerURL string   `json:"server_url"` // connect mode
	Port      int      `json:"port"`       // host mode
	LAN       bool     `json:"lan"`        // host mode: let phones and other PCs connect
	Devices   []Device `json:"devices"`
	// update check
	UpdateChecked string `json:"update_checked"`
	UpdateToldFor string `json:"update_told_for"` // version already announced
}

// CheckForUpdate looks for a newer Windows build once a day and shows one notification per new version.
func CheckForUpdate(current string, show func(title, body string) error) {
	c := Load()
	if t, err := time.Parse(time.RFC3339, c.UpdateChecked); err == nil && time.Since(t) < 20*time.Hour {
		return
	}
	c.UpdateChecked = time.Now().Format(time.RFC3339)
	Save(c)
	rel, err := LatestRelease()
	if err != nil || !Newer(rel.Version, current) || c.UpdateToldFor == rel.Version {
		return
	}
	if show("Household Hub "+rel.Version+" is available", "Open Household Hub, then Settings > Windows app > Install update.") == nil {
		c = Load()
		c.UpdateToldFor = rel.Version
		Save(c)
	}
}

var mu sync.Mutex

// Dir is the per-user folder for config, data and logs.
func Dir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	return filepath.Join(base, "HouseholdHub")
}

// DataDir is where host mode keeps household.db.
func DataDir() string { return filepath.Join(Dir(), "data") }

// Load reads the config (defaults when missing).
func Load() Config {
	mu.Lock()
	defer mu.Unlock()
	c := Config{Port: 8787}
	if b, err := os.ReadFile(filepath.Join(Dir(), "client.json")); err == nil {
		json.Unmarshal(b, &c)
	}
	if c.Port == 0 {
		c.Port = 8787
	}
	return c
}

// Save writes the config atomically.
func Save(c Config) error {
	mu.Lock()
	defer mu.Unlock()
	os.MkdirAll(Dir(), 0o755)
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := filepath.Join(Dir(), "client.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(Dir(), "client.json"))
}

// Server is the base URL the window and notifier talk to.
func (c Config) Server() string {
	if c.Mode == "host" {
		return fmt.Sprintf("http://127.0.0.1:%d", c.Port)
	}
	return strings.TrimRight(c.ServerURL, "/")
}

// ListenAddr is the address host mode serves on.
func (c Config) ListenAddr() string {
	if c.LAN {
		return fmt.Sprintf("0.0.0.0:%d", c.Port)
	}
	return fmt.Sprintf("127.0.0.1:%d", c.Port)
}

// HasDevice reports whether this PC already has a notifier token for user on the current server.
func (c Config) HasDevice(user string) bool {
	for _, d := range c.Devices {
		if d.Server == c.Server() && strings.EqualFold(d.User, user) {
			return true
		}
	}
	return false
}

// AddDevice stores a device token for the current server (replacing the user's old one).
func AddDevice(user, token string, latest int64) error {
	c := Load()
	var keep []Device
	for _, d := range c.Devices {
		if !(d.Server == c.Server() && strings.EqualFold(d.User, user)) {
			keep = append(keep, d)
		}
	}
	c.Devices = append(keep, Device{Server: c.Server(), User: user, Token: token, Latest: latest})
	return Save(c)
}

// Ping checks that a Household Hub server answers at base.
func Ping(base string) bool {
	cl := &http.Client{Timeout: 4 * time.Second}
	resp, err := cl.Get(strings.TrimRight(base, "/") + "/api/ping")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&m)
	return resp.StatusCode == 200 && m["app"] == "household-hub"
}

type notif struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// PollOnce fetches new alerts for every device of the current server and shows them. It returns how many it showed.
func PollOnce(show func(title, body string) error) int {
	c := Load()
	cl := &http.Client{Timeout: 15 * time.Second}
	shown := 0
	latest := map[string]int64{} // token -> newest alert id seen
	revoked := map[string]bool{} // token -> the server no longer accepts it
	for _, d := range c.Devices {
		if d.Server != c.Server() {
			continue
		}
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/notifications?after=%d&limit=20", d.Server, d.Latest), nil)
		req.Header.Set("Authorization", "Bearer "+d.Token)
		resp, err := cl.Do(req)
		if err != nil {
			continue // server off or unreachable: try again next time
		}
		if resp.StatusCode == 401 { // token removed in Settings: forget it
			resp.Body.Close()
			log.Printf("device token for %s on %s was revoked", d.User, d.Server)
			revoked[d.Token] = true
			continue
		}
		var out struct{ Items []notif }
		json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
		resp.Body.Close()
		for _, n := range out.Items {
			if n.ID > d.Latest {
				d.Latest = n.ID
				latest[d.Token] = n.ID
			}
			if err := show(n.Title, n.Body); err != nil {
				log.Printf("toast: %v", err)
			} else {
				shown++
			}
		}
	}
	if len(latest) > 0 || len(revoked) > 0 {
		// re-read, so a device the window added while we polled is kept
		cur := Load()
		var keep []Device
		for _, d := range cur.Devices {
			if revoked[d.Token] {
				continue
			}
			if n, ok := latest[d.Token]; ok && n > d.Latest {
				d.Latest = n
			}
			keep = append(keep, d)
		}
		cur.Devices = keep
		Save(cur)
	}
	return shown
}
