package auth

import (
	"mafuyu/internal/auth/credential"
	"mafuyu/internal/auth/oauth"
	"mafuyu/internal/auth/settings"
	"mafuyu/internal/auth/static"
)

type Middleware struct {
	static credential.Verifier
	oauth  credential.Verifier
}

func New(cfg settings.Config) (*Middleware, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	m := &Middleware{}
	var err error
	if cfg.Static.Enabled {
		m.static, err = static.New(cfg.Static)
		if err != nil {
			return nil, err
		}
	}
	if cfg.OAuth.Enabled {
		m.oauth, err = oauth.New(cfg.OAuth)
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}
