package mcpcatalog

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/darkquasar/fracta/internal/project/scaffolds"
)

// RenderOpts controls rendering of fracta.yaml blocks and k8s/compose manifests.
//
// Namespace applies only to k8s renders. Variant overrides the entry's
// preferred variant; empty selects the spec §6.3 default. ServiceName defaults
// to "<id>-mcp"; ImageTag overrides Variants[Variant].Image when non-empty.
type RenderOpts struct {
	Namespace   string
	Variant     string
	ServiceName string
	ImageTag    string
}

const defaultK8sNamespace = "fracta"

// EnvBinding pairs an env-var name with the k8s Secret key it should read.
type EnvBinding struct {
	Name       string
	SecretName string
	SecretKey  string
}

// composeEnvPair pairs an env-var name with its docker-compose value
// expression (typically a `${VAR}` shell reference).
type composeEnvPair struct {
	Name  string
	Value string
}

// k8sRenderData is the input to the Deployment+Service template.
type k8sRenderData struct {
	ID                string
	ServiceName       string
	Namespace         string
	Image             string
	ImagePullPolicy   string
	ImageComment      string
	ContainerArgs     []string
	ContainerPort     int
	ServicePort       int
	TargetPortComment string
	Resources         ResourcesSpec
	SecretName        string
	EnvBindings       []EnvBinding
}

// composeRenderData is the input to the compose-service template.
type composeRenderData struct {
	ServiceName     string
	Image           string
	ContainerArgs   []string
	EnvComposePairs []composeEnvPair
	Healthcheck     *ComposeHealthcheckSpec
}

// fractaYAMLK8sData / fractaYAMLLocalData are inputs to the per-mode fracta.yaml block templates.
type fractaYAMLK8sData struct {
	ID          string
	ServiceURL  string
	Transport   string
	EnvRequired []string
}

type fractaYAMLLocalData struct {
	ID          string
	Command     string
	Args        []string
	EnvRequired []string
}

// RenderFractaYAMLBlock emits the YAML snippet that gets spliced under
// `mcp_servers.servers.<id>` for the given mode.
func (e *Entry) RenderFractaYAMLBlock(mode scaffolds.Kind, opts RenderOpts) ([]byte, error) {
	if !e.SupportsMode(mode) {
		return nil, fmt.Errorf("mcpcatalog: server %q does not support mode %s (support.%s=%q)",
			e.ID, mode, supportKey(mode), e.SupportNote(mode))
	}
	variant, ok := e.resolveVariant(mode, opts.Variant)
	if !ok {
		return nil, fmt.Errorf("mcpcatalog: server %q has no variant suitable for mode %s", e.ID, mode)
	}
	v := e.Variants[variant]

	switch mode {
	case scaffolds.KindLocal:
		args := make([]string, len(v.Args))
		for i, a := range v.Args {
			args[i] = quoteForList(a)
		}
		return renderTemplate("templates/local/fracta-yaml-block.tmpl", fractaYAMLLocalData{
			ID:          e.ID,
			Command:     v.Command,
			Args:        args,
			EnvRequired: e.Auth.EnvRequired,
		})
	case scaffolds.KindDockerCompose:
		return renderTemplate("templates/docker-compose/fracta-yaml-block.tmpl", fractaYAMLK8sData{
			ID:          e.ID,
			ServiceURL:  v.ServiceURL,
			Transport:   v.Transport,
			EnvRequired: e.Auth.EnvRequired,
		})
	case scaffolds.KindK8s:
		return renderTemplate("templates/k8s/fracta-yaml-block.tmpl", fractaYAMLK8sData{
			ID:          e.ID,
			ServiceURL:  v.ServiceURL,
			Transport:   v.Transport,
			EnvRequired: e.Auth.EnvRequired,
		})
	}
	return nil, fmt.Errorf("mcpcatalog: unknown mode %v", mode)
}

// RenderK8sManifest renders the Deployment + Service YAML for the given entry.
func (e *Entry) RenderK8sManifest(opts RenderOpts) ([]byte, error) {
	if !e.SupportsMode(scaffolds.KindK8s) {
		return nil, fmt.Errorf("mcpcatalog: server %q does not support kubernetes (support.kubernetes=%q)",
			e.ID, e.SupportNote(scaffolds.KindK8s))
	}
	variant, ok := e.resolveVariant(scaffolds.KindK8s, opts.Variant)
	if !ok {
		return nil, fmt.Errorf("mcpcatalog: server %q has no variant suitable for kubernetes", e.ID)
	}
	v := e.Variants[variant]

	ns := opts.Namespace
	if ns == "" {
		ns = defaultK8sNamespace
	}
	svcName := opts.ServiceName
	if svcName == "" {
		svcName = e.ID + "-mcp"
	}
	image := opts.ImageTag
	if image == "" {
		image = v.Image
	}
	if image == "" {
		return nil, fmt.Errorf("mcpcatalog: server %q has no container image", e.ID)
	}
	pullPolicy := "IfNotPresent"
	if v.ImageOwner == "fracta" || e.Docker.ImageOwner == "fracta" {
		pullPolicy = "Never"
	}
	containerPort := v.ContainerPort
	if containerPort == 0 {
		containerPort = 3000
	}
	servicePort := v.ServicePort
	if servicePort == 0 {
		servicePort = 3000
	}
	targetPortComment := ""
	if containerPort != servicePort {
		targetPortComment = fmt.Sprintf("container listens on %d", containerPort)
	}
	resources := v.Resources
	resources = resourcesWithDefaults(resources)

	data := k8sRenderData{
		ID:                e.ID,
		ServiceName:       svcName,
		Namespace:         ns,
		Image:             image,
		ImagePullPolicy:   pullPolicy,
		ImageComment:      v.ImageComment,
		ContainerArgs:     v.ContainerArgs,
		ContainerPort:     containerPort,
		ServicePort:       servicePort,
		TargetPortComment: targetPortComment,
		Resources:         resources,
		SecretName:        svcName + "-secrets",
		EnvBindings:       e.envBindings(svcName),
	}
	return renderTemplate("templates/k8s/deployment-service.tmpl", data)
}

// RenderK8sSecretStub renders the Secret stub for env_required vars. Returns
// (nil, nil) when no env vars are declared — caller skips writing.
func (e *Entry) RenderK8sSecretStub(opts RenderOpts) ([]byte, error) {
	if len(e.Auth.EnvRequired) == 0 {
		return nil, nil
	}
	ns := opts.Namespace
	if ns == "" {
		ns = defaultK8sNamespace
	}
	svcName := opts.ServiceName
	if svcName == "" {
		svcName = e.ID + "-mcp"
	}
	data := struct {
		ServiceName string
		Namespace   string
		SecretName  string
		EnvBindings []EnvBinding
	}{
		ServiceName: svcName,
		Namespace:   ns,
		SecretName:  svcName + "-secrets",
		EnvBindings: e.envBindings(svcName),
	}
	return renderTemplate("templates/k8s/secret.tmpl", data)
}

// RenderComposeService renders the docker-compose service block for the given
// entry.
func (e *Entry) RenderComposeService(opts RenderOpts) ([]byte, error) {
	if !e.SupportsMode(scaffolds.KindDockerCompose) {
		return nil, fmt.Errorf("mcpcatalog: server %q does not support docker-compose (support.docker_compose=%q)",
			e.ID, e.SupportNote(scaffolds.KindDockerCompose))
	}
	variant, ok := e.resolveVariant(scaffolds.KindDockerCompose, opts.Variant)
	if !ok {
		return nil, fmt.Errorf("mcpcatalog: server %q has no variant suitable for docker-compose", e.ID)
	}
	v := e.Variants[variant]
	svcName := opts.ServiceName
	if svcName == "" {
		svcName = e.ID + "-mcp"
	}
	image := opts.ImageTag
	if image == "" {
		image = v.Image
	}

	var pairs []composeEnvPair
	for _, env := range e.Auth.EnvRequired {
		val, ok := e.Auth.EnvComposeValues[env]
		if !ok {
			val = "${" + env + "}"
		}
		pairs = append(pairs, composeEnvPair{Name: env, Value: val})
	}

	data := composeRenderData{
		ServiceName:     svcName,
		Image:           image,
		ContainerArgs:   v.ContainerArgs,
		EnvComposePairs: pairs,
		Healthcheck:     v.ComposeHealthcheck,
	}
	return renderTemplate("templates/docker-compose/compose-service.tmpl", data)
}

// envBindings returns the env→secret bindings for the entry, using
// Auth.EnvSecretKeys overrides where present and the LCP-strip-and-kebab
// default elsewhere.
func (e *Entry) envBindings(svcName string) []EnvBinding {
	if len(e.Auth.EnvRequired) == 0 {
		return nil
	}
	prefix := longestCommonPrefixUnderscore(e.Auth.EnvRequired)
	out := make([]EnvBinding, 0, len(e.Auth.EnvRequired))
	for _, env := range e.Auth.EnvRequired {
		key, ok := e.Auth.EnvSecretKeys[env]
		if !ok {
			key = kebabAfterPrefix(env, prefix)
		}
		out = append(out, EnvBinding{
			Name:       env,
			SecretName: svcName + "-secrets",
			SecretKey:  key,
		})
	}
	return out
}

// resolveVariant picks the variant to render for a given mode, honoring
// opts.Variant first then falling back to the spec §6.3 preference.
func (e *Entry) resolveVariant(mode scaffolds.Kind, override string) (string, bool) {
	if override != "" {
		if _, ok := e.Variants[override]; ok {
			return override, true
		}
		return "", false
	}
	return e.PreferredVariant(mode)
}

// renderTemplate parses one template from the embedded FS and executes it.
func renderTemplate(path string, data any) ([]byte, error) {
	raw, err := templatesFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mcpcatalog: read template %s: %w", path, err)
	}
	tmpl, err := template.New(path).Funcs(template.FuncMap{
		"quoteHealthcheck": quoteHealthcheck,
	}).Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("mcpcatalog: parse template %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("mcpcatalog: execute template %s: %w", path, err)
	}
	return buf.Bytes(), nil
}

// supportKey returns the catalog support.<key> name for a kind.
func supportKey(k scaffolds.Kind) string {
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

// longestCommonPrefixUnderscore returns the longest prefix that all inputs
// share AND that ends in an underscore — so we never split a token. Returns
// "" when there's no shared underscore-terminated prefix.
func longestCommonPrefixUnderscore(in []string) string {
	if len(in) == 0 {
		return ""
	}
	prefix := in[0]
	for _, s := range in[1:] {
		prefix = commonPrefix(prefix, s)
	}
	// Trim back to the last underscore (we never split mid-token).
	for i := len(prefix); i > 0; i-- {
		if prefix[i-1] == '_' {
			return prefix[:i]
		}
	}
	return ""
}

func commonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}

// kebabAfterPrefix strips prefix from name, lowercases the rest, and replaces
// underscores with dashes (the kebab convention used by k8s secret keys).
func kebabAfterPrefix(name, prefix string) string {
	rest := strings.TrimPrefix(name, prefix)
	rest = strings.ToLower(rest)
	return strings.ReplaceAll(rest, "_", "-")
}

// resourcesWithDefaults fills in the canonical fracta resource envelope when
// the entry leaves both blocks zero.
func resourcesWithDefaults(r ResourcesSpec) ResourcesSpec {
	if r.Requests.Memory == "" {
		r.Requests.Memory = "256Mi"
	}
	if r.Requests.CPU == "" {
		r.Requests.CPU = "100m"
	}
	if r.Limits.Memory == "" {
		r.Limits.Memory = "1Gi"
	}
	if r.Limits.CPU == "" {
		r.Limits.CPU = "500m"
	}
	return r
}

// quoteForList quotes a stdio arg the way an operator hand-edits fracta.yaml:
// bare tokens stay bare; tokens containing whitespace, leading dashes, or
// special chars get double-quoted.
func quoteForList(s string) string {
	if needsQuotes(s) {
		return strconv_Quote(s)
	}
	return s
}

func needsQuotes(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\'' || r == '#' {
			return true
		}
	}
	return false
}

// strconv_Quote is a one-line wrapper kept package-local so the import list
// stays tidy.
func strconv_Quote(s string) string {
	return "\"" + s + "\""
}

// quoteHealthcheck quotes a healthcheck arg for YAML flow-list output.
// All values are double-quoted; embedded `"` characters are backslash-escaped.
// This matches the docker-compose convention used by the existing fracta
// stack, where the healthcheck test is rendered as a flow-style list of
// quoted strings.
func quoteHealthcheck(s string) string {
	return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\""
}
