package cmd

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var (
	watchSince string
)

var watchCmd = &cobra.Command{
	Use:   "watch <name>",
	Short: "Stream live events from an agent via SSE",
	Long: `Connect to the control plane and stream live events for the given agent.
Events are displayed as they arrive. Use --since to replay from a specific event ID.
Press Ctrl-C to stop.`,
	Args: cobra.ExactArgs(1),
	RunE: runWatch,
	Annotations: map[string]string{
		RequiresFractaYAMLAnnotation: "true",
	},
}

func init() {
	watchCmd.Flags().StringVar(&watchSince, "since", "", "Replay events from this event ID")
	rootCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, args []string) error {
	task := args[0]

	cfg, err := loadConfigOrDefault(projectRoot)
	if err != nil {
		return err
	}

	cpURL := cfg.ControlPlaneAPI.URL
	if cpURL == "" {
		listenAddr := cfg.ControlPlaneAPI.Listen
		if listenAddr == "" {
			listenAddr = ":9090"
		}
		cpURL = "http://localhost" + listenAddr

		if err := ensureDaemonRunning(projectRoot, configFlag); err != nil {
			return fmt.Errorf("ensuring control plane daemon: %w", err)
		}
	}

	// Build SSE endpoint URL.
	watchURL := fmt.Sprintf("%s/api/v1/agents/%s/watch", cpURL, task)
	if watchSince != "" {
		watchURL += "?since=" + watchSince
	}

	// Set up graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "Watching events for agent %q (Ctrl-C to stop)...\n", task)

	return streamSSE(ctx, watchURL)
}

// streamSSE connects to an SSE endpoint and prints events to stdout.
// Reconnects automatically on disconnect with exponential backoff.
func streamSSE(ctx context.Context, baseURL string) error {
	backoff := time.Second
	maxBackoff := 30 * time.Second
	lastEventID := ""

	// Strip any existing "since" param from the base URL so we can replace it cleanly.
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("parsing watch URL: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Build request URL, replacing since= with the latest cursor.
		q := parsedURL.Query()
		if lastEventID != "" {
			q.Set("since", lastEventID)
		}
		parsedURL.RawQuery = q.Encode()
		reqURL := parsedURL.String()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return fmt.Errorf("building request: %w", err)
		}
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Cache-Control", "no-cache")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil // graceful shutdown
			}
			fmt.Fprintf(os.Stderr, "Connection failed: %v (retrying in %s)\n", err, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("watch endpoint returned %d", resp.StatusCode)
		}

		// Reset backoff on successful connection.
		backoff = time.Second

		// Read SSE events line by line.
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()

			// Track event ID for reconnection (skip blank IDs).
			if strings.HasPrefix(line, "id: ") {
				if id := strings.TrimPrefix(line, "id: "); id != "" {
					lastEventID = id
				}
			}

			// Print event and data lines to stdout.
			if strings.HasPrefix(line, "event: ") || strings.HasPrefix(line, "data: ") || strings.HasPrefix(line, "id: ") {
				fmt.Println(line)
			}
			// Blank line = end of event, print separator.
			if line == "" {
				fmt.Println()
			}
		}
		resp.Body.Close()

		if ctx.Err() != nil {
			return nil
		}

		fmt.Fprintf(os.Stderr, "Connection lost, reconnecting in %s...\n", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}
