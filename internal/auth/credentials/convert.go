package credentials

import (
	"github.com/darkquasar/fracta/internal/config"
)

// FromConfigProfile converts config.CredentialProfile to credentials.CredentialProfile.
// This bridges the duplicate type definitions until they are unified.
func FromConfigProfile(cp *config.CredentialProfile) *CredentialProfile {
	if cp == nil {
		return nil
	}
	p := &CredentialProfile{
		Env: cp.Env,
	}
	if cp.AuthOrigins != nil {
		p.AuthOrigins = make(map[string]CredentialSource, len(cp.AuthOrigins))
		for name, src := range cp.AuthOrigins {
			p.AuthOrigins[name] = fromConfigSource(src)
		}
	}
	if cp.RuntimeAuthResolvers != nil {
		p.RuntimeAuthResolvers = make(map[string]CredentialResolver, len(cp.RuntimeAuthResolvers))
		for name, r := range cp.RuntimeAuthResolvers {
			p.RuntimeAuthResolvers[name] = CredentialResolver{
				Type:    r.Type,
				Command: r.Command,
				TTLMs:   r.TTLMs,
				Order:   r.Order,
			}
		}
	}
	if cp.Assertions != nil {
		p.Assertions = &CredentialAssertions{
			RequireEnv:    cp.Assertions.RequireEnv,
			ForbidEnv:     cp.Assertions.ForbidEnv,
			RequireSource: cp.Assertions.RequireSource,
			WarnIfMissing: cp.Assertions.WarnIfMissing,
		}
	}
	if cp.DefaultBinding != nil {
		p.DefaultBinding = fromConfigBinding(cp.DefaultBinding)
	}
	return p
}

// FromConfigBinding converts config.CredentialBinding to credentials.CredentialBinding.
func FromConfigBinding(cb *config.CredentialBinding) *CredentialBinding {
	return fromConfigBinding(cb)
}

func fromConfigBinding(cb *config.CredentialBinding) *CredentialBinding {
	if cb == nil {
		return nil
	}
	return &CredentialBinding{
		Type:              cb.Type,
		RuntimeAuthResolver: cb.RuntimeAuthResolver,
		AuthOrigin:        cb.AuthOrigin,
		EnvName:           cb.EnvName,
	}
}

func fromConfigSource(cs config.CredentialSource) CredentialSource {
	s := CredentialSource{
		Type:     cs.Type,
		Scope:    cs.Scope,
		Command:  []string(cs.Command),
		Delivery: cs.Delivery,
		Path:     cs.Path,
		Required: cs.Required,
		EnvName:  cs.EnvName,
	}
	if cs.Request != nil {
		s.Request = &HTTPRequest{
			Method:  cs.Request.Method,
			URL:     cs.Request.URL,
			Headers: cs.Request.Headers,
		}
	}
	if cs.Extract != nil {
		s.Extract = &ExtractConfig{
			Header: cs.Extract.Header,
		}
	}
	if cs.SecretRef != nil {
		s.SecretRef = &HostSecretRef{
			Name: cs.SecretRef.Name,
			Key:  cs.SecretRef.Key,
		}
	}
	return s
}
