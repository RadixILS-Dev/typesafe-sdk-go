package typesafe

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://api.typesafe.ai"
	DefaultModel   = "jev-latest"
	Version        = "0.1.0"
)

type clientConfig struct {
	APIKey      string
	BaseURL     string
	Model       string
	MaxAttempts int
	HTTPClient  *http.Client
}

// NewClient creates a client using environment variables and SDK defaults.
// Options override those settings in order; the last option for a setting wins.
// It does not mutate or close a supplied HTTP client, or make network requests.
func NewClient(options ...ClientOption) (*Client, error) {
	config := clientConfig{
		APIKey:      environmentSetting("TYPESAFE_API_KEY", ""),
		BaseURL:     environmentSetting("TYPESAFE_BASE_URL", DefaultBaseURL),
		Model:       environmentSetting("TYPESAFE_DEFAULT_MODEL", DefaultModel),
		MaxAttempts: 3,
		HTTPClient: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("typesafe: client option must not be nil")
		}
		option(&config)
	}
	if config.APIKey == "" {
		return nil, fmt.Errorf("typesafe: use WithAPIKey or set TYPESAFE_API_KEY")
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	u, err := url.Parse(config.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("typesafe: base URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if config.MaxAttempts < 1 {
		return nil, fmt.Errorf("typesafe: max attempts must be at least one")
	}
	if config.HTTPClient == nil {
		return nil, fmt.Errorf("typesafe: HTTP client must not be nil")
	}
	return &Client{config: config}, nil
}

func environmentSetting(variable, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(variable)); value != "" {
		return value
	}
	return fallback
}
