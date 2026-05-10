package prereq

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/darkquasar/fracta/internal/project/scaffolds"
)

// EnsureDeps preserves the pre-spec-42 entry point: git only. Equivalent to
// EnsureDepsFor(scaffolds.KindLocal).
func EnsureDeps() error {
	return EnsureDepsFor(scaffolds.KindLocal)
}

// EnsureDepsFor checks the host for the tools the chosen scaffold expects:
//
//   - KindLocal:         git
//   - KindDockerCompose: git, docker (with `docker compose` plugin)
//   - KindK8s:           git, kubectl (warns if no current kube-context)
//
// Returns a useful error naming the missing tools.
func EnsureDepsFor(kind scaffolds.Kind) error {
	switch kind {
	case scaffolds.KindLocal:
		return checkBinaries("git")
	case scaffolds.KindDockerCompose:
		if err := checkBinaries("git", "docker"); err != nil {
			return err
		}
		// Verify `docker compose` plugin is available (modern Docker
		// Desktop ships it; older installs use docker-compose v1).
		cmd := exec.Command("docker", "compose", "version")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("docker compose plugin missing or broken: %w; install Docker Desktop ≥ 20.10 or the docker-compose-plugin package", err)
		}
		return nil
	case scaffolds.KindK8s:
		if err := checkBinaries("git", "kubectl"); err != nil {
			return err
		}
		// Optional warning: no current kube-context. Doesn't fail init —
		// scaffolding works without a cluster, only `kubectl apply` later
		// needs one.
		ctxCmd := exec.Command("kubectl", "config", "current-context")
		if err := ctxCmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "prereq: warning: no current kubectl context; you can scaffold now and configure a cluster later")
		}
		return nil
	default:
		return fmt.Errorf("prereq: unknown scaffold kind %v", kind)
	}
}

func checkBinaries(names ...string) error {
	var missing []string
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required dependencies: %s", strings.Join(missing, ", "))
	}
	return nil
}
