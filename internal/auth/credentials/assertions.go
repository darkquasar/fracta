package credentials

import (
	"fmt"

	"github.com/darkquasar/fracta/internal/fractalog"
)

// ValidateAssertions checks declarative rules against the final merged
// environment. Input env = host env + profile env + binding-derived env
// + source outputs. Returns diagnostics for each rule; errors indicate
// hard failures, warnings are advisory.
func ValidateAssertions(assertions *CredentialAssertions, mergedEnv map[string]string, sources []AnnotatedSource) []Diagnostic {
	if assertions == nil {
		return nil
	}
	log := fractalog.Component("credentials")
	var diags []Diagnostic

	for _, key := range assertions.RequireEnv {
		if val, ok := mergedEnv[key]; ok && val != "" {
			// Stage 5: credentials.assertion.pass
			log.Info("credentials.assertion.pass", "kind", "require_env", "key", key)
			diags = append(diags, Diagnostic{
				Severity: SeverityInfo,
				Stage:    "assertion.pass",
				Message:  fmt.Sprintf("require_env %s is set", key),
				Fields:   map[string]string{"kind": "require_env", "key": key},
			})
		} else {
			// Stage 5: credentials.assertion.fail
			log.Error("credentials.assertion.fail", "kind", "require_env", "key", key)
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Stage:    "assertion.fail",
				Message:  fmt.Sprintf("require_env %s is not set or empty", key),
				Fields:   map[string]string{"kind": "require_env", "key": key, "severity": "error"},
			})
		}
	}

	for _, key := range assertions.ForbidEnv {
		if _, ok := mergedEnv[key]; ok {
			log.Error("credentials.assertion.fail", "kind", "forbid_env", "key", key)
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Stage:    "assertion.fail",
				Message:  fmt.Sprintf("forbid_env %s is set but must not be", key),
				Fields:   map[string]string{"kind": "forbid_env", "key": key, "severity": "error"},
			})
		} else {
			log.Info("credentials.assertion.pass", "kind", "forbid_env", "key", key)
			diags = append(diags, Diagnostic{
				Severity: SeverityInfo,
				Stage:    "assertion.pass",
				Message:  fmt.Sprintf("forbid_env %s is not set", key),
				Fields:   map[string]string{"kind": "forbid_env", "key": key},
			})
		}
	}

	for _, sourceName := range assertions.RequireSource {
		found := false
		for _, src := range sources {
			if src.Name == sourceName && src.Phase != PhaseUnavailable {
				found = true
				break
			}
		}
		if found {
			log.Info("credentials.assertion.pass", "kind", "require_source", "key", sourceName)
			diags = append(diags, Diagnostic{
				Severity: SeverityInfo,
				Stage:    "assertion.pass",
				Message:  fmt.Sprintf("require_source %s is available", sourceName),
				Fields:   map[string]string{"kind": "require_source", "key": sourceName},
			})
		} else {
			log.Error("credentials.assertion.fail", "kind", "require_source", "key", sourceName)
			diags = append(diags, Diagnostic{
				Severity: SeverityError,
				Stage:    "assertion.fail",
				Message:  fmt.Sprintf("require_source %s is unavailable", sourceName),
				Fields:   map[string]string{"kind": "require_source", "key": sourceName, "severity": "error"},
			})
		}
	}

	for _, key := range assertions.WarnIfMissing {
		if val, ok := mergedEnv[key]; ok && val != "" {
			log.Info("credentials.assertion.pass", "kind", "warn_if_missing", "key", key)
			diags = append(diags, Diagnostic{
				Severity: SeverityInfo,
				Stage:    "assertion.pass",
				Message:  fmt.Sprintf("warn_if_missing %s is set", key),
				Fields:   map[string]string{"kind": "warn_if_missing", "key": key},
			})
		} else {
			log.Warn("credentials.assertion.fail", "kind", "warn_if_missing", "key", key)
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				Stage:    "assertion.fail",
				Message:  fmt.Sprintf("warn_if_missing %s is not set or empty", key),
				Fields:   map[string]string{"kind": "warn_if_missing", "key": key, "severity": "warning"},
			})
		}
	}

	return diags
}

// HasErrors returns true if any diagnostic has error severity.
func HasErrors(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}
