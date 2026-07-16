//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/crossplane-contrib/xp-testing/pkg/envvar"
	"github.com/crossplane-contrib/xp-testing/pkg/xpenvfuncs"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/support/kind"

	servicescloudsapv1 "github.com/SAP/sap-btp-service-operator/api/v1"

	adminv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	inventoryv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/inventory/v1alpha1"
	schemav1alpha1 "github.com/SAP/crossplane-provider-hana/apis/schema/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
)

const (
	// UXP installs into upbound-system, not crossplane-system.
	crossplaneNamespace = "upbound-system"
	providerSecretName  = "secret"
)

var testenv env.Environment

func TestMain(m *testing.M) {
	testenv = env.NewParallel()

	secretData := getProviderConfigSecretData()
	clusterName := envvar.GetOrDefault("E2E_CLUSTER_NAME", "local-dev")
	reuseCluster := envvar.CheckEnvVarExists("E2E_REUSE_CLUSTER")

	testenv.Setup(
		envfuncs.CreateCluster(&kind.Cluster{}, clusterName),
		installBTPOperatorCRDs(clusterName),
		xpenvfuncs.Conditional(
			func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
				r, err := cfg.NewClient()
				if err != nil {
					return ctx, err
				}
				secret := xpenvfuncs.SimpleSecret(providerSecretName, crossplaneNamespace, secretData)
				if err := r.Resources().Create(ctx, secret); err != nil {
					return ctx, fmt.Errorf("create provider secret: %w", err)
				}
				return ctx, nil
			},
			!reuseCluster,
		),
		xpenvfuncs.ApplyProviderConfigFromDir("./provider"),
		xpenvfuncs.LoadSchemas(
			func(s *runtime.Scheme) error { return apisv1alpha1.AddToScheme(s) },
			func(s *runtime.Scheme) error { return adminv1alpha1.AddToScheme(s) },
			func(s *runtime.Scheme) error { return schemav1alpha1.AddToScheme(s) },
			func(s *runtime.Scheme) error { return inventoryv1alpha1.AddToScheme(s) },
			func(s *runtime.Scheme) error { return servicescloudsapv1.AddToScheme(s) },
		),
		xpenvfuncs.AwaitCRDsEstablished,
	)

	testenv.Finish(
		xpenvfuncs.DumpLogs(clusterName, "post-tests"),
		xpenvfuncs.Conditional(envfuncs.DestroyCluster(clusterName), !reuseCluster),
	)

	os.Exit(testenv.Run(m))
}

func getProviderConfigSecretData() map[string]string {
	bindings := envvar.GetOrPanic("HANA_BINDINGS")

	var secretData map[string]string
	err := json.Unmarshal([]byte(bindings), &secretData)
	if err != nil {
		panic(fmt.Sprintf("Failed to unmarshal HANA_BINDINGS: %v", err))
	}

	return secretData
}

// installBTPOperatorCRDs returns a ClusterAwareFunc that installs the SAP BTP Service Operator CRDs.
func installBTPOperatorCRDs(clusterName string) env.Func {
	return func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
		// Use go list to find the module directory (works across different environments)
		cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/SAP/sap-btp-service-operator")
		output, err := cmd.Output()
		if err != nil {
			return ctx, fmt.Errorf("failed to find sap-btp-service-operator module: %w", err)
		}

		moduleDir := filepath.Clean(string(output[:len(output)-1])) // trim newline
		crdDir := filepath.Join(moduleDir, "config/crd/bases")

		// Apply the CRDs using kubectl
		kubeconfigPath := cfg.KubeconfigFile()

		files, err := filepath.Glob(filepath.Join(crdDir, "*.yaml"))
		if err != nil {
			return ctx, fmt.Errorf("failed to find BTP operator CRDs: %w", err)
		}

		for _, f := range files {
			cmd := exec.CommandContext(ctx, "kubectl", "apply", "-f", f, "--kubeconfig", kubeconfigPath)
			output, err := cmd.CombinedOutput()
			if err != nil {
				return ctx, fmt.Errorf("failed to apply CRD %s: %w\nOutput: %s", f, err, string(output))
			}
		}

		return ctx, nil
	}
}
