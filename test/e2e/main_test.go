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
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/support/kind"

	servicescloudsapv1 "github.com/SAP/sap-btp-service-operator/api/v1"

	adminv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	inventoryv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/inventory/v1alpha1"
	schemav1alpha1 "github.com/SAP/crossplane-provider-hana/apis/schema/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"

	"github.com/crossplane-contrib/xp-testing/pkg/vendored"

	"sigs.k8s.io/e2e-framework/pkg/env"

	"github.com/crossplane-contrib/xp-testing/pkg/images"
	"github.com/crossplane-contrib/xp-testing/pkg/logging"
	"github.com/crossplane-contrib/xp-testing/pkg/setup"
)

var testenv env.Environment

var (
	UUT_CONFIG_KEY     = "crossplane/provider-hana"
	UUT_CONTROLLER_KEY = "crossplane/provider-hana-controller"
)

func TestMain(m *testing.M) {
	var verbosity = 4
	logging.EnableVerboseLogging(&verbosity)
	testenv = env.NewParallel()

	imgs := images.GetImagesFromEnvironmentOrPanic(UUT_CONFIG_KEY, &UUT_CONTROLLER_KEY)

	secretData := getProviderConfigSecretData()

	clusterSetup := setup.ClusterSetup{
		ProviderName:       "hana-provider",
		Images:             imgs,
		ProviderCredential: &setup.ProviderCredentials{SecretData: secretData},
		CrossplaneSetup: setup.CrossplaneSetup{
			Version:  "1.14.3",
			Registry: setup.DockerRegistry,
		},

		ControllerConfig: &vendored.ControllerConfig{
			Spec: vendored.ControllerConfigSpec{
				Image: imgs.ControllerImage,
				Args: []string{
					"--sync=10s",
					"--debug",
				},
			},
		},
		AddToSchemaFuncs: []func(s *runtime.Scheme) error{
			apisv1alpha1.AddToScheme,
			adminv1alpha1.AddToScheme,
			schemav1alpha1.AddToScheme,
			inventoryv1alpha1.AddToScheme,
			servicescloudsapv1.AddToScheme,
		},
	}

	// Install BTP operator CRDs for KymaInstanceMapping tests
	clusterSetup.PostCreate(installBTPOperatorCRDs)

	_ = clusterSetup.Configure(testenv, &kind.Cluster{})

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
