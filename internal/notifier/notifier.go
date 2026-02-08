package notifier

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/hseinmoussa/claude-autopilot/internal/config"
)

// Notifier dispatches completion notifications through configured channels.
type Notifier struct {
	webhookURL     string
	desktopEnabled bool
	bellEnabled    bool
}

// NewNotifier creates a Notifier from the given configuration.
func NewNotifier(cfg *config.Config) *Notifier {
	return &Notifier{
		webhookURL:     cfg.WebhookURL,
		desktopEnabled: cfg.NotificationDesktop,
		bellEnabled:    cfg.NotificationBell,
	}
}

// NotifyComplete sends a completion notification through all enabled channels.
// Individual channel failures are logged as warnings but never cause a fatal
// error.
func (n *Notifier) NotifyComplete(summary string) {
	if n.bellEnabled {
		n.sendBell()
	}

	if n.desktopEnabled {
		if err := n.sendDesktop("claude-autopilot", summary); err != nil {
			log.Printf("WARN: desktop notification failed: %v", err)
		}
	}

	if n.webhookURL != "" {
		if err := n.sendWebhook(n.webhookURL, summary); err != nil {
			log.Printf("WARN: webhook notification failed: %v", err)
		}
	}
}

// sendBell prints the ASCII bell character to stdout.
func (n *Notifier) sendBell() {
	fmt.Fprint(os.Stdout, "\a")
}

// sendWebhook POSTs a JSON payload to the given URL. On failure, it retries
// once after 5 seconds. Returns an error only if both attempts fail.
func (n *Notifier) sendWebhook(url, message string) error {
	payload, err := json.Marshal(map[string]string{
		"text": message,
	})
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	err = doWebhookPost(url, payload)
	if err == nil {
		return nil
	}
	log.Printf("WARN: webhook first attempt failed: %v; retrying in 5s", err)

	time.Sleep(5 * time.Second)
	if retryErr := doWebhookPost(url, payload); retryErr != nil {
		return fmt.Errorf("webhook failed after retry: %w (first: %v)", retryErr, err)
	}
	return nil
}

// doWebhookPost performs a single HTTP POST with a JSON body.
func doWebhookPost(url string, payload []byte) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("post webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// sendDesktop sends a native desktop notification. Uses osascript on macOS
// and notify-send on Linux.
func (n *Notifier) sendDesktop(title, message string) error {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(
			`display notification %q with title %q`,
			message, title,
		)
		return exec.Command("osascript", "-e", script).Run()

	case "linux":
		return exec.Command("notify-send", title, message).Run()

	default:
		return fmt.Errorf("desktop notifications not supported on %s", runtime.GOOS)
	}
}
