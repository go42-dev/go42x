package source

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

var (
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?(?:\+[A-Za-z0-9.-]+)?$`)
	tagPattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)
	packagePattern = regexp.MustCompile(`^(?:@[a-z0-9._-]+/)?[a-z0-9][a-z0-9._-]*$`)
)

// Config describes an upstream source independently of the manifest and adapters.
type Config struct {
	Type     string `yaml:"type"`
	URL      string `yaml:"url,omitempty"`
	Ref      string `yaml:"ref,omitempty"`
	SHA256   string `yaml:"sha256,omitempty"`
	Package  string `yaml:"package,omitempty"`
	Version  string `yaml:"version,omitempty"`
	Registry string `yaml:"registry,omitempty"`
}

type Resolved struct {
	URL       string `yaml:"url,omitempty"`
	Commit    string `yaml:"commit,omitempty"`
	Version   string `yaml:"version,omitempty"`
	Integrity string `yaml:"integrity,omitempty"`
	SHA256    string `yaml:"sha256,omitempty"`
}

type Result struct {
	Root     string
	Resolved Resolved
}

// WithDefaults normalizes source values before validation and fingerprinting.
func (s Config) WithDefaults() Config {
	if s.Type == "npm" && s.Registry == "" {
		s.Registry = "https://registry.npmjs.org"
	}
	s.SHA256 = strings.ToLower(s.SHA256)
	return s
}

func ValidSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

func ExactVersion(v string) bool { return versionPattern.MatchString(v) }

func (s Config) Validate() error {
	switch s.Type {
	case "git":
		u, err := url.Parse(s.URL)
		if err != nil || u.Scheme == "" || s.Ref == "" || strings.HasPrefix(s.Ref, "-") ||
			strings.ContainsAny(s.Ref, "\x00\r\n") {
			return fmt.Errorf("git requires a repository URL and ref")
		}
		if u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh" && u.Scheme != "file" {
			return fmt.Errorf("unsupported Git URL scheme")
		}
		if (u.Scheme != "file" && u.Host == "") || u.Path == "" || strings.Contains(u.Path, "//") ||
			strings.ContainsAny(u.Path, "\\\x00") {
			return fmt.Errorf("git URL requires a repository path without getter subdirectory syntax")
		}
		if u.RawQuery != "" || u.Fragment != "" || (u.User != nil && hasPassword(u)) {
			return fmt.Errorf("git URL cannot contain query, fragment or password")
		}
		if s.Package != "" || s.Version != "" || s.Registry != "" || s.SHA256 != "" {
			return fmt.Errorf("git source contains unrelated fields")
		}
	case "http":
		if err := ValidateHTTPURL(s.URL); err != nil {
			return err
		}
		if s.SHA256 != "" && !ValidSHA256(s.SHA256) {
			return fmt.Errorf("sha256 must be a 64-character hexadecimal digest")
		}
		if s.Ref != "" || s.Package != "" || s.Version != "" || s.Registry != "" {
			return fmt.Errorf("http source contains unrelated fields")
		}
	case "npm":
		if !packagePattern.MatchString(s.Package) || (!ExactVersion(s.Version) && !tagPattern.MatchString(s.Version)) {
			return fmt.Errorf("npm requires a package and exact version or dist-tag (ranges are unsupported)")
		}
		u, err := parseHTTPURL(s.Registry)
		if err != nil {
			return fmt.Errorf("npm registry: %w", err)
		}
		if u.RawQuery != "" {
			return fmt.Errorf("npm registry cannot contain a query")
		}
		if s.URL != "" || s.Ref != "" || s.SHA256 != "" {
			return fmt.Errorf("npm source contains unrelated fields")
		}
	default:
		return fmt.Errorf("unsupported source type %q", s.Type)
	}
	return nil
}

func ValidateHTTPURL(value string) error {
	_, err := parseHTTPURL(value)
	return err
}

func parseHTTPURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("expected an HTTP(S) URL without credentials or fragment")
	}
	if strings.Contains(u.Path, "//") || strings.ContainsAny(u.Path, "\\:\x00") {
		return nil, fmt.Errorf("URL path cannot contain getter subdirectory syntax or non-portable characters")
	}
	for _, key := range []string{"archive", "checksum", "filename", "sshkey", "ref"} {
		if u.Query().Has(key) {
			return nil, fmt.Errorf("URL query %q is reserved; use source fields", key)
		}
	}
	return u, nil
}

func hasPassword(u *url.URL) bool { _, ok := u.User.Password(); return ok }

func ValidatePath(value string) error {
	if value == "" || value == "." || path.IsAbs(value) || path.Clean(value) != value ||
		strings.ContainsAny(value, "\\:\x00") {
		return fmt.Errorf("expected a portable relative file path: %q", value)
	}
	if slices.Contains(strings.Split(value, "/"), "..") {
		return fmt.Errorf("path escapes its root: %q", value)
	}
	return nil
}

// IntegrityChecksum translates registry SRI into go-getter's checksum syntax,
// preferring the strongest supported algorithm rather than token order.
func IntegrityChecksum(value string) (string, error) {
	for _, algorithm := range []struct {
		name string
		size int
	}{{"sha512", 64}, {"sha256", 32}} {
		for _, token := range strings.Fields(value) {
			prefix := algorithm.name + "-"
			if !strings.HasPrefix(token, prefix) {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(token, prefix))
			if err != nil || len(data) != algorithm.size {
				return "", fmt.Errorf("invalid npm integrity value")
			}
			return algorithm.name + ":" + hex.EncodeToString(data), nil
		}
	}
	return "", fmt.Errorf("npm integrity must include SHA-256 or SHA-512")
}
