package ocix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

// dockerConfigPath mirrors oras-go's resolution of the docker CLI config
// file: $DOCKER_CONFIG/config.json when set, else ~/.docker/config.json.
func dockerConfigPath() (string, error) {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return filepath.Join(dir, "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve docker config dir: %w", err)
	}
	return filepath.Join(home, ".docker", "config.json"), nil
}

// storedAuthHosts returns the registries the user has stored docker logins
// for, read from the config file at path. A missing file means no logins.
func storedAuthHosts(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Auths       map[string]json.RawMessage `json:"auths"`
		CredHelpers map[string]string          `json:"credHelpers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	hosts := make(map[string]bool, len(cfg.Auths)+len(cfg.CredHelpers))
	for key := range cfg.Auths {
		hosts[hostFromAuthKey(key)] = true
	}
	for key := range cfg.CredHelpers {
		hosts[hostFromAuthKey(key)] = true
	}
	return hosts, nil
}

// hostFromAuthKey reduces a docker config registry key to a bare host[:port].
// Keys are usually plain hostnames, but Docker Hub is stored as
// "https://index.docker.io/v1/" and some tools write scheme-prefixed keys.
func hostFromAuthKey(key string) string {
	host := strings.TrimPrefix(key, "https://")
	host = strings.TrimPrefix(host, "http://")
	host, _, _ = strings.Cut(host, "/")
	return host
}

// gatedCredential wraps delegate so it is consulted only for registries in
// stored. Docker credential helpers are exec'd even on lookup misses, and
// on macOS 15+ docker-credential-desktop reads Docker Desktop's protected
// group container, firing a "Terminal would like to access data from other
// apps" TCC prompt — so nem stays anonymous unless a login actually exists.
func gatedCredential(stored map[string]bool, delegate auth.CredentialFunc) auth.CredentialFunc {
	return func(ctx context.Context, hostport string) (auth.Credential, error) {
		key := hostFromAuthKey(credentials.ServerAddressFromHostname(hostport))
		if !stored[key] {
			return auth.EmptyCredential, nil
		}
		return delegate(ctx, hostport)
	}
}
