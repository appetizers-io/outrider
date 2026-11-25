package config

import (
	"fmt"
	"net/url"
	"strings"
)

// Forge is a forge besides GitHub, for example a self-hosted Forgejo.
type Forge struct {
	Kind      string  `yaml:"kind" jsonschema:"required,enum=forgejo" jsonschema_description:"forgejo: a Forgejo (or Gitea) instance."`
	Host      string  `yaml:"host" jsonschema:"required,pattern=^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]+)?$" jsonschema_description:"Host name, with a port when it has one. Repos on this forge are written host/owner/repo."`
	URL       *string `yaml:"url,omitempty" jsonschema:"pattern=^https?://[^/?#]+(/[^?#]*)?$,nullable" jsonschema_description:"Base URL when the forge is not at https://<host>, e.g. http://forge.lan:3000. The API is at <url>/api/v1."`
	Access    string  `yaml:"access,omitempty" jsonschema:"enum=api,enum=fj,default=api" jsonschema_description:"api: the REST API with a token from token_env or token_file. fj: the fj CLI (forgejo-cli) with its own login; outrider never sees a token."`
	TokenEnv  string  `yaml:"token_env,omitempty" jsonschema:"pattern=^[A-Za-z_][A-Za-z0-9_]*$" jsonschema_description:"access: api. Environment variable that holds the token. The token itself never goes into this file."`
	TokenFile string  `yaml:"token_file,omitempty" jsonschema:"minLength=1" jsonschema_description:"access: api. File that holds the token; ~/ is your home directory."`
}

// AccessMode is access with its default.
func (f Forge) AccessMode() string {
	if f.Access == "" {
		return "api"
	}
	return f.Access
}

// BaseURL is url without a trailing slash, else https://<host>.
func (f Forge) BaseURL() string {
	if f.URL != nil && *f.URL != "" {
		return strings.TrimRight(*f.URL, "/")
	}
	return "https://" + f.Host
}

// Forge is the configured forge of a host, nil when there is none.
func (c *Config) Forge(host string) *Forge {
	for i := range c.Forges {
		if strings.EqualFold(c.Forges[i].Host, host) {
			return &c.Forges[i]
		}
	}
	return nil
}

func (c *Config) checkForges() []string {
	var errs []string
	seen := map[string]bool{}
	for i, f := range c.Forges {
		prefix := fmt.Sprintf("forges[%d]", i)
		host := strings.ToLower(f.Host)
		if seen[host] {
			errs = append(errs, prefix+": host "+f.Host+" is configured twice")
		}
		seen[host] = true
		if host == "github.com" {
			errs = append(errs, prefix+": github.com is read through gh; remove it from forges")
		}
		if f.URL != nil {
			if u, err := url.Parse(*f.URL); err != nil || u.User != nil || u.Host == "" {
				errs = append(errs, prefix+".url: must be an http(s) URL without credentials")
			}
		}
		switch f.AccessMode() {
		case "api":
			if (f.TokenEnv == "") == (f.TokenFile == "") {
				errs = append(errs, prefix+": access: api needs exactly one of token_env and token_file")
			}
		case "fj":
			if f.TokenEnv != "" || f.TokenFile != "" {
				errs = append(errs, prefix+": access: fj uses fj's own login; remove token_env and token_file")
			}
		}
	}
	return errs
}
