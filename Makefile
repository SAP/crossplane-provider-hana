# ====================================================================================
# Setup Project
BASE_NAME := hana


PROJECT_NAME := crossplane-provider-$(BASE_NAME)
PROJECT_REPO := github.com/SAP/$(PROJECT_NAME)

TARGETARCH ?= amd64
PLATFORMS ?= linux_amd64

VERSION := $(shell echo "v$$(cat VERSION)-$$(git rev-list HEAD --count)-g$$(git describe --dirty --always)" | sed 's/-/./2' | sed 's/-/./2' | sed 's/-/./2')

GOLANGCILINT_VERSION ?= 2.8.0

-include build/makelib/common.mk

# Setup Output
-include build/makelib/output.mk

# Setup Go
NPROCS ?= 1
GO_TEST_PARALLEL := $(shell echo $$(( $(NPROCS) / 2 )))
GO_STATIC_PACKAGES = $(GO_PROJECT)/cmd/provider
GO_LDFLAGS += -X $(GO_PROJECT)/internal/version.Version=$(VERSION)
GO_SUBDIRS += cmd internal apis
GO111MODULE = on
-include build/makelib/golang.mk

# kind-related versions
KIND_VERSION ?= v0.30.0
KIND_NODE_IMAGE_TAG ?= v1.34.0

# Setup Kubernetes tools
-include build/makelib/k8s_tools.mk

# Setup Images
DOCKER_REGISTRY ?= crossplane
IMAGES = $(BASE_NAME) $(BASE_NAME)-controller
-include build/makelib/image.mk

export UUT_CONFIG = $(BUILD_REGISTRY)/$(subst crossplane-,crossplane/,$(PROJECT_NAME)):$(VERSION)
export UUT_CONTROLLER = $(BUILD_REGISTRY)/$(subst crossplane-,crossplane/,$(PROJECT_NAME))-controller:$(VERSION)
export E2E_IMAGES = {"crossplane/provider-hana":"$(UUT_CONFIG)","crossplane/provider-hana-controller":"$(UUT_CONTROLLER)"}

fallthrough: submodules
	@echo Initial setup complete. Running make again . . .
	@make

# run unit tests
test.run: $(GOJUNIT) $(GOCOVER_COBERTURA) go.test.unit

# e2e tests
e2e.run: test-e2e

test-e2e: $(KIND) $(HELM3) build
	@$(INFO) running e2e tests
	@echo E2E_IMAGES=$$E2E_IMAGES
	# echo E2E_IMAGES=$$E2E_IMAGES > e2e.env
	source test/e2e/secrets/secrets.env; echo HANA_BINDINGS=$$HANA_BINDINGS && HANA_BINDINGS=$$HANA_BINDINGS go test $(PROJECT_REPO)/test/... -tags=e2e -test.v  -count=1
	@$(OK) e2e tests passed

# Update the submodules, such as the common build scripts.
submodules:
	@git submodule sync
	@git submodule update --init --recursive

# NOTE(hasheddan): the build submodule currently overrides XDG_CACHE_HOME in
# order to force the Helm 3 to use the .work/helm directory. This causes Go on
# Linux machines to use that directory as the build cache as well. We should
# adjust this behavior in the build submodule because it is also causing Linux
# users to duplicate their build cache, but for now we just make it easier to
# identify its location in CI so that we cache between builds.
go.cachedir:
	@go env GOCACHE

# This is for running out-of-cluster locally, and is for convenience. Running
# this make target will print out the command which was used. For more control,
# try running the binary directly with different arguments.
run: go.build
	@$(INFO) Running Crossplane locally out-of-cluster . . .
	@# To see other arguments that can be provided, run the command with --help instead
	$(GO_OUT_DIR)/provider --debug

# This is for running out-of-cluster locally for development.
# It installs Crossplane CRDs, Provider CRDs, ProviderConfig, creates Crossplane namespace.
# You need to start the controller manually, ideally in an IDE for debugging.
dev-debug: $(KIND) $(KUBECTL)
	@$(INFO) Creating kind cluster
	@$(KIND) create cluster --name=$(PROJECT_NAME)-dev
	@$(KUBECTL) cluster-info --context kind-$(PROJECT_NAME)-dev
	@$(INFO) Installing Crossplane CRDs
	# @$(KUBECTL) apply -k https://github.com/crossplane/crossplane//cluster/crds?ref=main
	@$(INFO) Installing Provider hana CRDs
	@$(KUBECTL) apply -R -f package/crds
	@$(INFO) Creating crossplane-system namespace
	@$(KUBECTL) create ns crossplane-system
	@$(INFO) Creating provider config and secret
	@$(KUBECTL) apply -R -f examples/provider

# This is for running out-of-cluster locally for development.
# It installs Crossplane CRDs, Provider CRDs AND STARTS the controller.
# You need to apply ProviderConfig (along with secret) and the resources manually.
dev: $(KIND) $(KUBECTL)
	@$(INFO) Creating kind cluster
	@$(KIND) create cluster --name=$(PROJECT_NAME)-dev
	@$(KUBECTL) cluster-info --context kind-$(PROJECT_NAME)-dev
	@$(INFO) Installing Crossplane CRDs
	# @$(KUBECTL) apply -k https://github.com/crossplane/crossplane//cluster?ref=main
	@$(INFO) Installing Provider hana CRDs
	@$(KUBECTL) apply -R -f package/crds
	@$(INFO) Starting Provider hana controllers
	@$(GO) run cmd/provider/main.go --debug

dev-clean: $(KIND) $(KUBECTL)
	@$(INFO) Deleting kind cluster
	@$(KIND) delete cluster --name=$(PROJECT_NAME)-dev

.PHONY: submodules fallthrough test-integration run dev dev-clean test-e2e test.run

# ====================================================================================
# Special Targets

# Define tool versions (only if not already defined in golang.mk)
GOJUNIT_VERSION ?= v2.0.0
GOCOVER_COBERTURA_VERSION ?= aaee18c8195c3f2d90e5ef80ca918d265463842a

# Install gomplate
GOMPLATE_VERSION := 3.10.0
GOMPLATE := $(TOOLS_HOST_DIR)/gomplate-$(GOMPLATE_VERSION)

# Define custom tool installation targets that will only be used if the files don't exist
install-go-junit-report:
	@$(INFO) installing go-junit-report
	@mkdir -p $(TOOLS_HOST_DIR)
	@GOBIN=$(TOOLS_HOST_DIR) go install github.com/jstemmer/go-junit-report/v2@$(GOJUNIT_VERSION) || $(FAIL)
	@$(OK) installing go-junit-report

install-gocover-cobertura:
	@$(INFO) installing gocover-cobertura
	@mkdir -p $(TOOLS_HOST_DIR)
	@GOBIN=$(TOOLS_HOST_DIR) go install github.com/t-yuki/gocover-cobertura@$(GOCOVER_COBERTURA_VERSION) || $(FAIL)
	@$(OK) installing gocover-cobertura

# Check if tools exist and create dependencies if they don't
ifeq ($(wildcard $(GOJUNIT)),)
$(GOJUNIT): install-go-junit-report
endif

ifeq ($(wildcard $(GOCOVER_COBERTURA)),)
$(GOCOVER_COBERTURA): install-gocover-cobertura
endif

$(GOMPLATE):
	@$(INFO) installing gomplate $(SAFEHOSTPLATFORM)
	@mkdir -p $(TOOLS_HOST_DIR)
	@curl -fsSLo $(GOMPLATE) https://github.com/hairyhenderson/gomplate/releases/download/v$(GOMPLATE_VERSION)/gomplate_$(SAFEHOSTPLATFORM) || $(FAIL)
	@chmod +x $(GOMPLATE)
	@$(OK) installing gomplate $(SAFEHOSTPLATFORM)

export GOMPLATE
export GOJUNIT
export GOCOVER_COBERTURA

# This target prepares repo for your provider by replacing all "hana"
# occurrences with your provider name.
# This target can only be run once, if you want to rerun for some reason,
# consider stashing/resetting your git state.
# Arguments:
#   provider: Camel case name of your provider, e.g. GitHub, PlanetScale
provider.prepare:
	@[ "${provider}" ] || ( echo "argument \"provider\" is not set"; exit 1 )
	@PROVIDER=$(provider) ./hack/helpers/prepare.sh

# This target adds a new api type and its controller.
# You would still need to register new api in "apis/<provider>.go" and
# controller in "internal/controller/<provider>.go".
# Arguments:
#   provider: Camel case name of your provider, e.g. GitHub, PlanetScale
#   group: API group for the type you want to add.
#   kind: Kind of the type you want to add
#	apiversion: API version of the type you want to add. Optional and defaults to "v1alpha1"
provider.addtype: $(GOMPLATE)
	@[ "${provider}" ] || ( echo "argument \"provider\" is not set"; exit 1 )
	@[ "${group}" ] || ( echo "argument \"group\" is not set"; exit 1 )
	@[ "${kind}" ] || ( echo "argument \"kind\" is not set"; exit 1 )
	@PROVIDER=$(provider) GROUP=$(group) KIND=$(kind) APIVERSION=$(apiversion) ./hack/helpers/addtype.sh

define CROSSPLANE_MAKE_HELP
Crossplane Targets:
    submodules            Update the submodules, such as the common build scripts.
    run                   Run crossplane locally, out-of-cluster. Useful for development.

endef
# The reason CROSSPLANE_MAKE_HELP is used instead of CROSSPLANE_HELP is because the crossplane
# binary will try to use CROSSPLANE_HELP if it is set, and this is for something different.
export CROSSPLANE_MAKE_HELP

crossplane.help:
	@echo "$$CROSSPLANE_MAKE_HELP"

help-special: crossplane.help

.PHONY: crossplane.help help-special
