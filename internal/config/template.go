package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kiry163/llmmeter/internal/secure"
)

// templateBody is the annotated configuration written on first start. The
// values are substituted with yamlScalar so unusual paths stay valid YAML.
// It intentionally has no upstreams or gateway keys: the user adds the first
// one here or in the WebUI.
const templateBody = `# llmmeter configuration. This file contains secrets; keep it private.
#
# llmmeter created this file on first start. Add at least one upstream and one
# gateway key below, or use the WebUI under Settings. Saves from the server
# rewrite this file, so hand-written comments are not preserved.
version: 1
server:
  # HTTP listen address. Keep the loopback default unless an admin password is
  # configured.
  listen: %s
  # SQLite usage database. A relative path resolves against this file's
  # directory.
  database: %s
general:
  # IANA timezone used for reporting; "Local" follows the host timezone.
  timezone: %s
  # Days of request detail to keep.
  retention_days: 90
auth:
  # Argon2id admin password hash. Empty disables login for loopback requests.
  admin_password_hash: ""
  # Base64 32-byte session signing secret, generated for this first start.
  session_secret: %s
# OpenAI-compatible upstreams, for example:
#   - id: my-provider
#     name: My provider
#     base_url: https://api.example.com
#     api_key: sk-...
# Add the first upstream here or in the WebUI.
upstreams: []
# Local gateway keys bound to an upstream, for example:
#   - id: default
#     name: default
#     key: sk-local-...
#     upstream_id: my-provider
# Add the first key here or in the WebUI.
gateway_keys: []
`

// EnsureFile writes the annotated configuration template to path when the
// file does not exist. The template includes a freshly generated session
// secret, so the file is complete before the store loads it. It reports
// whether a file was created. An existing file, including one that is invalid,
// is left untouched. The parent directory is created with mode 0700 and the
// file is written atomically with mode 0600.
func EnsureFile(path string, p Paths) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, errors.New("config: file path must not be empty")
	}
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return false, nil
	case !errors.Is(err, fs.ErrNotExist):
		return false, fmt.Errorf("config: stat %s: %w", path, err)
	}

	secret, err := secure.RandomSecret()
	if err != nil {
		return false, fmt.Errorf("config: generate session secret: %w", err)
	}
	body := fmt.Sprintf(templateBody,
		yamlScalar(p.Listen),
		yamlScalar(p.Database),
		yamlScalar(time.Local.String()),
		yamlScalar(base64.StdEncoding.EncodeToString(secret)),
	)
	if err := writeFileAtomic(path, []byte(body), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// yamlScalar renders s as a single-line YAML scalar, quoting it only when the
// YAML encoder decides that is necessary.
func yamlScalar(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		return s
	}
	return strings.TrimSuffix(string(out), "\n")
}
