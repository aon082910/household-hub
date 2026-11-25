//go:build windows

package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// RegisterAppID makes Windows show our notifications as "Household Hub" with our icon (per user, no admin).
func RegisterAppID(iconPath string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+AppID, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	k.SetStringValue("DisplayName", "Household Hub")
	if iconPath != "" {
		k.SetStringValue("IconUri", iconPath)
	}
	return nil
}

const toastPS = `
$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$t = [Security.SecurityElement]::Escape($env:HHUB_T)
$b = [Security.SecurityElement]::Escape($env:HHUB_B)
$xml = "<toast><visual><binding template='ToastGeneric'><text>$t</text><text>$b</text></binding></visual><audio src='ms-winsoundevent:Notification.Reminder'/></toast>"
$d = New-Object Windows.Data.Xml.Dom.XmlDocument
$d.LoadXml($xml)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($env:HHUB_APP).Show([Windows.UI.Notifications.ToastNotification]::new($d))
`

// ShowToast shows a Windows notification. Title and body travel in environment variables, never in the script text.
func ShowToast(title, body string) error {
	ps := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", toastPS)
	cmd.Env = append(os.Environ(), "HHUB_T="+title, "HHUB_B="+body, "HHUB_APP="+AppID)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &toastErr{err: err, out: string(out)}
	}
	return nil
}

type toastErr struct {
	err error
	out string
}

func (e *toastErr) Error() string { return e.err.Error() + ": " + e.out }
