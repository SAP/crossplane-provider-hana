/*
Copyright 2026 SAP SE.
*/

package instancemapping

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/google/go-cmp/cmp"
)

// MockLogger is a mock implementation of logging.Logger
type MockLogger struct{}

func (l *MockLogger) Debug(_ string, _ ...interface{}) {}
func (l *MockLogger) Info(_ string, _ ...interface{})  {}
func (l *MockLogger) WithValues(_ ...interface{}) logging.Logger {
	return l
}

func TestList(t *testing.T) {
	ctx := context.Background()

	secondaryID := "test-namespace"

	cases := map[string]struct {
		handler http.HandlerFunc
		want    []InstanceMapping
		wantErr bool
	}{
		"Success200WithMappings": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("expected GET, got %s", r.Method)
				}
				if !strings.Contains(r.URL.Path, "/inventory/v2/serviceInstances/test-instance-id/instanceMappings") {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				w.WriteHeader(http.StatusOK)
				if err := json.NewEncoder(w).Encode(listMappingsResponse{
					Mappings: []InstanceMapping{
						{
							Platform:    "kubernetes",
							PrimaryID:   "cluster-1",
							SecondaryID: &secondaryID,
							IsDefault:   true,
						},
					},
				}); err != nil {
					t.Errorf("failed to encode response: %v", err)
				}
			},
			want: []InstanceMapping{
				{
					Platform:    "kubernetes",
					PrimaryID:   "cluster-1",
					SecondaryID: &secondaryID,
					IsDefault:   true,
				},
			},
			wantErr: false,
		},
		"Success200EmptyArray": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				if err := json.NewEncoder(w).Encode(listMappingsResponse{
					Mappings: []InstanceMapping{},
				}); err != nil {
					t.Errorf("failed to encode response: %v", err)
				}
			},
			want:    []InstanceMapping{},
			wantErr: false,
		},
		"NotFound404ReturnsEmpty": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			want:    []InstanceMapping{},
			wantErr: false,
		},
		"Unauthorized401": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
			},
			want:    nil,
			wantErr: true,
		},
		"ServerError500": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error": "internal server error"}`))
			},
			want:    nil,
			wantErr: true,
		},
		"InvalidJSONResponse": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`invalid json`))
			},
			want:    nil,
			wantErr: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(tc.handler)
			defer server.Close()

			// Extract host from server URL (strip https://)
			baseURL := strings.TrimPrefix(server.URL, "https://")
			client := NewClient(baseURL, server.Client(), &MockLogger{})

			got, err := client.List(ctx, "test-instance-id")

			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("List() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	secondaryID := "test-namespace"

	cases := map[string]struct {
		handler http.HandlerFunc
		req     CreateMappingRequest
		wantErr bool
	}{
		"Success201Created": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("expected POST, got %s", r.Method)
				}
				w.WriteHeader(http.StatusCreated)
			},
			req: CreateMappingRequest{
				Platform:    "kubernetes",
				PrimaryID:   "cluster-1",
				SecondaryID: &secondaryID,
				IsDefault:   true,
			},
			wantErr: false,
		},
		"Success200OK": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			req: CreateMappingRequest{
				Platform:  "kubernetes",
				PrimaryID: "cluster-1",
			},
			wantErr: false,
		},
		"BadRequest400": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error": "invalid request"}`))
			},
			req: CreateMappingRequest{
				Platform:  "kubernetes",
				PrimaryID: "cluster-1",
			},
			wantErr: true,
		},
		"Conflict409": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error": "mapping already exists"}`))
			},
			req: CreateMappingRequest{
				Platform:  "kubernetes",
				PrimaryID: "cluster-1",
			},
			wantErr: true,
		},
		"ServerError500": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error": "internal server error"}`))
			},
			req: CreateMappingRequest{
				Platform:  "kubernetes",
				PrimaryID: "cluster-1",
			},
			wantErr: true,
		},
		"VerifyRequestBody": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("expected Content-Type: application/json, got %s", r.Header.Get("Content-Type"))
				}

				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("failed to read request body: %v", err)
				}

				var req CreateMappingRequest
				if err := json.Unmarshal(body, &req); err != nil {
					t.Errorf("failed to unmarshal request body: %v", err)
				}

				if req.Platform != "kubernetes" {
					t.Errorf("expected platform 'kubernetes', got %s", req.Platform)
				}
				if req.PrimaryID != "cluster-1" {
					t.Errorf("expected primaryID 'cluster-1', got %s", req.PrimaryID)
				}
				if req.SecondaryID == nil || *req.SecondaryID != "test-namespace" {
					t.Errorf("expected secondaryID 'test-namespace', got %v", req.SecondaryID)
				}
				if !req.IsDefault {
					t.Error("expected isDefault to be true")
				}

				w.WriteHeader(http.StatusCreated)
			},
			req: CreateMappingRequest{
				Platform:    "kubernetes",
				PrimaryID:   "cluster-1",
				SecondaryID: &secondaryID,
				IsDefault:   true,
			},
			wantErr: false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(tc.handler)
			defer server.Close()

			baseURL := strings.TrimPrefix(server.URL, "https://")
			client := NewClient(baseURL, server.Client(), &MockLogger{})

			err := client.Create(ctx, "test-instance-id", tc.req)

			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	cases := map[string]struct {
		handler     http.HandlerFunc
		primaryID   string
		secondaryID string
		wantErr     bool
	}{
		"Success204NoContent": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete {
					t.Errorf("expected DELETE, got %s", r.Method)
				}
				w.WriteHeader(http.StatusNoContent)
			},
			primaryID:   "cluster-1",
			secondaryID: "test-namespace",
			wantErr:     false,
		},
		"Success200OK": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			primaryID:   "cluster-1",
			secondaryID: "test-namespace",
			wantErr:     false,
		},
		"NotFound404IsSuccess": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			primaryID:   "cluster-1",
			secondaryID: "test-namespace",
			wantErr:     false,
		},
		"BadRequest400": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error": "invalid request"}`))
			},
			primaryID:   "cluster-1",
			secondaryID: "test-namespace",
			wantErr:     true,
		},
		"ServerError500": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error": "internal server error"}`))
			},
			primaryID:   "cluster-1",
			secondaryID: "test-namespace",
			wantErr:     true,
		},
		"VerifyQueryParams": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				if query.Get("primaryID") != "cluster-1" {
					t.Errorf("expected primaryID=cluster-1, got %s", query.Get("primaryID"))
				}
				if query.Get("secondaryID") != "test-namespace" {
					t.Errorf("expected secondaryID=test-namespace, got %s", query.Get("secondaryID"))
				}
				w.WriteHeader(http.StatusNoContent)
			},
			primaryID:   "cluster-1",
			secondaryID: "test-namespace",
			wantErr:     false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(tc.handler)
			defer server.Close()

			baseURL := strings.TrimPrefix(server.URL, "https://")
			client := NewClient(baseURL, server.Client(), &MockLogger{})

			err := client.Delete(ctx, "test-instance-id", tc.primaryID, tc.secondaryID)

			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
