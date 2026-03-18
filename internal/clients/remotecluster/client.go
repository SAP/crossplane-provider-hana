/*
Copyright 2026 SAP SE.
*/

package remotecluster

import (
	"context"
	"fmt"

	servicescloudsapv1 "github.com/SAP/sap-btp-service-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CreateRemoteClient creates a Kubernetes client for a remote cluster from kubeconfig data.
// The client is configured with a runtime scheme that includes:
// - Core v1 types (ConfigMap, Secret, etc.)
// - BTP Service Operator v1 types (ServiceInstance, ServiceBinding)
//
// This follows the pattern used in co-metrics-operator for cross-cluster access.
func CreateRemoteClient(ctx context.Context, kubeconfigData []byte) (client.Client, error) {
	// Create REST config from kubeconfig
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config from kubeconfig: %w", err)
	}

	// Create scheme with all needed API types
	scheme := runtime.NewScheme()

	// Add core v1 types (ConfigMap, Secret, etc.)
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add core v1 to scheme: %w", err)
	}

	// Add BTP Service Operator v1 types (ServiceInstance, ServiceBinding)
	if err := servicescloudsapv1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add BTP service operator v1 to scheme: %w", err)
	}

	// Create client
	remoteClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create remote client: %w", err)
	}

	return remoteClient, nil
}
