// Package settings defines authentication configuration.
package settings

import (
	"fmt"
	"net/url"
	"strings"
)

type Config struct {
	Static Static `yaml:"static"`
	OAuth  OAuth  `yaml:"oauth"`
}
type Static struct {
	Enabled bool     `yaml:"enabled"`
	Clients []Client `yaml:"clients"`
}
type Client struct {
	Name  string `yaml:"name"`
	Token string `yaml:"token"`
}
type OAuth struct {
	Enabled          bool     `yaml:"enabled"`
	Mode             string   `yaml:"mode"`
	Issuer           string   `yaml:"issuer"`
	Audience         string   `yaml:"audience"`
	JWKSURL          string   `yaml:"jwks_url"`
	IntrospectionURL string   `yaml:"introspection_url"`
	ClientID         string   `yaml:"client_id"`
	ClientSecretEnv  string   `yaml:"client_secret_env"`
	RequiredScopes   []string `yaml:"required_scopes"`
}

func (c Config) Validate() error {
	if !c.Static.Enabled && !c.OAuth.Enabled {
		return fmt.Errorf("HTTP submission requires at least one enabled auth provider")
	}
	if c.Static.Enabled {
		if len(c.Static.Clients) == 0 {
			return fmt.Errorf("auth.static.clients is required")
		}
		names := make(map[string]bool)
		for _, client := range c.Static.Clients {
			if strings.TrimSpace(client.Name) == "" || len(client.Name) > 128 || names[client.Name] {
				return fmt.Errorf("static clients need unique names (1-128 bytes)")
			}
			if strings.TrimSpace(client.Token) == "" || client.Token != strings.TrimSpace(client.Token) || len(client.Token) > 16384 {
				return fmt.Errorf("static client %q needs a token (1-16384 bytes, without surrounding whitespace)", client.Name)
			}
			names[client.Name] = true
		}
	}
	if c.OAuth.Enabled {
		if !validURL(c.OAuth.Issuer) || c.OAuth.Audience == "" {
			return fmt.Errorf("OAuth issuer URL and audience are required")
		}
		switch c.OAuth.Mode {
		case "jwt":
			if !validURL(c.OAuth.JWKSURL) {
				return fmt.Errorf("OAuth jwt mode requires jwks_url")
			}
		case "introspection":
			if !validURL(c.OAuth.IntrospectionURL) || c.OAuth.ClientID == "" || c.OAuth.ClientSecretEnv == "" {
				return fmt.Errorf("OAuth introspection requires endpoint, client_id and client_secret_env")
			}
		default:
			return fmt.Errorf("OAuth mode must be jwt or introspection")
		}
		for _, scope := range c.OAuth.RequiredScopes {
			if strings.TrimSpace(scope) == "" {
				return fmt.Errorf("OAuth required scopes must be nonempty")
			}
		}
	}
	return nil
}
func validURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.Fragment == ""
}
