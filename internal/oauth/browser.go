package oauth

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser opens the URL in the user's default browser.
// Returns an error if the browser cannot be opened; the caller should
// fall back to printing the URL.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
	return cmd.Start()
}
