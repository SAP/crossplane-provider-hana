//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/crossplane-contrib/xp-testing/pkg/envvar"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/e2e-framework/support/kind"

	adminv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
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
		},
	}

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
