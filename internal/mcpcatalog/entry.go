// Package mcpcatalog decodes and renders the per-project MCP server catalog
// kept at <root>/mcp-servers/. See spec-43 for the full contract.
package mcpcatalog

import (
	"strings"

	"github.com/darkquasar/fracta/internal/project/scaffolds"
)

// Entry is one MCP server entry decoded from <id>/server.yaml.
type Entry struct {
	ID           string                 `yaml:"id"`
	Name         string                 `yaml:"name"`
	Category     string                 `yaml:"category"`
	Status       string                 `yaml:"status"`
	Description  string                 `yaml:"description"`
	Upstream     UpstreamSpec           `yaml:"upstream"`
	Auth         AuthSpec               `yaml:"auth"`
	Variants     map[string]VariantSpec `yaml:"variants"`
	Support      SupportSpec            `yaml:"support"`
	Docker       DockerSpec             `yaml:"docker,omitempty"`
	Verification VerificationSpec       `yaml:"verification,omitempty"`
	Notes        string                 `yaml:"notes,omitempty"`
}

type UpstreamSpec struct {
	Type string `yaml:"type"`
	URL  string `yaml:"url"`
}

type AuthSpec struct {
	Modes       []string `yaml:"modes"`
	EnvRequired []string `yaml:"env_required,omitempty"`
	Notes       string   `yaml:"notes,omitempty"`
}

type VariantSpec struct {
	Transport    string   `yaml:"transport,omitempty"`
	Command      string   `yaml:"command,omitempty"`
	Args         []string `yaml:"args,omitempty"`
	Image        string   `yaml:"image,omitempty"`
	ImageOwner   string   `yaml:"image_owner,omitempty"`
	ServiceURL   string   `yaml:"service_url,omitempty"`
	URL          string   `yaml:"url,omitempty"`
	Auth         string   `yaml:"auth,omitempty"`
	FractaNative string   `yaml:"fracta_native,omitempty"`
}

// SupportSpec — three separate fields; one tag each. A single shared tag
// like `yaml:"local_process|docker_compose|kubernetes"` would silently fall
// back to lowercased name-matching.
type SupportSpec struct {
	LocalProcess  string `yaml:"local_process"`
	DockerCompose string `yaml:"docker_compose"`
	Kubernetes    string `yaml:"kubernetes"`
}

type DockerSpec struct {
	Dockerfile  string `yaml:"dockerfile,omitempty"`
	BuildTarget string `yaml:"build_target,omitempty"`
	LoadTarget  string `yaml:"load_target,omitempty"`
	ImageOwner  string `yaml:"image_owner,omitempty"`
}

type VerificationSpec struct {
	TestedOn              string   `yaml:"tested_on,omitempty"`
	SmokeTest             string   `yaml:"smoke_test,omitempty"`
	ExpectedToolsContains []string `yaml:"expected_tools_contains,omitempty"`
}

// SupportsMode returns true when the server entry supports the given scaffold
// mode. False for empty, "not_supported", and any "blocked_until_*" value.
func (e Entry) SupportsMode(k scaffolds.Kind) bool {
	v := e.SupportNote(k)
	if v == "" || v == "not_supported" {
		return false
	}
	if strings.HasPrefix(v, "blocked_until_") {
		return false
	}
	return true
}

// SupportNote returns the raw `support.<mode>` text for use in error messages.
func (e Entry) SupportNote(k scaffolds.Kind) string {
	switch k {
	case scaffolds.KindLocal:
		return e.Support.LocalProcess
	case scaffolds.KindDockerCompose:
		return e.Support.DockerCompose
	case scaffolds.KindK8s:
		return e.Support.Kubernetes
	}
	return ""
}

// RequiresImageBuild reports whether fracta owns the image (operator should
// build/push it, not pull). True when docker.dockerfile is set AND fracta is
// the image owner (either declared on docker.image_owner or on the container
// variant's image_owner field).
func (e Entry) RequiresImageBuild() bool {
	if e.Docker.Dockerfile == "" {
		return false
	}
	if e.Docker.ImageOwner == "fracta" {
		return true
	}
	if c, ok := e.Variants["container"]; ok && c.ImageOwner == "fracta" {
		return true
	}
	return false
}

// ImageRef returns the container variant's image reference, or "" if no
// container variant is declared.
func (e Entry) ImageRef() string {
	if c, ok := e.Variants["container"]; ok {
		return c.Image
	}
	return ""
}

// PreferredVariant resolves a scaffold kind to a variant name per spec §6.3:
//
//	local           → "local", else "local_proxy"
//	docker-compose  → "container"
//	k8s             → "container"
//
// Returns ("", false) when no suitable variant exists.
func (e Entry) PreferredVariant(k scaffolds.Kind) (string, bool) {
	switch k {
	case scaffolds.KindLocal:
		if _, ok := e.Variants["local"]; ok {
			return "local", true
		}
		if _, ok := e.Variants["local_proxy"]; ok {
			return "local_proxy", true
		}
		return "", false
	case scaffolds.KindDockerCompose, scaffolds.KindK8s:
		if _, ok := e.Variants["container"]; ok {
			return "container", true
		}
		return "", false
	}
	return "", false
}
