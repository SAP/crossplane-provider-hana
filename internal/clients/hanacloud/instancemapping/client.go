package instancemapping

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
)

// InstanceMapping represents a mapping between a HANA instance and a Kubernetes namespace
type InstanceMapping struct {
	Platform    string `json:"platform"`
	PrimaryID   string `json:"primaryID"`
	SecondaryID *string `json:"secondaryID,omitempty"`
	IsDefault   bool   `json:"isDefault"`
}

// listMappingsResponse wraps the API response for listing instance mappings
type listMappingsResponse struct {
	Mappings []InstanceMapping `json:"mappings"`
}

// CreateMappingRequest is the request body for creating a mapping
type CreateMappingRequest struct {
	Platform    string  `json:"platform"`
	PrimaryID   string  `json:"primaryID"`
	SecondaryID *string `json:"secondaryID"`
	IsDefault   bool    `json:"isDefault"`
}

// Client is the interface for instance mapping operations
type Client interface {
	List(ctx context.Context, serviceInstanceID string) ([]InstanceMapping, error)
	Create(ctx context.Context, serviceInstanceID string, req CreateMappingRequest) error
	Delete(ctx context.Context, serviceInstanceID, primaryID, secondaryID string) error
}

type instanceMappingClient struct {
	baseURL    string
	httpClient *http.Client
	logger     logging.Logger
}

// NewClient creates a new instance mapping client
func NewClient(baseURL string, httpClient *http.Client, logger logging.Logger) Client {
	return &instanceMappingClient{
		baseURL:    baseURL,
		httpClient: httpClient,
		logger:     logger,
	}
}

// List retrieves all instance mappings for a service instance
func (c *instanceMappingClient) List(ctx context.Context, serviceInstanceID string) ([]InstanceMapping, error) {
	apiURL := fmt.Sprintf("https://%s/inventory/v2/serviceInstances/%s/instanceMappings",
		c.baseURL, serviceInstanceID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req) //nolint:gosec // G704: URL is constructed from validated service instance ID
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound {
		// Service instance not found or no mappings - return empty list
		c.logger.Debug("No mappings found for service instance", "serviceInstanceID", serviceInstanceID)
		return []InstanceMapping{}, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Unmarshal into wrapper struct (API returns {"mappings": [...]})
	var response listMappingsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return response.Mappings, nil
}

// Create creates a new instance mapping
func (c *instanceMappingClient) Create(ctx context.Context, serviceInstanceID string, req CreateMappingRequest) error {
	apiURL := fmt.Sprintf("https://%s/inventory/v2/serviceInstances/%s/instanceMappings",
		c.baseURL, serviceInstanceID)

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq) //nolint:gosec // G704: URL is constructed from validated service instance ID
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	c.logger.Debug("Successfully created instance mapping",
		"serviceInstanceID", serviceInstanceID,
		"primaryID", req.PrimaryID,
		"secondaryID", req.SecondaryID)

	return nil
}

// Delete removes an instance mapping
func (c *instanceMappingClient) Delete(ctx context.Context, serviceInstanceID, primaryID, secondaryID string) error {
	// Build URL with query parameters
	apiURL := fmt.Sprintf("https://%s/inventory/v2/serviceInstances/%s/instanceMappings",
		c.baseURL, serviceInstanceID)

	// Add query parameters
	params := url.Values{}
	params.Set("primaryID", primaryID)
	params.Set("secondaryID", secondaryID)
	apiURL = apiURL + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req) //nolint:gosec // G704: URL is constructed from validated service instance ID
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound {
		// Mapping already deleted - consider this success
		c.logger.Debug("Mapping not found (already deleted)",
			"serviceInstanceID", serviceInstanceID,
			"primaryID", primaryID,
			"secondaryID", secondaryID)
		return nil
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	c.logger.Debug("Successfully deleted instance mapping",
		"serviceInstanceID", serviceInstanceID,
		"primaryID", primaryID,
		"secondaryID", secondaryID)

	return nil
}
