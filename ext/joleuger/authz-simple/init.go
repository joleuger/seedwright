package authz_simple

import (
	"context"

	"seedwright/internal/authz"
	"gopkg.in/yaml.v3"
)

func init() {
	authz.RegisterEngine("ext/joleuger/authz-simple", func(ctx context.Context, resolver authz.IdentityResolver, rawConfig yaml.Node) (authz.Enforcer, error) {
		cfg, err := ParseConfig(rawConfig)
		if err != nil {
			return nil, err
		}

		adapter, err := NewConfigAdapter(cfg)
		if err != nil {
			return nil, err
		}

		return NewSimpleEnforcer(adapter, resolver)
	})
}
