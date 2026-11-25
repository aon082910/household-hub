//go:build windows

// HouseholdHub.exe is the Windows window of Household Hub (WebView2, its own window and taskbar icon). On first
// start it asks whether this PC hosts the household data or connects to a server (the Unraid container). It keeps
// hhub.exe background running, which hosts the data in host mode and shows Windows notifications in both modes.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"

	"hhub/internal/client"
)

const windowTitle = "Household Hub"

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	build    = "0"
	version  = "dev" // set by build.ps1
)

func utf16p(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func messageBox(text string) {
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(utf16p(text))), uintptr(unsafe.Pointer(utf16p(windowTitle))), 0x10)
}

func ctl(method, path string) (map[string]any, error) {
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", client.ControlPort, path), nil)
	req.Header.Set("X-HHub", "1")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var m map[string]any
	err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&m)
	return m, err
}

// ensureBackground starts hhub.exe background (detached, so it keeps running after the window closes).
func ensureBackground() error {
	if m, err := ctl("GET", "/status"); err == nil && m["app"] == "household-hub-background" {
		return nil
	}
	self, _ := os.Executable()
	exe := filepath.Join(filepath.Dir(self), "hhub.exe")
	cmd := exec.Command(exe, "background")
	cmd.Dir = filepath.Dir(self)
	// DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200 | 0x08000000}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start hhub.exe: %w", err)
	}
	cmd.Process.Release()
	for end := time.Now().Add(60 * time.Second); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		if _, err := ctl("GET", "/status"); err == nil {
			return nil
		}
	}
	return errors.New("hhub.exe background did not answer")
}

// ready makes sure the chosen server answers; in host mode it asks the background process to (re)start it.
func ready(c client.Config) error {
	if c.Mode == "host" {
		if _, err := ctl("POST", "/reload"); err != nil {
			return err
		}
		for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
			if client.Ping(c.Server()) {
				return nil
			}
		}
		return fmt.Errorf("the household server on this PC did not start (port %d in use?). See %s", c.Port, filepath.Join(client.Dir(), "background.log"))
	}
	ctl("POST", "/reload")
	if !client.Ping(c.Server()) {
		return fmt.Errorf("no Household Hub answers at %s", c.Server())
	}
	return nil
}

func focusExisting() {
	h, _, _ := user32.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(utf16p(windowTitle))))
	if h != 0 {
		user32.NewProc("ShowWindow").Call(h, 9)
		user32.NewProc("SetForegroundWindow").Call(h)
	}
}

func setWindowIcons(hwnd uintptr) {
	self, _ := os.Executable()
	ico := filepath.Join(filepath.Dir(self), "icon.ico")
	load, send := user32.NewProc("LoadImageW"), user32.NewProc("SendMessageW")
	for i, size := range []uintptr{32, 16} {
		if h, _, _ := load.Call(0, uintptr(unsafe.Pointer(utf16p(ico))), 1, size, size, 0x10); h != 0 {
			send.Call(hwnd, 0x80, uintptr(1-i), h)
		}
	}
}

const chooser = `<!doctype html><html><head><meta charset="utf-8"><title>Household Hub</title><style>
body{font:15px "Segoe UI",sans-serif;background:#f4f6fb;color:#1c2333;margin:0;display:flex;justify-content:center;padding:4vh 1rem}
.card{background:#fff;border:1px solid #e2e6ef;border-radius:12px;padding:1.4rem 1.6rem;max-width:620px;width:100%}
h1{margin:.2rem 0 1rem;font-size:1.5rem}.opt{border:1px solid #e2e6ef;border-radius:10px;padding:.8rem 1rem;margin:.6rem 0;cursor:pointer;display:block}
.opt:hover{border-color:#4f7cff}.opt input[type=radio]{margin-right:.5rem}input[type=text],input[type=number]{font:inherit;padding:.45rem .6rem;border:1px solid #d5dae6;border-radius:8px;width:100%;box-sizing:border-box;margin-top:.4rem}
small{color:#6b7489}button{font:inherit;background:#4f7cff;color:#fff;border:0;border-radius:8px;padding:.6rem 1.2rem;cursor:pointer;margin-top:1rem}
.err{color:#d64545;min-height:1.2em;margin-top:.6rem}code{background:#eef1f7;padding:.1rem .3rem;border-radius:4px;font-size:.82em;user-select:all}
@media (prefers-color-scheme: dark){body{background:#11151f;color:#e5e9f2}.card{background:#1a2030;border-color:#2b3346}.opt{border-color:#2b3346}input{background:#11151f;color:#e5e9f2;border-color:#2b3346}code{background:#11151f}}
</style></head><body><div class="card"><h1>Household Hub</h1><p>Where does your household's data live?</p>
<label class="opt"><input type="radio" name="m" value="connect" id="mc"><b>Connect to the household server</b> (the Unraid app)<br>
<small>Everyone shares one server. This PC shows the app and Windows notifications.</small>
<input type="text" id="url" placeholder="http://192.168.1.144:8095"></label>
<label class="opt"><input type="radio" name="m" value="host" id="mh"><b>Keep the data on this PC</b><br>
<small>This PC is the household server. It runs in the background after sign-in.</small><br>
<label><input type="checkbox" id="lan"> Let phones and other computers on the home network use it</label>
<input type="number" id="port" min="1024" max="65535" value="8787">
<small id="lanNote">Windows may ask once to allow it through the firewall: choose <b>Private networks</b>. Others then open <code id="addr"></code> in a browser. If they cannot connect, run this once in an admin PowerShell:<br><code id="fw"></code></small></label>
<div class="err" id="err"></div><button id="go">Continue</button></div>
<script>
(async () => {
  const c = JSON.parse(await window.hhubCurrent());
  document.getElementById(c.mode === 'host' ? 'mh' : 'mc').checked = true;
  document.getElementById('url').value = c.server_url || '';
  document.getElementById('lan').checked = !!c.lan;
  document.getElementById('port').value = c.port || 8787;
  const upd = () => { const p = document.getElementById('port').value;
    document.getElementById('addr').textContent = 'http://' + (c.ip || 'this-pc') + ':' + p;
    document.getElementById('fw').textContent = 'New-NetFirewallRule -DisplayName "Household Hub" -Direction Inbound -Protocol TCP -LocalPort ' + p + ' -Action Allow -Profile Private'; };
  document.getElementById('port').oninput = upd; upd();
  if (c.error) document.getElementById('err').textContent = c.error;
  document.getElementById('go').onclick = async () => {
    const mode = document.querySelector('input[name=m]:checked').value;
    document.getElementById('err').textContent = 'Connecting...';
    try { await window.hhubChoose(JSON.stringify({ mode, server_url: document.getElementById('url').value.trim(), lan: document.getElementById('lan').checked, port: Number(document.getElementById('port').value) })); }
    catch (e) { document.getElementById('err').textContent = String(e.message || e); }
  };
})();
</script></body></html>`

// lanIP is this PC's address on the home network (a UDP "dial" sends nothing; it only picks the route).
func lanIP() string {
	conn, err := net.Dial("udp", "192.168.1.1:9")
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func main() {
	runtime.LockOSThread()
	for _, a := range os.Args[1:] {
		if a == "--check" {
			os.Exit(0)
		}
	}
	_, _, lastErr := kernel32.NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(utf16p(`Local\HouseholdHubWindow`))))
	if errno, ok := lastErr.(syscall.Errno); ok && errno == 183 {
		focusExisting()
		return
	}
	shell32.NewProc("SetCurrentProcessExplicitAppUserModelID").Call(uintptr(unsafe.Pointer(utf16p(client.AppID))))
	if err := ensureBackground(); err != nil {
		messageBox(err.Error() + "\nReinstall with build.ps1 -Install.")
		os.Exit(1)
	}
	data := filepath.Join(client.Dir(), "webview")
	os.MkdirAll(data, 0o755)
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus:     true,
		DataPath:      data,
		WindowOptions: webview2.WindowOptions{Title: windowTitle, Width: 1360, Height: 900, Center: true},
	})
	c := client.Load()
	if w == nil { // no WebView2 runtime: use the default browser
		if c.Mode != "" && ready(c) == nil {
			exec.Command("rundll32", "url.dll,FileProtocolHandler", c.Server()).Start()
			return
		}
		messageBox("The WebView2 component is missing. Install it from Microsoft (WebView2 Runtime) and start again.")
		os.Exit(1)
	}
	setWindowIcons(uintptr(w.Window()))
	lastErr2 := ""
	showChooser := func(msg string) { lastErr2 = msg; w.SetHtml(chooser) }
	open := func(c client.Config) error {
		if err := ready(c); err != nil {
			return err
		}
		w.Navigate(c.Server() + "/")
		return nil
	}

	w.Bind("hhubCurrent", func() string {
		c := client.Load()
		b, _ := json.Marshal(map[string]any{"mode": c.Mode, "server_url": c.ServerURL, "lan": c.LAN, "port": c.Port, "error": lastErr2, "ip": lanIP()})
		return string(b)
	})
	w.Bind("hhubChoose", func(js string) error {
		var in client.Config
		if err := json.Unmarshal([]byte(js), &in); err != nil {
			return err
		}
		c := client.Load()
		c.Mode = in.Mode
		switch in.Mode {
		case "connect":
			u := strings.TrimRight(strings.TrimSpace(in.ServerURL), "/")
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				u = "http://" + u
			}
			if p, err := url.Parse(u); err != nil || p.Host == "" {
				return errors.New("type the server address, for example http://192.168.1.144:8095")
			}
			if !client.Ping(u) {
				return fmt.Errorf("no Household Hub answers at %s", u)
			}
			c.ServerURL = u
		case "host":
			if in.Port < 1024 || in.Port > 65535 {
				return errors.New("port must be 1024-65535")
			}
			c.Port, c.LAN = in.Port, in.LAN
		default:
			return errors.New("pick one")
		}
		if err := client.Save(c); err != nil {
			return err
		}
		lastErr2 = ""
		if err := ready(c); err != nil {
			return err
		}
		w.Dispatch(func() { w.Navigate(c.Server() + "/") })
		return nil
	})
	w.Bind("hhubSwitchServer", func() { w.Dispatch(func() { showChooser("") }) })
	w.Bind("hhubHasDevice", func(user string) bool { return client.Load().HasDevice(user) })
	w.Bind("hhubHostName", func() string { h, _ := os.Hostname(); return h })
	w.Bind("hhubVersion", func() (string, error) {
		out := map[string]any{"current": version}
		rel, err := client.LatestRelease()
		if err != nil {
			out["error"] = err.Error()
		} else {
			out["latest"], out["notes"], out["newer"] = rel.Version, rel.Notes, client.Newer(rel.Version, version)
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	})
	w.Bind("hhubInstallUpdate", func() error {
		rel, err := client.LatestRelease()
		if err != nil {
			return err
		}
		if !client.Newer(rel.Version, version) {
			return errors.New("already up to date")
		}
		dir, err := client.DownloadUpdate(rel)
		if err != nil {
			return err
		}
		self, _ := os.Executable()
		script, err := client.UpdateScript(dir, filepath.Dir(self))
		if err != nil {
			return err
		}
		cmd := exec.Command("cmd.exe", "/C", script)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200 | 0x08000000}
		if err := cmd.Start(); err != nil {
			return err
		}
		ctl("POST", "/quit")
		go func() { time.Sleep(500 * time.Millisecond); w.Dispatch(func() { w.Terminate() }) }()
		return nil
	})
	w.Bind("hhubSaveDevice", func(js string) error {
		var in struct {
			Token  string `json:"token"`
			Latest int64  `json:"latest"`
			User   string `json:"user"`
		}
		if err := json.Unmarshal([]byte(js), &in); err != nil || in.Token == "" {
			return errors.New("bad device data")
		}
		return client.AddDevice(in.User, in.Token, in.Latest)
	})

	if c.Mode == "" {
		showChooser("")
	} else if err := open(c); err != nil {
		showChooser(err.Error())
	}
	_ = build
	w.Run()
	w.Destroy()
}
