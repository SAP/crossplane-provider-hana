/*
Copyright 2026 SAP SE.
*/

package instancemapping

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/SAP/crossplane-provider-hana/apis/inventory/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud"
	imclient "github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud/instancemapping"
)

const (
	testBaseURLKey     = "baseurl"
	testUAAKey         = "uaa"
	testCredentialsKey = "credentials"
	testLegacyBTPKey   = "attribute.credentials"
)

// An existing InstanceMapping must keep working when the BTP provider replaces
// attribute.credentials with separate entries, including subsequent rotations.
func TestConnectorConnectSecretUpgrade(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "admin-credentials", Namespace: testNamespace},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	var got hanacloud.AdminAPICredentials
	connector := NewConnector(kube, logging.NewNopLogger(), func(ctx context.Context, creds hanacloud.AdminAPICredentials, log logging.Logger) (imclient.Client, error) {
		got = creds
		return DefaultClientFactory(ctx, creds, log)
	})
	cr := &v1alpha1.InstanceMapping{
		Spec: v1alpha1.InstanceMappingSpec{
			ForProvider: v1alpha1.InstanceMappingParameters{
				AdminCredentialsSecretRef: v1alpha1.AdminCredentialsSecretRef{
					Name: secret.Name, Namespace: secret.Namespace, Key: testLegacyBTPKey,
				},
			},
		},
	}
	want := hanacloud.AdminAPICredentials{
		BaseURL: "https://api.example.com",
		UAA: hanacloud.UAAConfig{
			URL: "https://auth.example.com", ClientID: "test-client", ClientSecret: "original-secret",
		},
	}
	for _, phase := range []string{"BeforeUpgrade", "AfterUpgrade", "AfterRotation"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "AfterRotation" {
				want.UAA.ClientSecret = "rotated-secret"
			}
			if phase == "BeforeUpgrade" {
				data, err := json.Marshal(want)
				if err != nil {
					t.Fatal(err)
				}
				secret.Data = map[string][]byte{testLegacyBTPKey: data}
			} else {
				uaa, err := json.Marshal(want.UAA)
				if err != nil {
					t.Fatal(err)
				}
				secret.Data = map[string][]byte{testBaseURLKey: []byte(want.BaseURL), testUAAKey: uaa}
			}
			if err := kube.Update(context.Background(), secret); err != nil {
				t.Fatal(err)
			}
			if _, err := connector.Connect(context.Background(), cr); err != nil {
				t.Fatalf("Connect() failed: %v", err)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Credentials mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestConnectorConnectCredentials(t *testing.T) {
	const (
		baseURL         = "https://api.example.com"
		uaaJSON         = `{"url":"https://auth.example.com","clientid":"test-client","clientsecret":"test-secret\"with\\escapes"}`
		credentialsJSON = `{"baseurl":"https://api.example.com","uaa":` + uaaJSON + `}`
	)
	want := hanacloud.AdminAPICredentials{
		BaseURL: baseURL,
		UAA: hanacloud.UAAConfig{
			URL:          "https://auth.example.com",
			ClientID:     "test-client",
			ClientSecret: `test-secret"with\escapes`,
		},
	}
	flattened := map[string][]byte{
		testBaseURLKey: []byte(baseURL),
		testUAAKey:     []byte(uaaJSON),
		"other":        []byte("ignored binding metadata"),
	}
	tests := map[string]struct {
		key     string
		data    map[string][]byte
		wantErr string
	}{
		"FlattenedWithoutKey": {data: flattened},
		"FlattenedWithLegacyBTPKey": {
			key:  testLegacyBTPKey,
			data: flattened,
		},
		"FlattenedWithMissingCustomKey": {
			key:  testCredentialsKey,
			data: flattened,
		},
		"LegacyBTPJSON": {
			key:  testLegacyBTPKey,
			data: map[string][]byte{testLegacyBTPKey: []byte(credentialsJSON)},
		},
		"KymaJSON": {
			key:  testCredentialsKey,
			data: map[string][]byte{testCredentialsKey: []byte(credentialsJSON)},
		},
		"CustomJSONKey": {
			key:  "custom",
			data: map[string][]byte{"custom": []byte(credentialsJSON)},
		},
		"SelectedJSONTakesPrecedence": {
			key: testCredentialsKey,
			data: map[string][]byte{
				testCredentialsKey: []byte(credentialsJSON),
				testBaseURLKey:     []byte("https://other.example.com"),
				testUAAKey:         []byte(`{"clientid":"other-client"}`),
			},
		},
		"MalformedSelectedJSONDoesNotFallBack": {
			key: testCredentialsKey,
			data: map[string][]byte{
				testCredentialsKey: []byte(`{`),
				testBaseURLKey:     []byte(baseURL),
				testUAAKey:         []byte(uaaJSON),
			},
			wantErr: "cannot parse admin API credentials: failed to parse admin API credentials",
		},
		"MissingBaseURL": {
			data:    map[string][]byte{testUAAKey: []byte(uaaJSON)},
			wantErr: `credentials key "baseurl" not found in secret`,
		},
		"MissingUAA": {
			key:     testLegacyBTPKey,
			data:    map[string][]byte{testBaseURLKey: []byte(baseURL)},
			wantErr: `credentials key "uaa" not found in secret`,
		},
		"EmptySecret": {wantErr: `credentials key "baseurl" not found in secret`},
		"MalformedUAA": {
			data:    map[string][]byte{testBaseURLKey: []byte(baseURL), testUAAKey: []byte(`{`)},
			wantErr: "cannot parse admin API credentials: cannot parse uaa credentials",
		},
		"WrongUAAType": {
			data:    map[string][]byte{testBaseURLKey: []byte(baseURL), testUAAKey: []byte(`"invalid"`)},
			wantErr: "cannot parse admin API credentials: cannot parse uaa credentials",
		},
		"IncompleteUAA": {
			data:    map[string][]byte{testBaseURLKey: []byte(baseURL), testUAAKey: []byte(`{}`)},
			wantErr: "cannot connect to HANA Cloud API: uaa.url is required",
		},
		"EmptyBaseURL": {
			data:    map[string][]byte{testBaseURLKey: {}, testUAAKey: []byte(uaaJSON)},
			wantErr: "cannot connect to HANA Cloud API: baseurl is required",
		},
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "admin-credentials", Namespace: testNamespace},
				Data:       tt.data,
			}
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
			var got hanacloud.AdminAPICredentials
			factory := func(ctx context.Context, creds hanacloud.AdminAPICredentials, log logging.Logger) (imclient.Client, error) {
				got = creds
				return DefaultClientFactory(ctx, creds, log)
			}
			connector := NewConnector(kube, logging.NewNopLogger(), factory)
			cr := &v1alpha1.InstanceMapping{
				Spec: v1alpha1.InstanceMappingSpec{
					ForProvider: v1alpha1.InstanceMappingParameters{
						AdminCredentialsSecretRef: v1alpha1.AdminCredentialsSecretRef{
							Name: secret.Name, Namespace: secret.Namespace, Key: tt.key,
						},
					},
				},
			}
			external, err := connector.Connect(context.Background(), cr)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Connect() error = %v, want containing %q", err, tt.wantErr)
				}
				if external != nil {
					t.Fatal("Connect() returned a client on error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Connect() failed: %v", err)
			}
			if external == nil {
				t.Fatal("Connect() returned no client")
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Credentials mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
