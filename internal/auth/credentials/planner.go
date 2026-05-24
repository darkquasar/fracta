package credentials

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/runtime"
)

// PlanContext carries topology and options for plan building and execution.
type PlanContext struct {
	Topology Topology
	Logger   *slog.Logger
	DryRun   bool
}

// BuildCredentialPlan selects the effective binding, resolves sources and
// resolver from the profile, and annotates each source with its execution
// phase (prepare_now / runtime_only / unavailable) based on the current
// topology. Does NOT filter out runtime_only sources — they stay in the plan
// so the runtime helper can use them later.
func BuildCredentialPlan(
	profileName string,
	profile *CredentialProfile,
	hostBinding *CredentialBinding, // override from host config, may be nil
	hostEnv []runtime.EnvEntry,
	pctx PlanContext,
) (*CredentialPlan, error) {
	log := fractalog.Component("credentials")

	if profile == nil {
		return nil, fmt.Errorf("credential profile is nil")
	}

	resolverName := ""

	// Select effective binding: host override > profile default.
	binding := profile.DefaultBinding
	if hostBinding != nil {
		binding = hostBinding
	}
	if binding == nil {
		return nil, fmt.Errorf("credential profile %q: no binding (neither host override nor default_binding)", profileName)
	}

	// Resolve the binding's resolver.
	var resolver *CredentialResolver
	if binding.RuntimeAuthResolver != "" {
		r, ok := profile.RuntimeAuthResolvers[binding.RuntimeAuthResolver]
		if !ok {
			return nil, fmt.Errorf("credential profile %q: binding references unknown runtime_auth_resolver %q", profileName, binding.RuntimeAuthResolver)
		}
		resolver = &r
		resolverName = binding.RuntimeAuthResolver
		if len(resolver.Order) > 0 {
			log.Warn("credentials.resolver.order_deprecated",
				"profile", profileName,
				"resolver_name", resolverName,
				"order_count", len(resolver.Order),
			)
		}
	}

	// Stage 1: credentials.plan.build
	log.Info("credentials.plan.build",
		"profile", profileName,
		"binding_type", binding.Type,
		"resolver_name", resolverName,
		"topology", string(pctx.Topology),
		"source_count", len(profile.AuthOrigins),
	)

	// Merge env: start with profile env, overlay host env.
	mergedEnv := make(map[string]string)
	for k, v := range profile.Env {
		mergedEnv[k] = v
	}
	for _, e := range hostEnv {
		if e.SecretRef == nil {
			mergedEnv[e.Name] = e.Value
		}
	}

	// Annotate each source with execution phase.
	var sources []AnnotatedSource
	for name, src := range profile.AuthOrigins {
		srcCopy := src
		phase := annotatePhase(Scope(srcCopy.Scope), pctx.Topology)
		sources = append(sources, AnnotatedSource{
			AuthOrigin: &srcCopy,
			Name:       name,
			Phase:      phase,
		})

		// Stage 2: credentials.plan.source
		log.Info("credentials.plan.source",
			"source_name", name,
			"scope", srcCopy.Scope,
			"phase", string(phase),
		)
	}

	// Stage 3: credentials.plan.complete
	phaseCounts := countPhases(sources)
	envKeys := sortedKeys(mergedEnv)
	log.Info("credentials.plan.complete",
		"binding_type", binding.Type,
		"resolver", resolverName,
		"prepare_now", phaseCounts[PhasePrepareNow],
		"runtime_only", phaseCounts[PhaseRuntimeOnly],
		"unavailable", phaseCounts[PhaseUnavailable],
		"merged_env_keys", strings.Join(envKeys, ","),
	)

	return &CredentialPlan{
		Profile:             profileName,
		AuthOrigins:         sources,
		RuntimeAuthResolver: resolver,
		Binding:             binding,
		Env:                 mergedEnv,
		Assertions:          profile.Assertions,
	}, nil
}

// annotatePhase determines the execution phase for a source based on its
// scope and the current topology.
func annotatePhase(scope Scope, topology Topology) ExecutionPhase {
	switch scope {
	case ScopeAny:
		return PhasePrepareNow
	case ScopeHostEdge:
		if topology == TopologyHostEdge {
			return PhasePrepareNow
		}
		return PhaseUnavailable
	case ScopeAgentRuntime:
		return PhaseRuntimeOnly
	default:
		return PhaseUnavailable
	}
}

// ExecuteCredentialPlan materializes the plan:
// - Prepares host-edge artifacts (prepare_now sources)
// - Validates assertions against final merged env
// - Returns generic CredentialOutput for adapter projection
func ExecuteCredentialPlan(ctx context.Context, plan *CredentialPlan, pctx PlanContext) (*CredentialOutput, error) {
	log := fractalog.Component("credentials")

	var diags []Diagnostic
	secretData := make(map[string][]byte)
	var mountPath string

	// Process each source according to its phase.
	for i := range plan.AuthOrigins {
		src := &plan.AuthOrigins[i]
		switch src.Phase {
		case PhasePrepareNow:
			if src.MaterializedData != nil {
				// Already materialized (e.g., via rehydration) — use as-is.
				log.Info("credentials.source.prepare",
					"source_name", src.Name,
					"source_type", src.AuthOrigin.Type,
					"pre_materialized", true,
					"artifact_bytes", len(src.MaterializedData),
				)
				diags = append(diags, Diagnostic{
					Severity: SeverityInfo,
					Stage:    "source.prepare",
					Message:  fmt.Sprintf("source %s already materialized (%d bytes)", src.Name, len(src.MaterializedData)),
					Fields:   map[string]string{"source_name": src.Name, "source_type": src.AuthOrigin.Type, "pre_materialized": "true"},
				})
			} else if !pctx.DryRun {
				// Stage 4: credentials.source.prepare
				log.Info("credentials.source.prepare",
					"source_name", src.Name,
					"source_type", src.AuthOrigin.Type,
					"command_path", sourceCommandPath(src.AuthOrigin),
				)
				data, err := prepareSource(ctx, src)
				if err != nil {
					// Stage 4: credentials.source.fail
					log.Warn("credentials.source.fail",
						"source_name", src.Name,
						"required", src.AuthOrigin.IsRequired(),
						"error", scrubError(err),
					)
					diags = append(diags, Diagnostic{
						Severity: severityForSourceFailure(src.AuthOrigin),
						Stage:    "source.fail",
						Message:  fmt.Sprintf("source %s preparation failed: %v", src.Name, err),
						Fields:   map[string]string{"source_name": src.Name, "required": fmt.Sprintf("%v", src.AuthOrigin.IsRequired())},
					})
					if src.AuthOrigin.IsRequired() {
						return nil, fmt.Errorf("required source %q failed: %w", src.Name, err)
					}
					continue
				}
				src.MaterializedData = data
				// Stage 4: credentials.source.success
				log.Info("credentials.source.success",
					"source_name", src.Name,
					"artifact_bytes", len(data),
					"mount_path", src.AuthOrigin.Path,
				)
				diags = append(diags, Diagnostic{
					Severity: SeverityInfo,
					Stage:    "source.success",
					Message:  fmt.Sprintf("source %s prepared (%d bytes)", src.Name, len(data)),
					Fields:   map[string]string{"source_name": src.Name, "artifact_bytes": fmt.Sprintf("%d", len(data))},
				})
			} else {
				log.Info("credentials.source.prepare",
					"source_name", src.Name,
					"dry_run", true,
				)
				diags = append(diags, Diagnostic{
					Severity: SeverityInfo,
					Stage:    "source.prepare",
					Message:  fmt.Sprintf("source %s would be prepared (dry-run)", src.Name),
					Fields:   map[string]string{"source_name": src.Name, "dry_run": "true"},
				})
			}

			// Collect artifacts for K8s Secret.
			if src.MaterializedData != nil && src.AuthOrigin.Path != "" {
				key := filepath.Base(src.AuthOrigin.Path)
				secretData[key] = src.MaterializedData
				if mountPath == "" {
					mountPath = filepath.Dir(src.AuthOrigin.Path)
				}
			}

		case PhaseRuntimeOnly:
			diags = append(diags, Diagnostic{
				Severity: SeverityInfo,
				Stage:    "source.prepare",
				Message:  fmt.Sprintf("source %s is runtime_only (runtime helper handles it later)", src.Name),
				Fields:   map[string]string{"source_name": src.Name, "phase": "runtime_only"},
			})

		case PhaseUnavailable:
			diags = append(diags, Diagnostic{
				Severity: SeverityInfo,
				Stage:    "source.prepare",
				Message:  fmt.Sprintf("source %s is unavailable in current topology", src.Name),
				Fields:   map[string]string{"source_name": src.Name, "phase": "unavailable"},
			})
		}
	}

	// Validate assertions against the final merged env.
	assertionDiags := ValidateAssertions(plan.Assertions, plan.Env, plan.AuthOrigins)
	diags = append(diags, assertionDiags...)

	if HasErrors(assertionDiags) {
		return nil, fmt.Errorf("credential assertions failed: %s", summarizeErrors(assertionDiags))
	}

	// Build env entries from the plan's merged env.
	var envEntries []runtime.EnvEntry
	for k, v := range plan.Env {
		envEntries = append(envEntries, runtime.EnvEntry{Name: k, Value: v})
	}

	// Binding-specific credential delivery.
	// For claude_api_key_helper: adapter handles projection (via CredentialOutput.Plan).
	// For bearer_env: inject the materialized token directly as an env var.
	// For token_file: inject the token via SecretData (mounted as a file).
	if plan.Binding != nil {
		switch plan.Binding.Type {
		case "bearer_env":
			// Find the source referenced by the binding and inject its token as env.
			// If binding references a resolver (not a direct source), prefer the
			// single materialized source. Legacy resolver.order is still honored as a
			// compatibility fallback when multiple materialized sources exist.
			if plan.Binding.EnvName != "" {
				var token []byte
				var secretRef *runtime.SecretRef
				if plan.Binding.AuthOrigin != "" {
					// Direct source reference.
					for _, src := range plan.AuthOrigins {
						if src.Name != plan.Binding.AuthOrigin {
							continue
						}
						if src.MaterializedData != nil {
							token = src.MaterializedData
						} else if src.AuthOrigin.Type == "secret_env" && src.AuthOrigin.SecretRef != nil {
							secretRef = &runtime.SecretRef{
								Name: src.AuthOrigin.SecretRef.Name,
								Key:  src.AuthOrigin.SecretRef.Key,
							}
						}
						break
					}
				} else if plan.RuntimeAuthResolver != nil {
					var resolverDiags []Diagnostic
					token, resolverDiags = pickResolverToken(plan)
					diags = append(diags, resolverDiags...)
				}
				if token != nil {
					envEntries = append(envEntries, runtime.EnvEntry{
						Name:  plan.Binding.EnvName,
						Value: string(bytes.TrimSpace(token)),
					})
				} else if secretRef != nil {
					envEntries = append(envEntries, runtime.EnvEntry{
						Name:      plan.Binding.EnvName,
						SecretRef: secretRef,
					})
				}
			}
		case "token_file":
			// Find the source and inject as a secret file.
			srcName := plan.Binding.AuthOrigin
			if srcName != "" {
				for _, src := range plan.AuthOrigins {
					if src.Name == srcName && src.MaterializedData != nil && src.AuthOrigin.Path != "" {
						key := filepath.Base(src.AuthOrigin.Path)
						secretData[key] = src.MaterializedData
						if mountPath == "" {
							mountPath = filepath.Dir(src.AuthOrigin.Path)
						}
						break
					}
				}
			}
		}
		// claude_api_key_helper: no action here — Claude adapter projects from output.Plan.
	}

	return &CredentialOutput{
		Plan:        plan,
		EnvEntries:  envEntries,
		SecretData:  secretData,
		MountPath:   mountPath,
		Diagnostics: diags,
	}, nil
}

func pickResolverToken(plan *CredentialPlan) ([]byte, []Diagnostic) {
	var materialized []AnnotatedSource
	for _, src := range plan.AuthOrigins {
		if src.MaterializedData != nil {
			materialized = append(materialized, src)
		}
	}

	switch len(materialized) {
	case 0:
		return nil, nil
	case 1:
		return materialized[0].MaterializedData, nil
	}

	if plan.RuntimeAuthResolver != nil && len(plan.RuntimeAuthResolver.Order) > 0 {
		diags := []Diagnostic{{
			Severity: SeverityWarning,
			Stage:    "binding.project",
			Message:  "resolver.order is deprecated; prefer a single materialized source or let the runtime helper own fallback order",
			Fields:   map[string]string{"binding_type": "bearer_env", "deprecated_field": "resolver.order"},
		}}
		for _, orderName := range plan.RuntimeAuthResolver.Order {
			for _, src := range materialized {
				if src.Name == orderName {
					return src.MaterializedData, diags
				}
			}
		}
		return nil, diags
	}

	return nil, []Diagnostic{{
		Severity: SeverityWarning,
		Stage:    "binding.project",
		Message:  "bearer_env binding with runtime resolver is ambiguous without resolver.order when multiple sources are materialized",
		Fields:   map[string]string{"binding_type": "bearer_env", "resolver_name": plan.Binding.RuntimeAuthResolver},
	}}
}

// prepareSource executes a prepare_now source and returns its output data.
func prepareSource(ctx context.Context, src *AnnotatedSource) ([]byte, error) {
	switch src.AuthOrigin.Type {
	case "command_output":
		if len(src.AuthOrigin.Command) == 0 {
			return nil, fmt.Errorf("command_output source %q has no command", src.Name)
		}
		cmd := exec.CommandContext(ctx, src.AuthOrigin.Command[0], src.AuthOrigin.Command[1:]...)
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("command %v failed: %w", src.AuthOrigin.Command, err)
		}
		return output, nil

	case "secret_env":
		// secret_env sources are handled by K8s natively — nothing to prepare.
		return nil, nil

	case "http_header_token":
		// http_header_token sources with scope=host_edge should not normally
		// be prepare_now (they're typically agent_runtime), but if scope=any, we
		// can't execute HTTP requests at plan time — that's the resolver's job.
		return nil, fmt.Errorf("http_header_token sources cannot be prepared at plan time")

	default:
		return nil, fmt.Errorf("unknown source type %q", src.AuthOrigin.Type)
	}
}

// severityForSourceFailure returns error severity for required sources,
// warning for optional ones.
func severityForSourceFailure(src *CredentialSource) DiagnosticSeverity {
	if src.IsRequired() {
		return SeverityError
	}
	return SeverityWarning
}

// summarizeErrors returns a single-line summary of all error diagnostics.
func summarizeErrors(diags []Diagnostic) string {
	var msgs []string
	for _, d := range diags {
		if d.Severity == SeverityError {
			msgs = append(msgs, d.Message)
		}
	}
	if len(msgs) == 0 {
		return ""
	}
	result := msgs[0]
	for _, m := range msgs[1:] {
		result += "; " + m
	}
	return result
}

// countPhases returns a map from ExecutionPhase to count.
func countPhases(sources []AnnotatedSource) map[ExecutionPhase]int {
	counts := make(map[ExecutionPhase]int)
	for _, s := range sources {
		counts[s.Phase]++
	}
	return counts
}

// sortedKeys returns the keys of a map sorted alphabetically.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sourceCommandPath returns the command path for logging (never log full args
// which may contain secrets).
func sourceCommandPath(src *CredentialSource) string {
	if len(src.Command) > 0 {
		return src.Command[0]
	}
	return ""
}

// scrubError returns an error string with potential credential data removed.
// Only preserves the exit status and command path, not stdout/stderr content.
func scrubError(err error) string {
	if err == nil {
		return ""
	}
	// os/exec.ExitError includes stderr which may contain token data.
	// Return only the top-level error message.
	return fmt.Sprintf("%v", err)
}
