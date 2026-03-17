/*
Copyright 2026 SAP SE.
*/

package mocks

import (
	"context"
	"sync"

	imclient "github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud/instancemapping"
)

// Compile-time interface check
var _ imclient.Client = (*MockInstanceMappingClient)(nil)

// CreateCallRecord records a Create call.
type CreateCallRecord struct {
	ServiceInstanceID string
	Request           imclient.CreateMappingRequest
}

// DeleteCallRecord records a Delete call.
type DeleteCallRecord struct {
	ServiceInstanceID string
	PrimaryID         string
	SecondaryID       string
}

// MockInstanceMappingClient is a mock implementation of imclient.Client.
type MockInstanceMappingClient struct {
	mu       sync.Mutex
	mappings map[string][]imclient.InstanceMapping

	// Call tracking
	ListCalls   []string
	CreateCalls []CreateCallRecord
	DeleteCalls []DeleteCallRecord

	// Error injection
	ListErr   error
	CreateErr error
	DeleteErr error
}

// NewMockClient creates a new MockInstanceMappingClient.
func NewMockClient() *MockInstanceMappingClient {
	return &MockInstanceMappingClient{
		mappings: make(map[string][]imclient.InstanceMapping),
	}
}

// List returns stored mappings for the service instance.
func (m *MockInstanceMappingClient) List(ctx context.Context, serviceInstanceID string) ([]imclient.InstanceMapping, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ListCalls = append(m.ListCalls, serviceInstanceID)

	if m.ListErr != nil {
		return nil, m.ListErr
	}

	return m.mappings[serviceInstanceID], nil
}

// Create stores a mapping and records the call.
func (m *MockInstanceMappingClient) Create(ctx context.Context, serviceInstanceID string, req imclient.CreateMappingRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CreateCalls = append(m.CreateCalls, CreateCallRecord{
		ServiceInstanceID: serviceInstanceID,
		Request:           req,
	})

	if m.CreateErr != nil {
		return m.CreateErr
	}

	// Store the mapping so List returns it
	m.mappings[serviceInstanceID] = append(m.mappings[serviceInstanceID], imclient.InstanceMapping(req))

	return nil
}

// Delete removes a mapping and records the call.
func (m *MockInstanceMappingClient) Delete(ctx context.Context, serviceInstanceID, primaryID, secondaryID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.DeleteCalls = append(m.DeleteCalls, DeleteCallRecord{
		ServiceInstanceID: serviceInstanceID,
		PrimaryID:         primaryID,
		SecondaryID:       secondaryID,
	})

	if m.DeleteErr != nil {
		return m.DeleteErr
	}

	// Remove matching mapping
	existing := m.mappings[serviceInstanceID]
	filtered := make([]imclient.InstanceMapping, 0, len(existing))
	for _, mapping := range existing {
		secondaryMatches := (mapping.SecondaryID == nil && secondaryID == "") ||
			(mapping.SecondaryID != nil && *mapping.SecondaryID == secondaryID)
		if mapping.PrimaryID != primaryID || !secondaryMatches {
			filtered = append(filtered, mapping)
		}
	}
	m.mappings[serviceInstanceID] = filtered

	return nil
}

// Reset clears all recorded calls and stored mappings.
func (m *MockInstanceMappingClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.mappings = make(map[string][]imclient.InstanceMapping)
	m.ListCalls = nil
	m.CreateCalls = nil
	m.DeleteCalls = nil
	m.ListErr = nil
	m.CreateErr = nil
	m.DeleteErr = nil
}

// AddMapping adds a mapping to the mock (for setting up test state).
func (m *MockInstanceMappingClient) AddMapping(serviceInstanceID string, mapping imclient.InstanceMapping) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.mappings[serviceInstanceID] = append(m.mappings[serviceInstanceID], mapping)
}
