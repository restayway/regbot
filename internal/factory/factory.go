// Package factory constructs configured registry providers.
package factory

import (
	"fmt"
	"time"

	"github.com/restayway/regbot/internal/config"
	gh "github.com/restayway/regbot/internal/provider/github"
	"github.com/restayway/regbot/internal/provider/oci"
	"github.com/restayway/regbot/pkg/provider"
)

func Providers(cfg *config.Config) (map[string]provider.Provider, error) {
	result := make(map[string]provider.Provider, len(cfg.Registries))
	for name, registry := range cfg.Registries {
		timeout := registry.Timeout.Duration
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		switch registry.Provider {
		case "oci":
			username, err := config.Secret(registry.Credentials.UsernameEnv, registry.Credentials.UsernameFile)
			if err != nil {
				return nil, fmt.Errorf("registry %s username: %w", name, err)
			}
			password, err := config.Secret(registry.Credentials.PasswordEnv, registry.Credentials.PasswordFile)
			if err != nil {
				return nil, fmt.Errorf("registry %s password: %w", name, err)
			}
			client, err := oci.New(name, registry.Endpoint, username, password, registry.TLS.CAFile, registry.TLS.InsecureSkipVerify, timeout, registry.Repositories)
			if err != nil {
				return nil, err
			}
			result[name] = client
		case "github":
			token, err := config.Secret(registry.TokenEnv, registry.TokenFile)
			if err != nil {
				return nil, fmt.Errorf("registry %s token: %w", name, err)
			}
			client, err := gh.New(name, registry.Endpoint, registry.Owner, registry.OwnerType, token, timeout)
			if err != nil {
				return nil, err
			}
			result[name] = client
		default:
			return nil, fmt.Errorf("unsupported provider %q", registry.Provider)
		}
	}
	return result, nil
}
