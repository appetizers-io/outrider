package classifier

import (
	"os"
	"runtime"
	"strings"

	"github.com/appetizers-io/outrider/internal/config"
)

// Jev is fetched at run time (`npx -y jev-use@…`) and is given PR text to
// judge, so it runs with an allowlisted environment instead of outrider's
// own: everything it needs to start and reach its backend, and nothing else.
// A `kind: command` classifier is the user's own binary and keeps the full
// environment.
var (
	// jevEnvKeys are passed through by exact name.
	jevEnvKeys = []string{
		// Process basics, including the Windows ones Node needs to start.
		"PATH", "HOME", "USERPROFILE", "SystemRoot", "SystemDrive", "ComSpec",
		"PATHEXT", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "TMPDIR",
		// Locale, so output and JSON encoding behave.
		"LANG", "LC_ALL", "LC_CTYPE",
		// Reaching the backend through a proxy or a private CA.
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
	}
	// jevEnvPrefixes are passed through by prefix: the classifier's own
	// namespace, plus the backend keys it authenticates with.
	jevEnvPrefixes = []string{"JEV_", "TYPESAFE_"}
)

// jevEnv is the environment for a Jev child, built from environ
// ("KEY=value" entries, like os.Environ). Windows variable names are
// case-insensitive, and that is how Windows hands them over ("Path"), so
// ignoreCase decides how a name is matched.
func jevEnv(environ []string, ignoreCase bool) []string {
	fold := func(key string) string {
		if ignoreCase {
			return strings.ToUpper(key)
		}
		return key
	}
	allowed := make(map[string]bool, len(jevEnvKeys)+len(config.JevBackendEnv))
	for _, key := range jevEnvKeys {
		allowed[fold(key)] = true
	}
	for _, key := range config.JevBackendEnv {
		allowed[fold(key)] = true
	}
	out := []string{} // Empty, unlike nil, still means "this exact environment".
	for _, entry := range environ {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if allowed[fold(key)] {
			out = append(out, entry)
			continue
		}
		for _, prefix := range jevEnvPrefixes {
			if strings.HasPrefix(fold(key), fold(prefix)) {
				out = append(out, entry)
				break
			}
		}
	}
	return out
}

// JevEnv is jevEnv over this process's environment.
func JevEnv() []string { return jevEnv(os.Environ(), runtime.GOOS == "windows") }
