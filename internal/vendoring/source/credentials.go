package source

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

const headerAuthorization = "Authorization"

// LookupFunc reads an environment variable, like os.LookupEnv.
type LookupFunc func(string) (string, bool)

// Authorize returns a client that adds the credentials named by s.Auth to requests for
// the scheme and host of cfg.Origin. Requests to other hosts, such as redirect targets,
// receive no credentials. Without auth, it returns client unchanged.
func Authorize(client *http.Client, cfg Config, lookup LookupFunc) (*http.Client, error) {
	if cfg.Auth == (Auth{}) {
		return client, nil
	}

	origin, err := url.Parse(cfg.Origin())
	if err != nil || !secureOrigin(origin) {
		return nil, fmt.Errorf("auth requires an HTTPS URL, or HTTP to a loopback host")
	}

	value, err := cfg.Auth.header(lookup)
	if err != nil {
		return nil, err
	}

	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	authorized := *client
	authorized.Transport = &credentialTransport{
		base:   base,
		scheme: origin.Scheme,
		host:   origin.Host,
		value:  value,
	}
	return &authorized, nil
}

// header builds a Bearer value, or a Basic value when a user variable is named.
func (a Auth) header(lookup LookupFunc) (string, error) {
	token, err := credential(lookup, a.TokenEnv)
	if err != nil {
		return "", err
	}
	if len(a.UserEnv) == 0 {
		return "Bearer " + token, nil
	}

	user, err := credential(lookup, a.UserEnv)
	if err != nil {
		return "", err
	}

	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token)), nil
}

// credential reads a variable. Errors name the variable and never include its value.
func credential(lookup LookupFunc, name string) (string, error) {
	value, ok := lookup(name)
	if !ok || len(value) == 0 {
		return "", fmt.Errorf("auth variable %s is not set", name)
	}
	if strings.ContainsFunc(value, unicode.IsControl) {
		return "", fmt.Errorf("auth variable %s contains control characters", name)
	}

	return value, nil
}

type credentialTransport struct {
	base   http.RoundTripper
	scheme string
	host   string
	value  string
}

// RoundTrip adds credentials only to requests for the configured scheme and host.
func (t *credentialTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != t.scheme || !strings.EqualFold(r.URL.Host, t.host) {
		return t.base.RoundTrip(r)
	}

	authorized := r.Clone(r.Context())
	authorized.Header.Set(headerAuthorization, t.value)
	return t.base.RoundTrip(authorized)
}
