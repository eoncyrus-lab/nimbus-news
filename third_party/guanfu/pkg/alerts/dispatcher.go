// OS-level dispatchers for alerts (L5). MVP supports:
//
//	osascript (macOS notification)
//	stdout    (plain print — portable, testable)
//
// Webhook / email / telegram are explicitly deferred to v4 — they need
// secret management we don't want to design in MVP.

package alerts

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Dispatch sends the alert through the named channel. Returns the
// channel name on success, empty + error on failure.
func Dispatch(a Alert, channel string) (string, error) {
	switch channel {
	case "", "stdout":
		return "stdout", dispatchStdout(a)
	case "osascript":
		if runtime.GOOS != "darwin" {
			return "", fmt.Errorf("osascript only supported on darwin")
		}
		return "osascript", dispatchOsascript(a)
	}
	return "", fmt.Errorf("unknown dispatch channel %q", channel)
}

func dispatchStdout(a Alert) error {
	fmt.Printf("🔔 ALERT  %s  %s %s %g  (observed %g)\n  asset=%s  expr=%q\n",
		a.Triggered.Format("2006-01-02 15:04"),
		a.Metric, a.Operator, a.Threshold, a.ObservedValue,
		a.Asset, a.Expression)
	return nil
}

func dispatchOsascript(a Alert) error {
	title := fmt.Sprintf("guanfu %s", a.Asset)
	body := fmt.Sprintf("%s %s %g (observed %g)",
		a.Metric, a.Operator, a.Threshold, a.ObservedValue)
	script := fmt.Sprintf(`display notification %q with title %q`, body, title)
	cmd := exec.Command("osascript", "-e", script)
	return cmd.Run()
}
