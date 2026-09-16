// Package secret reads a credential from a Docker or Kubernetes secret
// file first, then falls back to an environment variable.
package secret

import (
	"os"
	"path/filepath"
	"strings"
)

// dir is the folder that holds a mounted secret file. Tests override it.
var dir = "/run/secrets"

// Read returns the trimmed content of dir/name if that file exists,
// otherwise env[envVar].
func Read(name, envVar string, env map[string]string) string {
	if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
		return strings.TrimSpace(string(b))
	}
	return env[envVar]
}
