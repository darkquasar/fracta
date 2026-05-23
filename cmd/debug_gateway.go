package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/darkquasar/fracta/internal/gateway"
	"github.com/spf13/cobra"
)

var (
	debugGatewayURL     string
	debugGatewayCPURL   string
	debugGatewayDirect  bool
	debugGatewayVerbose bool
	debugGatewayJSON    bool
)

var debugGatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Inspect a running fracta-gateway pod's live state.",
}

var debugGatewayPolicyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Fetch the gateway's tool-policy and visibility snapshot.",
	Long: `Fetches the gateway's policy state and prints it. By default the
request is routed through the control-plane API (which proxies to the gateway
pod), so thin-client operators don't need cluster-internal DNS or
kubectl port-forward to the gateway Service.

URL resolution order:
  1. --cp-url / control_plane_api.url in fracta.yaml — preferred path; the
     CP daemon proxies the request to its configured gateway URL.
  2. --direct (skip the CP API and hit the gateway directly).
  3. --gateway-url / $FRACTA_GATEWAY_URL / gateway.url — direct fallback.

Use --verbose for a per-tool breakdown with the reason each non-visible tool
was filtered. Use --json for the raw response body.`,
	RunE: runDebugGatewayPolicy,
}

func init() {
	debugCmd.AddCommand(debugGatewayCmd)
	debugGatewayCmd.AddCommand(debugGatewayPolicyCmd)

	debugGatewayPolicyCmd.Flags().StringVar(&debugGatewayCPURL, "cp-url", "",
		"control-plane API URL (default: control_plane_api.url in fracta.yaml)")
	debugGatewayPolicyCmd.Flags().BoolVar(&debugGatewayDirect, "direct", false,
		"skip the CP API and call the gateway directly (requires cluster-internal DNS or port-forward)")
	debugGatewayPolicyCmd.Flags().StringVar(&debugGatewayURL, "gateway-url", "",
		"gateway base URL for --direct mode (default: gateway.url in fracta.yaml or $FRACTA_GATEWAY_URL)")
	debugGatewayPolicyCmd.Flags().BoolVar(&debugGatewayVerbose, "verbose", false,
		"include per-tool visibility breakdown")
	debugGatewayPolicyCmd.Flags().BoolVar(&debugGatewayJSON, "json", false,
		"emit raw JSON instead of pretty-printed text")
}

func runDebugGatewayPolicy(cmd *cobra.Command, args []string) error {
	endpoint, displayBase, err := resolveDebugPolicyEndpoint()
	if err != nil {
		return err
	}

	if debugGatewayVerbose {
		if strings.Contains(endpoint, "?") {
			endpoint += "&verbose=1"
		} else {
			endpoint += "?verbose=1"
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if debugGatewayJSON {
		fmt.Fprintln(cmd.OutOrStdout(), string(body))
		return nil
	}

	var state gateway.PolicyState
	if err := json.Unmarshal(body, &state); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	printGatewayPolicyState(cmd.OutOrStdout(), displayBase, state)
	return nil
}

// resolveDebugPolicyEndpoint returns (full URL to GET, label to show in
// output). Prefers the CP API proxy unless --direct is set.
func resolveDebugPolicyEndpoint() (endpoint, display string, err error) {
	if !debugGatewayDirect {
		cp, cperr := resolveCPAPIBaseURL()
		if cperr == nil {
			return strings.TrimRight(cp, "/") + "/api/v1/debug/gateway-policy",
				cp + " (via control-plane API)",
				nil
		}
		// If --direct was not requested and CP API resolution fails, fall
		// through to gateway-direct so users without a CP daemon still work.
	}

	gw, err := resolveGatewayBaseURL()
	if err != nil {
		return "", "", err
	}
	return strings.TrimRight(gw, "/") + "/debug/policy", gw, nil
}

func resolveCPAPIBaseURL() (string, error) {
	if debugGatewayCPURL != "" {
		return validateGatewayURL(debugGatewayCPURL)
	}
	root, _ := FindProjectRoot("")
	cfg, err := loadConfigOrDefault(root)
	if err == nil && cfg != nil && cfg.ControlPlaneAPI.URL != "" {
		return validateGatewayURL(cfg.ControlPlaneAPI.URL)
	}
	return "", fmt.Errorf("no control-plane API URL configured")
}

func resolveGatewayBaseURL() (string, error) {
	if debugGatewayURL != "" {
		return validateGatewayURL(debugGatewayURL)
	}
	if env := os.Getenv("FRACTA_GATEWAY_URL"); env != "" {
		return validateGatewayURL(env)
	}

	root, _ := FindProjectRoot("")
	cfg, err := loadConfigOrDefault(root)
	if err == nil && cfg != nil && cfg.Gateway.URL != "" {
		return validateGatewayURL(cfg.Gateway.URL)
	}

	return "", fmt.Errorf("no gateway URL: pass --gateway-url, set FRACTA_GATEWAY_URL, or configure gateway.url in fracta.yaml")
}

func validateGatewayURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid gateway URL %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("gateway URL %q must include scheme and host", raw)
	}
	return raw, nil
}

func printGatewayPolicyState(w io.Writer, baseURL string, s gateway.PolicyState) {
	fmt.Fprintf(w, "Gateway: %s\n", baseURL)
	fmt.Fprintf(w, "Has registry store:   %t\n", s.HasRegistryStore)
	fmt.Fprintf(w, "Has policies:         %t\n", s.HasPolicies)
	fmt.Fprintf(w, "Visible set built:    %t (generation %d)\n", s.VisibleSetBuilt, s.Generation)
	fmt.Fprintf(w, "Catalog size:         %d\n", s.CatalogSize)
	fmt.Fprintf(w, "Visible:              %d\n", s.VisibleCount)
	fmt.Fprintf(w, "Denied by policy:     %d\n", s.DeniedByPolicy)
	fmt.Fprintf(w, "Disabled by registry: %d\n", s.DisabledByRegistry)
	fmt.Fprintln(w)

	if len(s.Policies) == 0 {
		fmt.Fprintln(w, "Policies: (none configured)")
	} else {
		fmt.Fprintf(w, "Policies (%d servers):\n", len(s.Policies))
		for _, p := range s.Policies {
			fmt.Fprintf(w, "  %s:\n", p.Server)
			fmt.Fprintf(w, "    deny:       %s\n", fmtList(p.Deny))
			fmt.Fprintf(w, "    allow_only: %s\n", fmtList(p.AllowOnly))
		}
	}

	if len(s.Tools) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Tools:")
	for _, t := range s.Tools {
		mark := "+"
		extra := ""
		if !t.Visible {
			mark = "-"
			extra = " [" + t.Reason + "]"
		}
		fmt.Fprintf(w, "  %s %s%s\n", mark, t.NamespacedName, extra)
	}
}

func fmtList(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return "[" + strings.Join(items, ", ") + "]"
}
