//go:build generate

/*
Copyright 2025 SAP SE
*/

// Remove existing CRDs
//go:generate rm -rf ../package/crds

// Generate deepcopy methodsets and CRD manifests
//go:generate go run -tags generate sigs.k8s.io/controller-tools/cmd/controller-gen object:headerFile=../hack/boilerplate.go.txt paths=./... crd:crdVersions=v1 output:artifacts:config=../package/crds

// Generate crossplane-runtime methodsets (resource.Claim, etc)
//go:generate go run -tags generate github.com/crossplane/crossplane-tools/cmd/angryjet generate-methodsets --header-file=../hack/boilerplate.go.txt ./...

package apis
