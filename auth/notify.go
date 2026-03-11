package auth

import (
	"log/slog"
	"os/exec"
	"runtime"
)

// sendNotification sends a desktop notification so the user knows
// a browser window needs attention. Best-effort: failures are logged
// but never block the caller.
func sendNotification(title, message string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		script := `display notification "` + escapeAppleScript(message) + `" with title "` + escapeAppleScript(title) + `"`
		cmd = exec.Command("osascript", "-e", script)
	case "linux":
		cmd = exec.Command("notify-send", "--urgency=critical", title, message)
	case "windows":
		ps := `[void][System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms');` +
			`$n=New-Object System.Windows.Forms.NotifyIcon;` +
			`$n.Icon=[System.Drawing.SystemIcons]::Information;` +
			`$n.Visible=$true;` +
			`$n.ShowBalloonTip(10000,'` + title + `','` + message + `',[System.Windows.Forms.ToolTipIcon]::Info)`
		cmd = exec.Command("powershell", "-NoProfile", "-Command", ps)
	default:
		slog.Debug("Desktop notifications not supported on this platform", "os", runtime.GOOS)
		return
	}

	if err := cmd.Start(); err != nil {
		slog.Debug("Failed to send desktop notification", "error", err)
		return
	}

	// Don't block on the notification process.
	go func() {
		_ = cmd.Wait()
	}()
}

func escapeAppleScript(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			result = append(result, '\\', '"')
		case '\\':
			result = append(result, '\\', '\\')
		default:
			result = append(result, s[i])
		}
	}
	return string(result)
}
