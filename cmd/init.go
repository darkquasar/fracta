package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darkquasar/fracta/internal/project"
	"github.com/darkquasar/fracta/internal/project/scaffolds"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	initScaffold        string
	initSource          string
	initSourceChecksum  string
	initForce           bool
	initYes             bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize fracta in the current git repository",
	Long: `Initialize fracta in the current git repository by materializing a scaffold.

Examples:
  fracta init --scaffold local
  fracta init --scaffold docker-compose
  fracta init --scaffold k8s
  fracta init --scaffold k8s --source github:acme/fracta-scaffolds@v3.2.0
  fracta init --scaffold k8s --source ./local/templates
  fracta init --scaffold k8s --source https://example.com/scaffold-k8s.tar.gz \
    --source-checksum sha256:abc123...
  fracta init --scaffold local --force --yes

The first invocation drops a complete deployment tree into the operator's
repository. Re-running with --force overwrites existing files; without
--force, existing files are preserved.`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initScaffold, "scaffold", "", "Scaffold to lay down: local | docker-compose | k8s")
	initCmd.Flags().StringVar(&initSource, "source", "", "Override scaffold source (path, github:owner/repo@ref, or https://...)")
	initCmd.Flags().StringVar(&initSourceChecksum, "source-checksum", "", "Optional sha256:<hex> for https:// sources")
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing files")
	initCmd.Flags().BoolVar(&initYes, "yes", false, "Skip confirmation prompt for --force")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	root := projectRoot
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return err
		}
	}

	// BC3: --scaffold becomes required next release. For now warn and
	// default to local.
	if initScaffold == "" {
		fmt.Fprintln(os.Stderr, "warning: 'fracta init' without --scaffold is deprecated; defaulting to --scaffold local. This will be required in the next release.")
		initScaffold = "local"
	}

	kind, err := scaffolds.ParseKind(initScaffold)
	if err != nil {
		return err
	}

	// Refuse mode-mismatched re-runs. A project that already declares
	// runtime.backend: kubernetes can't be re-scaffolded as local without
	// producing an incoherent tree (the mode-specific manifests, configs,
	// and the top-level fracta.yaml would all disagree). One mode per
	// project. Re-running with the SAME kind is fine (idempotent).
	if err := checkExistingMode(root, kind); err != nil {
		return err
	}

	src, err := scaffolds.ResolveSource(context.Background(), initSource, kind, initSourceChecksum)
	if err != nil {
		return err
	}
	defer src.Close()

	policy := scaffolds.ConflictSkipExisting
	if initForce {
		if !initYes {
			fmt.Fprintf(os.Stderr, "fracta init --force will overwrite existing files at %s. Continue? [y/N] ", root)
			r := bufio.NewReader(os.Stdin)
			line, _ := r.ReadString('\n')
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y") {
				return fmt.Errorf("aborted")
			}
		}
		policy = scaffolds.ConflictOverwrite
	}

	res, err := project.Init(root, project.InitOpts{
		Scaffold:   kind,
		Source:     src,
		OnConflict: policy,
	})
	if err != nil {
		return err
	}

	desc := src.Description()
	// Embedded sources get a version annotation so operators know which
	// fracta build produced their tree. Spec-42 §5.7.
	if desc == "embedded" {
		desc = fmt.Sprintf("embedded (fracta %s)", Version())
	}

	fmt.Printf("Fracta initialized successfully.\n  scaffold: %s\n  source:   %s\n  files:    %d written, %d skipped\n",
		kind, desc, len(res.Written), len(res.Skipped))
	return nil
}

// checkExistingMode refuses re-runs that would mix scaffold modes in one
// project. Detection order:
//
//  1. If deployment/k8s/manifests/ exists, the project is a k8s scaffold.
//  2. Else if deployment/docker-compose.yml exists, it's a compose scaffold.
//  3. Else if fracta.yaml exists and parses, derive from runtime.backend
//     (local|kubernetes); a kubernetes value is a hard signal even without
//     manifests on disk yet.
//  4. Otherwise the project is empty/local — any kind is acceptable.
//
// Returns nil if requested == detected (idempotent re-run) or detection
// finds nothing (fresh init). Returns a clear error on mismatch.
func checkExistingMode(root string, want scaffolds.Kind) error {
	if _, err := os.Stat(filepath.Join(root, "deployment", "k8s", "manifests")); err == nil {
		return modeError(scaffolds.KindK8s, want)
	}
	if _, err := os.Stat(filepath.Join(root, "deployment", "docker-compose.yml")); err == nil {
		return modeError(scaffolds.KindDockerCompose, want)
	}
	yamlPath := filepath.Join(root, "fracta.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		// No fracta.yaml — fresh project, any kind is fine.
		return nil
	}
	var probe struct {
		Runtime struct {
			Backend string `yaml:"backend"`
		} `yaml:"runtime"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		// Malformed YAML — let the scaffold walker surface its own error
		// when it tries to write/skip. We don't want to pre-empt the more
		// useful downstream diagnostic with a parse error here.
		return nil
	}
	if probe.Runtime.Backend == "kubernetes" {
		return modeError(scaffolds.KindK8s, want)
	}
	// runtime.backend == "local" matches BOTH KindLocal and KindDockerCompose
	// (compose uses backend: local per spec-34 §4.10). Without one of the
	// disambiguating files above we can't tell them apart, so we treat this
	// case as "compatible with local OR docker-compose" — any of those two
	// kinds is allowed; only KindK8s is refused here.
	if want == scaffolds.KindK8s {
		return modeError(scaffolds.KindLocal, want)
	}
	return nil
}

func modeError(detected, want scaffolds.Kind) error {
	if detected == want {
		return nil
	}
	return fmt.Errorf("this project is already scaffolded as %s; cannot re-init as %s without losing customizations.\n"+
		"  - To switch modes destructively: rm -rf deployment/ fracta.yaml .fracta/ && fracta init --scaffold %s\n"+
		"  - To keep your existing %s setup: re-run fracta init --scaffold %s\n"+
		"  - For multi-mode patterns (one repo, multiple deployment targets), see the deployment overview in the docs.",
		detected, want, want, detected, detected)
}
