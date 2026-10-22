package source

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
)

const (
	TypeGit  = "git"
	TypeHTTP = "http"
	TypeNPM  = "npm"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
	schemeSSH   = "ssh"
	schemeFile  = "file"
)

const (
	checksumSHA256 = "sha256"
	checksumSHA512 = "sha512"
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

type integrityAlgorithm struct {
	name string
	size int
}

// WithDefaults normalizes source values before validation and fingerprinting.
func (s Config) WithDefaults() Config {
	if s.Type == TypeNPM && len(s.Registry) == 0 {
		s.Registry = "https://registry.npmjs.org"
	}
	s.SHA256 = strings.ToLower(s.SHA256)
	return s
}

func ValidSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func ExactVersion(v string) bool {
	return versionPattern.MatchString(v)
}

func (s Config) Validate() error {
	switch s.Type {
	case TypeGit:
		u, err := url.Parse(s.URL)
		if err != nil || len(u.Scheme) == 0 || len(s.Ref) == 0 || strings.HasPrefix(s.Ref, "-") ||
			strings.ContainsAny(s.Ref, "\x00\r\n") {
			return fmt.Errorf("git requires a repository URL and ref")
		}
		if u.Scheme != schemeHTTPS && u.Scheme != schemeHTTP && u.Scheme != schemeSSH && u.Scheme != schemeFile {
			return fmt.Errorf("unsupported Git URL scheme")
		}
		if (u.Scheme != schemeFile && len(u.Host) == 0) || len(u.Path) == 0 || strings.Contains(u.Path, "//") ||
			strings.ContainsAny(u.Path, "\\\x00") {
			return fmt.Errorf("git URL requires a repository path without getter subdirectory syntax")
		}
		if len(u.RawQuery) > 0 || len(u.Fragment) > 0 || (u.User != nil && hasPassword(u)) {
			return fmt.Errorf("git URL cannot contain query, fragment or password")
		}
		if len(s.Package) > 0 || len(s.Version) > 0 || len(s.Registry) > 0 || len(s.SHA256) > 0 {
			return fmt.Errorf("git source contains unrelated fields")
		}
	case TypeHTTP:
		if err := ValidateHTTPURL(s.URL); err != nil {
			return err
		}
		if len(s.SHA256) > 0 && !ValidSHA256(s.SHA256) {
			return fmt.Errorf("sha256 must be a 64-character hexadecimal digest")
		}
		if len(s.Ref) > 0 || len(s.Package) > 0 || len(s.Version) > 0 || len(s.Registry) > 0 {
			return fmt.Errorf("http source contains unrelated fields")
		}
	case TypeNPM:
		if !packagePattern.MatchString(s.Package) || (!ExactVersion(s.Version) && !tagPattern.MatchString(s.Version)) {
			return fmt.Errorf("npm requires a package and exact version or dist-tag (ranges are unsupported)")
		}
		u, err := parseHTTPURL(s.Registry)
		if err != nil {
			return fmt.Errorf("npm registry: %w", err)
		}
		if len(u.RawQuery) > 0 {
			return fmt.Errorf("npm registry cannot contain a query")
		}
		if len(s.URL) > 0 || len(s.Ref) > 0 || len(s.SHA256) > 0 {
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
	if err != nil || (u.Scheme != schemeHTTPS && u.Scheme != schemeHTTP) || len(u.Host) == 0 || u.User != nil ||
		len(u.Fragment) > 0 {
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

func hasPassword(u *url.URL) bool {
	_, ok := u.User.Password()
	return ok
}

func ValidatePath(value string) error {
	if len(value) == 0 || value == "." || path.IsAbs(value) || path.Clean(value) != value ||
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
	for _, algorithm := range []integrityAlgorithm{
		{
			name: checksumSHA512,
			size: sha512.Size,
		},
		{
			name: checksumSHA256,
			size: sha256.Size,
		},
	} {
		for token := range strings.FieldsSeq(value) {
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
