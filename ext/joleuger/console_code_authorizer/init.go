package console_code_authorizer

import (
	"seedwright/internal/authz"
)

func init() {
	authz.RegisterControlPlaneAuthenticator("ext/joleuger/console_code_authorizer", func() authz.ControlPlaneAuthenticator {
		return New()
	})
}
