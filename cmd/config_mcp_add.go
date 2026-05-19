package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/darkquasar/fracta/internal/mcpcatalog"
	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"github.com/spf13/cobra"
)

var (
	addTargetDeploymentFlag string
	addVariantFlag          string
	addDryRunFlag           bool
	addForceFlag            bool
	addYesFlag              bool
	addPullFlag             bool
	addBuildFlag            bool
)

var configMcpAddCmd = &cobra.Command{
	Use:   "add <server>",
	Short: "Inject an MCP server into the current scaffold mode.",
	Long: `Renders the per-mode configuration for one MCP server and writes it
to fracta.yaml, deployment/docker-compose.yml, and/or deployment/k8s/manifests/.

A failed mutation rolls back to the pre-'add' state; the successful happy path
leaves no .bak files. Re-running without --force errors when the per-mode
entry already exists.`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigMcpAdd,
}

func init() {
	configMcpAddCmd.Flags().StringVar(&addTargetDeploymentFlag, "target-deployment", "",
		"Deployment mode: local | docker-compose | k8s. Default: only-enabled-mode if exactly one is scaffolded.")
	configMcpAddCmd.Flags().StringVar(&addVariantFlag, "variant", "",
		"Variant key from server.yaml (auto-resolved when unambiguous).")
	configMcpAddCmd.Flags().BoolVar(&addDryRunFlag, "dry-run", false,
		"Print pre-flight summary; no writes.")
	configMcpAddCmd.Flags().BoolVar(&addForceFlag, "force", false,
		"Overwrite existing per-mode entries.")
	configMcpAddCmd.Flags().BoolVar(&addYesFlag, "yes", false,
		"Skip the pull/build confirmation prompt.")
	configMcpAddCmd.Flags().BoolVar(&addPullFlag, "pull", false,
		"Eagerly 'docker pull <image>' after scaffolding.")
	configMcpAddCmd.Flags().BoolVar(&addBuildFlag, "build", false,
		"Eagerly 'docker build' (only if Dockerfile is fracta-owned).")

	configMcpCmd.AddCommand(configMcpAddCmd)
}

func runConfigMcpAdd(cmd *cobra.Command, args []string) error {
	serverID := args[0]

	cat, err := mcpcatalog.LoadProjectCatalog(projectRoot)
	if err != nil {
		if errors.Is(err, mcpcatalog.ErrNoCatalog) {
			return errNoCatalogRemediation
		}
		return err
	}

	entry, ok := cat.Get(serverID)
	if !ok {
		return fmt.Errorf("server %q not found in catalog (run 'fracta config mcp fetch' to refresh)", serverID)
	}

	state, err := mcpcatalog.LoadProjectState(projectRoot)
	if err != nil {
		return fmt.Errorf("read project state: %w", err)
	}

	mode, err := resolveAddMode(addTargetDeploymentFlag, state)
	if err != nil {
		return err
	}

	if !entry.SupportsMode(mode) {
		return fmt.Errorf("server %q does not support target-deployment %s: support.%s = %q",
			serverID, mode, supportKeyForCLI(mode), entry.SupportNote(mode))
	}

	if !state.EnabledScaffolds[mode] {
		return fmt.Errorf("scaffold %s is not enabled in this project; run 'fracta init --scaffold %s' first", mode, mode)
	}

	variant, ok := resolveAddVariant(entry, mode, addVariantFlag)
	if !ok {
		return fmt.Errorf("server %q has no variant suitable for target-deployment %s", serverID, mode)
	}

	plan, err := planAdd(projectRoot, entry, mode, variant, addForceFlag)
	if err != nil {
		return err
	}

	writeAddPreflight(cmd.OutOrStdout(), entry, mode, variant, plan)

	if addDryRunFlag {
		fmt.Fprintln(cmd.OutOrStdout(), "(dry run; no writes)")
		return nil
	}
	if !addYesFlag {
		fmt.Fprint(cmd.OutOrStdout(), "Proceed? [y/N] ")
		if !promptYes(cmd.InOrStdin()) {
			return errors.New("aborted")
		}
	}

	if err := plan.apply(); err != nil {
		return err
	}

	// Best-effort image pull/build.
	if addPullFlag || addBuildFlag {
		imgRef := entry.ImageRef()
		if addPullFlag && imgRef != "" {
			runBestEffort(cmd.OutOrStdout(), "docker", "pull", imgRef)
		}
		if addBuildFlag && entry.RequiresImageBuild() {
			runBestEffort(cmd.OutOrStdout(), "docker", "build", "-f", entry.Docker.Dockerfile, "-t", imgRef, ".")
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Added %q for target-deployment %s.\n", entry.ID, mode)
	return nil
}

func resolveAddMode(flag string, state *mcpcatalog.ProjectState) (scaffolds.Kind, error) {
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

func resolveAddVariant(e *mcpcatalog.Entry, mode scaffolds.Kind, explicit string) (string, bool) {
	if explicit != "" {
		if _, ok := e.Variants[explicit]; ok {
			return explicit, true
		}
		return "", false
	}
	return e.PreferredVariant(mode)
}

// supportKeyForCLI is the CLI-side name for the support.<key> field, mirroring
// the unexported supportKey in render.go. Duplicated here so we don't depend on
// an internal symbol.
func supportKeyForCLI(k scaffolds.Kind) string {
	switch k {
	case scaffolds.KindLocal:
		return "local_process"
	case scaffolds.KindDockerCompose:
		return "docker_compose"
	case scaffolds.KindK8s:
		return "kubernetes"
	}
	return ""
}

// addAction is one atomic step in an add plan. kind dictates the path through
// apply() and rollback().
//
// Two flavours of mutation:
//   - "fracta-yaml"     update mcp_servers.servers.<id>.<modeKey> in fracta.yaml
//   - "compose"         insert services.<id>-mcp into docker-compose.yml
//   - "env-example"     append KEY= lines into .env.example
//   - "write-file"      create a new k8s manifest file
//
// New-file writes have no .bak; updates to existing files write a .bak first
// so rollback can restore byte-identical state.
type addAction struct {
	kind        string
	description string
	// fracta-yaml / compose targets
	path string
	id   string
	mode scaffolds.Kind
	// new-file target
	body []byte
	// env-example
	envVars []string
	// compose service body
	serviceName string
	composeBody []byte
}

type addPlan struct {
	root    string
	entry   *mcpcatalog.Entry
	mode    scaffolds.Kind
	variant string
	force   bool

	// Rendered outputs.
	fractaBlock []byte
	k8sManifest []byte
	k8sSecret   []byte
	composeSvc  []byte

	// Targets.
	fractaYAML   string
	composeFile  string
	envExample   string
	k8sManifestP string
	k8sSecretP   string

	actions []addAction
}

func planAdd(root string, entry *mcpcatalog.Entry, mode scaffolds.Kind, variant string, force bool) (*addPlan, error) {
	p := &addPlan{
		root:         root,
		entry:        entry,
		mode:         mode,
		variant:      variant,
		force:        force,
		fractaYAML:   filepath.Join(root, "fracta.yaml"),
		composeFile:  filepath.Join(root, "deployment", "docker-compose.yml"),
		envExample:   filepath.Join(root, ".env.example"),
		k8sManifestP: filepath.Join(root, "deployment", "k8s", "manifests", entry.ID+"-mcp.yaml"),
		k8sSecretP:   filepath.Join(root, "deployment", "k8s", "manifests", entry.ID+"-mcp-secret.yaml"),
	}

	opts := mcpcatalog.RenderOpts{
		Variant:     variant,
		ServiceName: entry.ID + "-mcp",
	}

	// Always render the fracta.yaml block.
	block, err := entry.RenderFractaYAMLBlock(mode, opts)
	if err != nil {
		return nil, fmt.Errorf("render fracta.yaml block: %w", err)
	}
	p.fractaBlock = block

	switch mode {
	case scaffolds.KindLocal:
		p.actions = []addAction{
			{
				kind:        "fracta-yaml",
				description: "~ fracta.yaml (insert mcp_servers.servers." + entry.ID + ".local)",
				path:        p.fractaYAML, id: entry.ID, mode: mode, body: block,
			},
		}
	case scaffolds.KindDockerCompose:
		svc, err := entry.RenderComposeService(opts)
		if err != nil {
			return nil, fmt.Errorf("render compose service: %w", err)
		}
		p.composeSvc = svc

		p.actions = []addAction{
			{
				kind:        "compose",
				description: "~ deployment/docker-compose.yml (append services." + entry.ID + "-mcp)",
				path:        p.composeFile, serviceName: entry.ID + "-mcp", composeBody: svc,
			},
			{
				kind:        "fracta-yaml",
				description: "~ fracta.yaml (insert mcp_servers.servers." + entry.ID + ".remote)",
				path:        p.fractaYAML, id: entry.ID, mode: mode, body: block,
			},
		}
		if len(entry.Auth.EnvRequired) > 0 {
			p.actions = append(p.actions, addAction{
				kind:        "env-example",
				description: "~ .env.example (append " + strings.Join(entry.Auth.EnvRequired, ", ") + ")",
				path:        p.envExample, id: entry.ID, envVars: entry.Auth.EnvRequired,
			})
		}
	case scaffolds.KindK8s:
		manifest, err := entry.RenderK8sManifest(opts)
		if err != nil {
			return nil, fmt.Errorf("render k8s manifest: %w", err)
		}
		p.k8sManifest = manifest

		secret, err := entry.RenderK8sSecretStub(opts)
		if err != nil {
			return nil, fmt.Errorf("render k8s secret stub: %w", err)
		}
		p.k8sSecret = secret

		p.actions = []addAction{
			{
				kind:        "write-file",
				description: "+ " + p.k8sManifestP + " (new, Deployment+Service)",
				path:        p.k8sManifestP, body: manifest,
			},
		}
		if len(secret) > 0 {
			p.actions = append(p.actions, addAction{
				kind:        "write-file",
				description: "+ " + p.k8sSecretP + " (new, Secret stub)",
				path:        p.k8sSecretP, body: secret,
			})
		}
		p.actions = append(p.actions, addAction{
			kind:        "fracta-yaml",
			description: "~ fracta.yaml (insert mcp_servers.servers." + entry.ID + ".remote)",
			path:        p.fractaYAML, id: entry.ID, mode: mode, body: block,
		})
	}

	// New-file existence check unless --force.
	if !force {
		for _, a := range p.actions {
			if a.kind == "write-file" {
				if _, err := os.Stat(a.path); err == nil {
					return nil, fmt.Errorf("%s already exists; use --force to overwrite", a.path)
				}
			}
		}
	}
	return p, nil
}

func writeAddPreflight(w io.Writer, e *mcpcatalog.Entry, mode scaffolds.Kind, variant string, p *addPlan) {
	fmt.Fprintf(w, "Adding %q for target-deployment %s:\n", e.ID, mode)
	if variant != "" {
		fmt.Fprintf(w, "  Variant: %s\n", variant)
	}
	imgRef := e.ImageRef()
	if imgRef != "" {
		owner := "external"
		if c, ok := e.Variants["container"]; ok && c.ImageOwner != "" {
			owner = c.ImageOwner
		}
		fmt.Fprintf(w, "  Container required:    yes (image: %s, owner: %s)\n", imgRef, owner)
		if e.RequiresImageBuild() {
			fmt.Fprintln(w, "  Container build:       required (fracta-owned image)")
		} else {
			fmt.Fprintln(w, "  Container build:       not required (public image)")
		}
	} else {
		fmt.Fprintln(w, "  Container required:    no (stdio subprocess or remote URL)")
	}
	if len(e.Auth.EnvRequired) > 0 {
		modes := strings.Join(e.Auth.Modes, ",")
		fmt.Fprintf(w, "  Auth: %s; you must populate %s\n", modes, strings.Join(e.Auth.EnvRequired, ", "))
	}
	fmt.Fprintln(w, "  Files to be written:")
	for _, a := range p.actions {
		fmt.Fprintf(w, "    %s\n", a.description)
	}
}

func (p *addPlan) apply() error {
	// First normalize fracta.yaml — locks drift-prone scalars so the .bak
	// captures the post-normalize state (per spec §4 R5).
	if anyFractaYAML(p.actions) {
		if _, err := os.Stat(p.fractaYAML); err == nil {
			if _, err := mcpcatalog.NormalizeFractaYAML(p.fractaYAML); err != nil {
				return fmt.Errorf("normalize fracta.yaml: %w", err)
			}
		}
	}

	type bak struct{ path, bakPath string }
	var baks []bak
	var written []string

	cleanupBaks := func() {
		for _, b := range baks {
			_ = os.Remove(b.bakPath)
		}
	}
	rollback := func() {
		// Restore each .bak in reverse order.
		for i := len(baks) - 1; i >= 0; i-- {
			b := baks[i]
			data, err := os.ReadFile(b.bakPath)
			if err == nil {
				_ = os.WriteFile(b.path, data, 0o644)
			}
			_ = os.Remove(b.bakPath)
		}
		// Delete any newly-written files.
		for _, w := range written {
			_ = os.Remove(w)
		}
	}

	for _, a := range p.actions {
		// Snapshot existing content if the target file already exists.
		switch a.kind {
		case "fracta-yaml", "compose", "env-example":
			if data, err := os.ReadFile(a.path); err == nil {
				bakPath := a.path + ".bak"
				if err := os.WriteFile(bakPath, data, 0o644); err != nil {
					rollback()
					return fmt.Errorf("write .bak for %s: %w", a.path, err)
				}
				baks = append(baks, bak{path: a.path, bakPath: bakPath})
			}
		case "write-file":
			// New file — no .bak; rollback deletes it.
		}

		if err := applyAddAction(a, p.force); err != nil {
			rollback()
			return err
		}

		if a.kind == "write-file" {
			written = append(written, a.path)
		}
	}

	cleanupBaks()
	return nil
}

func applyAddAction(a addAction, force bool) error {
	switch a.kind {
	case "fracta-yaml":
		root, err := mcpcatalog.ReadFractaYAML(a.path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", a.path, err)
		}
		if root == nil {
			root, _ = mcpcatalog.ReadFractaYAML(a.path)
		}
		if err := mcpcatalog.UpsertMCPServer(root, a.id, a.mode, a.body, force); err != nil {
			return err
		}
		return mcpcatalog.WriteFractaYAMLAtomic(a.path, root)
	case "compose":
		root, err := mcpcatalog.ReadComposeYAML(a.path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", a.path, err)
		}
		if err := mcpcatalog.UpsertComposeService(root, a.serviceName, a.composeBody, force); err != nil {
			return err
		}
		return mcpcatalog.WriteComposeYAMLAtomic(a.path, root)
	case "env-example":
		return mcpcatalog.AppendEnvExample(a.path, a.id, a.envVars)
	case "write-file":
		if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
			return fmt.Errorf("mkdir for %s: %w", a.path, err)
		}
		return writeFileAtomicLocal(a.path, a.body)
	default:
		return fmt.Errorf("unknown add action kind %q", a.kind)
	}
}

// writeFileAtomicLocal mirrors mcpcatalog.writeFileAtomic (which is unexported);
// used for new k8s manifest files where we don't want a yaml round-trip.
func writeFileAtomicLocal(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("fsync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}

func anyFractaYAML(actions []addAction) bool {
	for _, a := range actions {
		if a.kind == "fracta-yaml" {
			return true
		}
	}
	return false
}

// runBestEffort runs a command, streaming output to w. Errors are logged to w
// but do not fail the surrounding add — spec-43 §5.6 marks --pull/--build as
// best-effort, non-fatal.
func runBestEffort(w io.Writer, name string, args ...string) {
	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(w, "warning: %s %s: %v\n", name, strings.Join(args, " "), err)
	}
}
 
