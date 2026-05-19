package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/darkquasar/fracta/internal/mcpcatalog"
	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"github.com/spf13/cobra"
)

var (
	removeTargetDeploymentFlag string
	removeKeepConfigFlag       bool
	removeYesFlag              bool
)

var configMcpRemoveCmd = &cobra.Command{
	Use:   "remove <server>",
	Short: "Remove an MCP server from the current deployment scaffold.",
	Long: `Reverse of 'fracta config mcp add'. By default removes both the
generated artifacts (compose service, k8s manifests) and the fracta.yaml
mcp_servers block. --keep-config preserves the fracta.yaml block but still
removes the generated artifacts.`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigMcpRemove,
}

func init() {
	configMcpRemoveCmd.Flags().StringVar(&removeTargetDeploymentFlag, "target-deployment", "",
		"Deployment mode: local | docker-compose | k8s. Default: only-enabled-mode if unique.")
	configMcpRemoveCmd.Flags().BoolVar(&removeKeepConfigFlag, "keep-config", false,
		"Remove generated manifests/compose entries but leave the fracta.yaml block intact.")
	configMcpRemoveCmd.Flags().BoolVar(&removeYesFlag, "yes", false,
		"Skip the confirmation prompt.")

	configMcpCmd.AddCommand(configMcpRemoveCmd)
}

func runConfigMcpRemove(cmd *cobra.Command, args []string) error {
	serverID := args[0]

	state, err := mcpcatalog.LoadProjectState(projectRoot)
	if err != nil {
		return fmt.Errorf("read project state: %w", err)
	}

	mode, err := resolveRemoveMode(removeTargetDeploymentFlag, state)
	if err != nil {
		return err
	}

	plan, err := planRemove(projectRoot, serverID, mode, state, removeKeepConfigFlag)
	if err != nil {
		return err
	}
	if len(plan.actions) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Nothing to remove: %q is not configured for target-deployment %s.\n", serverID, mode)
		return nil
	}

	if !removeYesFlag {
		writeRemovePreflight(cmd.OutOrStdout(), serverID, mode, plan)
		if !promptYes(cmd.InOrStdin()) {
			return errors.New("aborted")
		}
	}

	return plan.apply()
}

// removeMode is the resolved deployment mode for one invocation.
type removeMode = scaffolds.Kind

func resolveRemoveMode(flag string, state *mcpcatalog.ProjectState) (removeMode, error) {
	switch flag {
	case "":
		if state != nil {
			if only, ok := state.OnlyEnabled(); ok {
				return only, nil
			}
		}
		return 0, errors.New("multiple scaffolds enabled (or none); pass --target-deployment {local|docker-compose|k8s}")
	case "local":
		return scaffolds.KindLocal, nil
	case "docker-compose":
		return scaffolds.KindDockerCompose, nil
	case "k8s":
		return scaffolds.KindK8s, nil
	default:
		return 0, fmt.Errorf("unknown --target-deployment %q (supported: local, docker-compose, k8s)", flag)
	}
}

// removeAction is one atomic step. kind determines the path through apply():
//   "fracta-yaml"     removes mcp_servers.servers.<id>.<modeKey>
//   "compose"         removes services.<id>-mcp from docker-compose.yml
//   "delete-file"     deletes a file (e.g. k8s manifest, k8s secret)
type removeAction struct {
	kind        string
	description string
	// fracta-yaml fields
	yamlPath string
	id       string
	mode     scaffolds.Kind
	// compose fields
	composePath string
	serviceName string
	// delete-file fields
	filePath string
}

type removePlan struct {
	actions []removeAction
}

func planRemove(root, id string, mode scaffolds.Kind, state *mcpcatalog.ProjectState, keepConfig bool) (*removePlan, error) {
	plan := &removePlan{}

	fractaYAML := filepath.Join(root, "fracta.yaml")
	composePath := filepath.Join(root, "deployment", "docker-compose.yml")
	manifestPath := filepath.Join(root, "deployment", "k8s", "manifests", id+"-mcp.yaml")
	secretPath := filepath.Join(root, "deployment", "k8s", "manifests", id+"-mcp-secret.yaml")

	configured := false
	if state != nil && state.Configured[id] != nil {
		configured = state.Configured[id][mode]
	}
	if !configured {
		// Nothing to do — return empty plan.
		return plan, nil
	}

	switch mode {
	case scaffolds.KindLocal:
		if !keepConfig {
			plan.actions = append(plan.actions, removeAction{
				kind:        "fracta-yaml",
				description: "~ fracta.yaml (remove mcp_servers.servers." + id + ".local)",
				yamlPath:    fractaYAML, id: id, mode: mode,
			})
		}
	case scaffolds.KindDockerCompose:
		// Compose: remove the service block first, then the fracta.yaml entry.
		if _, err := os.Stat(composePath); err == nil {
			plan.actions = append(plan.actions, removeAction{
				kind:        "compose",
				description: "~ deployment/docker-compose.yml (remove services." + id + "-mcp)",
				composePath: composePath, serviceName: id + "-mcp",
			})
		}
		if !keepConfig {
			plan.actions = append(plan.actions, removeAction{
				kind:        "fracta-yaml",
				description: "~ fracta.yaml (remove mcp_servers.servers." + id + ".remote)",
				yamlPath:    fractaYAML, id: id, mode: mode,
			})
		}
	case scaffolds.KindK8s:
		if _, err := os.Stat(manifestPath); err == nil {
			plan.actions = append(plan.actions, removeAction{
				kind:        "delete-file",
				description: "- " + manifestPath,
				filePath:    manifestPath,
			})
		}
		if _, err := os.Stat(secretPath); err == nil {
			plan.actions = append(plan.actions, removeAction{
				kind:        "delete-file",
				description: "- " + secretPath,
				filePath:    secretPath,
			})
		}
		if !keepConfig {
			plan.actions = append(plan.actions, removeAction{
				kind:        "fracta-yaml",
				description: "~ fracta.yaml (remove mcp_servers.servers." + id + ".remote)",
				yamlPath:    fractaYAML, id: id, mode: mode,
			})
		}
	}

	return plan, nil
}

func writeRemovePreflight(w io.Writer, id string, mode scaffolds.Kind, plan *removePlan) {
	fmt.Fprintf(w, "Removing %q for target-deployment %s:\n", id, mode)
	fmt.Fprintln(w, "  Files to be changed:")
	for _, a := range plan.actions {
		fmt.Fprintf(w, "    %s\n", a.description)
	}
	fmt.Fprintln(w, "Proceed? [y/N]")
}

func promptYes(r io.Reader) bool {
	br := bufio.NewReader(r)
	line, _ := br.ReadString('\n')
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y")
}

func (p *removePlan) apply() error {
	for _, a := range p.actions {
		switch a.kind {
		case "fracta-yaml":
			if err := applyRemoveFractaYAML(a); err != nil {
				return err
			}
		case "compose":
			if err := applyRemoveCompose(a); err != nil {
				return err
			}
		case "delete-file":
			if err := os.Remove(a.filePath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", a.filePath, err)
			}
		default:
			return fmt.Errorf("unknown remove action kind %q", a.kind)
		}
	}
	return nil
}

func applyRemoveFractaYAML(a removeAction) error {
	root, err := mcpcatalog.ReadFractaYAML(a.yamlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", a.yamlPath, err)
	}
	if err := mcpcatalog.RemoveMCPServer(root, a.id, a.mode); err != nil {
		return fmt.Errorf("remove server from %s: %w", a.yamlPath, err)
	}
	return mcpcatalog.WriteFractaYAMLAtomic(a.yamlPath, root)
}

func applyRemoveCompose(a removeAction) error {
	root, err := mcpcatalog.ReadComposeYAML(a.composePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", a.composePath, err)
	}
	if err := mcpcatalog.RemoveComposeService(root, a.serviceName); err != nil {
		return fmt.Errorf("remove service from %s: %w", a.composePath, err)
	}
	return mcpcatalog.WriteComposeYAMLAtomic(a.composePath, root)
} 
