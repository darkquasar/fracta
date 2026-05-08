package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/darkquasar/fracta/internal/auth/credentials"
	"github.com/darkquasar/fracta/internal/config"
	"github.com/darkquasar/fracta/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	diagnoseRuntime string
	diagnoseConfigPath string
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Auth credential pipeline commands",
}

var authDiagnoseCmd = &cobra.Command{
	Use:   "diagnose",
	Short: "Diagnose credential pipeline for a host type",
	Long: `Runs BuildCredentialPlan + ExecuteCredentialPlan in dry-run mode and prints
the resolved credential origins, execution phases, runtime helper, binding,
assertions, and final merged env. Useful for debugging auth issues without
actually spawning an agent.`,
	RunE: runAuthDiagnose,
}

func init() {
	authDiagnoseCmd.Flags().StringVar(&diagnoseRuntime, "runtime", "", "runtime to diagnose (e.g. 'claude')")
	authDiagnoseCmd.Flags().StringVar(&diagnoseRuntime, "host-type", "", "deprecated: use --runtime instead")
	_ = authDiagnoseCmd.Flags().MarkDeprecated("host-type", "use --runtime instead")
	authDiagnoseCmd.Flags().StringVar(&diagnoseConfigPath, "config", "", "path to fracta config file (default: fracta.yaml in project root)")
	authCmd.AddCommand(authDiagnoseCmd)
	rootCmd.AddCommand(authCmd)
}

func runAuthDiagnose(cmd *cobra.Command, args []string) error {
	var (
		cfg *config.Config
		err error
	)
	if diagnoseConfigPath != "" {
		cfg, err = config.LoadConfig(diagnoseConfigPath)
		if err != nil {
			return fmt.Errorf("loading config %s: %w", diagnoseConfigPath, err)
		}
	} else {
		cfg, err = loadConfigOrDefault(projectRoot)
		if err != nil {
			return err
		}
	}

	runtimeType := diagnoseRuntime
	if runtimeType == "" {
		runtimeType = cfg.Agents.EffectiveDefaultRuntime()
		if runtimeType == "" {
			runtimeType = "claude"
		}
	}

	// Resolve credential profile.
	credProfile, hostBinding, err := config.ResolveCredentialProfile(cfg, runtimeType)
	if err != nil {
		return fmt.Errorf("resolve credential profile: %w", err)
	}
	if credProfile == nil {
		fmt.Printf("No credential profile configured for runtime type %q\n", runtimeType)
		return nil
	}

	runtimes := cfg.EffectiveRuntimes()
	profileName := runtimes[runtimeType].AuthProfile

	// Build host env.
	var hostEnv []runtime.EnvEntry
	if hc, ok := runtimes[runtimeType]; ok {
		hostEnv, _ = config.BuildHostEnv(hc, cfg.Runtime.Backend)
	}

	// Build credential plan.
	plan, err := credentials.BuildCredentialPlan(
		profileName,
		credentials.FromConfigProfile(credProfile),
		credentials.FromConfigBinding(hostBinding),
		hostEnv,
		credentials.PlanContext{
			Topology: credentials.TopologyHostEdge,
			DryRun:   true,
		},
	)
	if err != nil {
		return fmt.Errorf("build credential plan: %w", err)
	}

	// Execute in dry-run mode.
	output, err := credentials.ExecuteCredentialPlan(context.Background(), plan, credentials.PlanContext{
		Topology: credentials.TopologyHostEdge,
		DryRun:   true,
	})
	if err != nil {
		return fmt.Errorf("execute credential plan (dry-run): %w", err)
	}

	// Print diagnostic output.
	fmt.Printf("Credential Profile: %s\n", profileName)
	fmt.Printf("Topology: host_edge\n\n")

	// Sources.
	fmt.Println("Credential Origins:")
	if len(output.Plan.AuthOrigins) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, src := range output.Plan.AuthOrigins {
			scopeStr := ""
			cmdStr := ""
			deliveryStr := ""
			if src.AuthOrigin != nil {
				scopeStr = src.AuthOrigin.Scope
				if len(src.AuthOrigin.Command) > 0 {
					cmdStr = strings.Join(src.AuthOrigin.Command, " ")
				}
				if src.AuthOrigin.Delivery != "" {
					deliveryStr = src.AuthOrigin.Delivery
				}
			}
			phaseLabel := phaseDescription(src.Phase)
			fmt.Printf("  %-16s scope=%-13s phase=%-14s %s\n", src.Name, scopeStr, src.Phase, phaseLabel)
			if cmdStr != "" {
				fmt.Printf("    command: %s\n", cmdStr)
			}
			if deliveryStr != "" && src.AuthOrigin.Path != "" {
				fmt.Printf("    delivery: %s -> %s\n", deliveryStr, src.AuthOrigin.Path)
			}
		}
	}
	fmt.Println()

	// Resolver.
	if output.Plan.RuntimeAuthResolver != nil {
		r := output.Plan.RuntimeAuthResolver
		fmt.Printf("Runtime Helper: %s\n", r.Command)
		if r.TTLMs > 0 {
			fmt.Printf("  ttl: %dms\n", r.TTLMs)
		}
		if len(r.Order) > 0 {
			fmt.Printf("  source order (deprecated): [%s]\n", strings.Join(r.Order, ", "))
		}
		fmt.Println()
	}

	// Binding.
	if output.Plan.Binding != nil {
		b := output.Plan.Binding
		parts := []string{b.Type}
		if b.RuntimeAuthResolver != "" {
			parts = append(parts, "runtime_auth_resolver:"+b.RuntimeAuthResolver)
		}
		if b.AuthOrigin != "" {
			parts = append(parts, "auth_origin:"+b.AuthOrigin)
		}
		if b.EnvName != "" {
			parts = append(parts, "env:"+b.EnvName)
		}
		fmt.Printf("Binding: %s\n\n", strings.Join(parts, " -> "))
	}

	// Assertions.
	if output.Plan.Assertions != nil {
		fmt.Println("Assertions:")
		a := output.Plan.Assertions
		mergedEnv := output.Plan.Env
		for _, key := range a.RequireEnv {
			if val, ok := mergedEnv[key]; ok {
				fmt.Printf("  [pass] require_env %s=%s\n", key, val)
			} else {
				fmt.Printf("  [FAIL] require_env %s (not set)\n", key)
			}
		}
		for _, key := range a.ForbidEnv {
			if _, ok := mergedEnv[key]; ok {
				fmt.Printf("  [FAIL] forbid_env %s (is set!)\n", key)
			} else {
				fmt.Printf("  [pass] forbid_env %s (not set)\n", key)
			}
		}
		fmt.Println()
	}

	// Final merged env.
	if len(output.Plan.Env) > 0 {
		fmt.Println("Final Merged Env:")
		for k, v := range output.Plan.Env {
			fmt.Printf("  %s=%s\n", k, v)
		}
		fmt.Println()
	}

	// Diagnostics.
	if len(output.Diagnostics) > 0 {
		fmt.Println("Diagnostics:")
		for _, d := range output.Diagnostics {
			fmt.Printf("  [%s] %s: %s\n", d.Severity, d.Stage, d.Message)
		}
	}

	return nil
}

func phaseDescription(phase credentials.ExecutionPhase) string {
	switch phase {
	case credentials.PhasePrepareNow:
		return "(materialize now)"
	case credentials.PhaseRuntimeOnly:
		return "(runtime helper handles this later)"
	case credentials.PhaseUnavailable:
		return "(scope mismatch)"
	default:
		return ""
	}
}
