// Package scaffolds owns the file-tree templates `fracta init --scaffold`
// materializes into operator repositories, plus the Source/Apply machinery
// that walks them.
//
// A Source produces an fs.FS rooted at one scaffold kind (local |
// docker-compose | k8s). The Apply walker writes that tree into a destination
// directory, honoring a ConflictPolicy and the spec-42 §6 mode invariant
// (auth-helpers/* always 0755).
//
// See spec-42 §5 (scaffold contracts), §7 (source resolution).
package scaffolds

import "fmt"

// Kind enumerates the scaffold variants `fracta init --scaffold` supports.
type Kind int

const (
	// KindLocal scaffolds a local-process project: fracta.yaml with
	// runtime.backend=local, .fracta/ state dir, .gitignore.
	KindLocal Kind = iota + 1
	// KindDockerCompose scaffolds a docker-compose stack project. The agent
	// runs as a subprocess inside the controlplane container — see spec-34
	// §4.10 for why backend=local even in compose mode.
	KindDockerCompose
	// KindK8s scaffolds a Kubernetes deployment: fracta.yaml with
	// runtime.backend=kubernetes, manifests under fracta/k8s/manifests/, and
	// a fracta-auth-helpers ConfigMap reference.
	KindK8s
)

// String returns the canonical CLI/filesystem name for the kind.
func (k Kind) String() string {
	switch k {
	case KindLocal:
		return "local"
	case KindDockerCompose:
		return "docker-compose"
	case KindK8s:
		return "k8s"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// AllKinds returns the kinds in CLI-display order.
func AllKinds() []Kind {
	return []Kind{KindLocal, KindDockerCompose, KindK8s}
}

// ParseKind matches the CLI surface: "local", "docker-compose", "k8s".
// Unknown inputs return a typed error citing the valid options.
func ParseKind(s string) (Kind, error) {
	switch s {
	case "local":
		return KindLocal, nil
	case "docker-compose":
		return KindDockerCompose, nil
	case "k8s":
		return KindK8s, nil
	default:
		return 0, fmt.Errorf("unknown scaffold %q; valid: local, docker-compose, k8s", s)
	}
}
