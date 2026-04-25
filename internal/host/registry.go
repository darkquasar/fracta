package host

import (
	"errors"
	"fmt"
)

// ErrUnknownRuntime is returned when a runtime type is not registered.
var ErrUnknownRuntime = errors.New("unknown runtime type")

// ErrUnknownHost is a deprecated alias for ErrUnknownRuntime.
var ErrUnknownHost = ErrUnknownRuntime

// RuntimeRegistry resolves host implementations by runtime type name.
type RuntimeRegistry interface {
	// Get returns the Host implementation for a given runtime type.
	// Returns ErrUnknownRuntime if the type is not registered.
	Get(runtimeType string) (Host, error)

	// Default returns the default runtime type name and implementation.
	Default() (string, Host)
}

// HostRegistry is a deprecated alias for RuntimeRegistry.
type HostRegistry = RuntimeRegistry

// MapRegistry is a simple map-based HostRegistry.
type MapRegistry struct {
	hosts      map[string]Host
	defaultKey string
}

// NewMapRegistry creates a registry with the given default host type name.
// The default host must be registered via Register before calling Default.
func NewMapRegistry(defaultKey string) *MapRegistry {
	return &MapRegistry{
		hosts:      make(map[string]Host),
		defaultKey: defaultKey,
	}
}

// Register adds a host implementation under the given type name.
func (r *MapRegistry) Register(hostType string, h Host) {
	r.hosts[hostType] = h
}

// Get returns the Host for the given runtime type, or ErrUnknownRuntime.
func (r *MapRegistry) Get(runtimeType string) (Host, error) {
	h, ok := r.hosts[runtimeType]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownRuntime, runtimeType)
	}
	return h, nil
}

// Default returns the default runtime type name and implementation.
// Returns (defaultKey, nil) if the default key was never registered —
// callers must nil-check the Host before use.
func (r *MapRegistry) Default() (string, Host) {
	return r.defaultKey, r.hosts[r.defaultKey]
}
