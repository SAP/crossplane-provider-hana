package hanacloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud/instancemapping"
)

// AdminAPICredentials contains the credentials for HANA Cloud Admin API
type AdminAPICredentials struct {
	BaseURL string    `json:"baseurl"`
	UAA     UAAConfig `json:"uaa"`
}

// UAAConfig contains UAA/OAuth2 configuration
type UAAConfig struct {
	URL          string `json:"url"`
	ClientID     string `json:"clientid"`
	ClientSecret string `json:"clientsecret"`
}

// Client is the interface for HANA Cloud REST API operations
type Client interface {
	Connect(ctx context.Context, creds AdminAPICredentials) error
	InstanceMapping() instancemapping.Client
	Disconnect() error
}

type hanaCloudClient struct {
	baseURL    string
	httpClient *http.Client
	imClient   instancemapping.Client
	logger     logging.Logger
	mu         sync.RWMutex
}

// New returns a new HANA Cloud API client with the provided logger
func New(logger logging.Logger) Client {
	return &hanaCloudClient{
		logger: logger,
	}
}

// Connect establishes a connection to the HANA Cloud Admin API using OAuth2
func (c *hanaCloudClient) Connect(ctx context.Context, creds AdminAPICredentials) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Validate credentials
	if creds.BaseURL == "" {
		return fmt.Errorf("baseurl is required")
	}
	if creds.UAA.URL == "" {
		return fmt.Errorf("uaa.url is required")
	}
	if creds.UAA.ClientID == "" {
		return fmt.Errorf("uaa.clientid is required")
	}
	if creds.UAA.ClientSecret == "" {
		return fmt.Errorf("uaa.clientsecret is required")
	}

	// Configure OAuth2 client credentials
	oauth2Config := clientcredentials.Config{
		ClientID:     creds.UAA.ClientID,
		ClientSecret: creds.UAA.ClientSecret,
		TokenURL:     creds.UAA.URL + "/oauth/token",
	}

	// Create HTTP client with OAuth2 token source
	c.httpClient = oauth2Config.Client(ctx)
	c.baseURL = creds.BaseURL

	// Initialize instance mapping client
	c.imClient = instancemapping.NewClient(c.baseURL, c.httpClient, c.logger)

	return nil
}

// InstanceMapping returns the instance mapping client
func (c *hanaCloudClient) InstanceMapping() instancemapping.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.imClient
}

// Disconnect closes the connection (currently a no-op as HTTP client handles cleanup)
func (c *hanaCloudClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.httpClient = nil
	c.imClient = nil
	c.baseURL = ""

	return nil
}

// ParseAdminAPICredentials parses admin API credentials from JSON
func ParseAdminAPICredentials(data []byte) (AdminAPICredentials, error) {
	var creds AdminAPICredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return AdminAPICredentials{}, fmt.Errorf("failed to parse admin API credentials: %w", err)
	}
	return creds, nil
}
