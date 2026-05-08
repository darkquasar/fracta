package prereq

import (
	"fmt"
	"os/exec"
	"strings"
)

var requiredDeps = []string{"git"}

func EnsureDeps() error {
	var missing []string
	for _, dep := range requiredDeps {
		if _, err := exec.LookPath(dep); err != nil {
			missing = append(missing, dep)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required dependencies: %s", strings.Join(missing, ", "))
	}
	return nil
}
