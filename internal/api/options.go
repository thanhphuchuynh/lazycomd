package api

import (
	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/configw"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
	"github.com/thanhphuchuynh/lazycomd/internal/probe"
)

// Options is everything a Server needs. Only Manager and Reload are required;
// the rest degrade to a smaller API rather than failing.
type Options struct {
	Manager *manager.Manager
	Reload  func() (*config.Config, error)

	// Token is required only when a TCP listener is configured; AuthHandler
	// rejects everything when it is empty.
	Token string

	// Sampler may be nil, in which case the probe fields are absent and
	// GET /v1/system returns an empty snapshot.
	Sampler *probe.Sampler

	// ConfigPath is the global config file, the target for writes to a bare
	// command name. Writers may be nil, which refuses every write with 501.
	ConfigPath string
	Writers    *configw.Registry
}

// New builds a Server from its options.
func New(o Options) *Server {
	return &Server{
		mgr:     o.Manager,
		token:   o.Token,
		reload:  o.Reload,
		probe:   o.Sampler,
		cfgPath: o.ConfigPath,
		writers: o.Writers,
	}
}
