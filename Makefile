# Image URL to use all building/pushing image targets
IMAGE_REPO ?= ghcr.io/oxidecomputer/cluster-api-provider-oxide
IMAGE_TAG ?= dev
IMG ?= $(IMAGE_REPO):$(IMAGE_TAG)
KO_DOCKER_REPO ?= $(IMAGE_REPO)
MAKEFILE_PATH := $(shell cd "$(dirname "$0")" ; pwd -P )
TOOLS_MOD := $(MAKEFILE_PATH)/tools/go.mod
GO_TOOL := go tool -modfile=$(TOOLS_MOD)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations, as well as `go generate`-d files.
	go generate ./...
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: verify
verify: ## Run linters and vetting on codebase with no commit checks and no mutations.
	"$(GOLANGCI_LINT)" config verify
	$(GOLANGCI_LINT) run
	go vet ./...

.PHONY: fix
fix: golangci-lint ## Perform lint, fmt, and mod/sum file fixes
	go fmt ./...
	go mod tidy
	cd tools/ && go mod tidy
	$(GOLANGCI_LINT) run --fix

.PHONY: precommit
precommit: fix verify test ## Run all precommit checks

.PHONY: test
test: manifests generate setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test -v $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
# CertManager is installed by default; skip with:
# - CERT_MANAGER_INSTALL_SKIP=true
KIND_CLUSTER_NAME ?= capox-e2e
ARTIFACTS    ?= $(PWD)/_artifacts

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER_NAME)"*) \
			echo "Kind cluster '$(KIND_CLUSTER_NAME)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER_NAME)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER_NAME) ;; \
	esac

.PHONY: test-e2e
test-e2e: manifests generate tools setup-test-e2e ## Run e2e tests on a local KinD cluster
	@mkdir -p $(ARTIFACTS)

	# Build the manager image into the local Docker daemon under the exact name the
	# e2e config and config/manager kustomization reference, then load it into the
	# kind cluster so the controller deployment can pull it (imagePullPolicy=IfNotPresent).
	DOCKER_HOST="$$(docker context inspect --format '{{.Endpoints.docker.Host}}')" \
		KO_DOCKER_REPO=$(IMAGE_REPO) $(KO) build --bare --tags $(IMAGE_TAG) --local ./cmd
	$(KIND) load docker-image $(IMG) --name $(KIND_CLUSTER_NAME)

	$(KIND) get kubeconfig --name $(KIND_CLUSTER_NAME) > $(ARTIFACTS)/kubeconfig
	KUBECONFIG=$(ARTIFACTS)/kubeconfig \
		go test ./tests/e2e/ -timeout 30m -v -args \
		  -e2e.config=$(PWD)/tests/e2e/config/oxide.yaml \
		  -e2e.artifacts-folder=$(ARTIFACTS) \
		  -ginkgo.v

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER_NAME)

CCM_RELEASE ?= capox
CCM_VERSION ?= 0.6.0

.PHONY: render-e2e-ccm
render-e2e-ccm: ## Render the Oxide CCM manifest used by e2e tests.
	@mkdir -p tests/e2e/data/ccm
	helm template $(CCM_RELEASE) \
	  oci://ghcr.io/oxidecomputer/helm-charts/oxide-cloud-controller-manager \
	  --version $(CCM_VERSION) \
	  --namespace kube-system \
	  > tests/e2e/data/ccm/oxide-ccm.yaml

##@ Build

.PHONY: build
build: manifests generate ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate ## Run a controller from your host.
	go run ./cmd/main.go

.PHONY: build-installer
build-installer: manifests generate ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( $(KUSTOMIZE) build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | $(KUBECTL) apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( $(KUSTOMIZE) build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

# Put locally-installed tool binaries (populated by the `tools` target) ahead of the system
# PATH so tools that subprocesses shell out to -- e.g. the e2e framework / clusterctl invoking
# kustomize -- resolve from here rather than requiring a global install.
export PATH := $(LOCALBIN):$(PATH)

## Tool Binaries
CONTROLLER_GEN ?= $(GO_TOOL) controller-gen
KUBECTL ?= kubectl
KIND ?= $(GO_TOOL) kind
KO ?= $(GO_TOOL) ko
KUSTOMIZE ?= $(GO_TOOL) kustomize
GORELEASER ?= $(GO_TOOL) goreleaser
ENVTEST ?= go tool setup-envtest # this tool is in the main go.mod so the version stays in-sync
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint-custom

.PHONY: tools
tools: kubectl $(LOCALBIN) ## Install all tools declared in tools/go.mod as binaries into $(LOCALBIN)
	GOBIN="$(LOCALBIN)" go install -modfile=$(TOOLS_MOD) tool

.PHONY: kubectl
kubectl: $(LOCALBIN) ## Download kubectl matching ENVTEST_K8S_VERSION into the local bin directory.
	@command -v $(KUBECTL) >/dev/null 2>&1 && exit 0; \
		v=$$(curl -fsSL "https://dl.k8s.io/release/stable-$(ENVTEST_K8S_VERSION).txt"); \
		curl -fsSL -o "$(LOCALBIN)/kubectl" "https://dl.k8s.io/release/$$v/bin/$$(go env GOOS)/$$(go env GOARCH)/kubectl"; \
		chmod +x "$(LOCALBIN)/kubectl"

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

.PHONY: setup-envtest
setup-envtest: ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: golangci-lint
golangci-lint: ## Compile custom golangci-lint
	@echo "Building custom golangci-lint with plugins..."
	go tool -modfile $(TOOLS_MOD) golangci-lint custom --destination $(LOCALBIN) --name golangci-lint-custom

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef

##@ Continuous Integration and Release

.PHONY: ci-verify
ci-verify: fix verify ## Verify fmt, linters, mod/sum files, etc. and fail if any mutate.
	@git diff --exit-code || { \
		echo; \
		echo "ERROR: working tree is dirty after 'make fix'."; \
		echo "Run 'make fix' locally and commit the result."; \
		exit 1; \
	}

# Release is driven by GoReleaser (config in .goreleaser.yaml), which uses ko to
# build and push the multi-arch images. Both CI and local runs go through these
# targets so they share one interface. Registry auth comes from the environment
# (`docker login ghcr.io`); in GitHub Actions that is handled by the release
# workflow. GoReleaser also needs GITHUB_TOKEN to create the GitHub release.

.PHONY: release-check
release-check: ## Validate the GoReleaser configuration.
	$(GORELEASER) check

.PHONY: release-snapshot
release-snapshot: ## Build the release artifacts locally without publishing (images go to a local registry).
	$(GORELEASER) release --snapshot --clean

.PHONY: release
release: ## Build and push the multi-arch (linux/amd64,linux/arm64) images to ghcr and cut a GitHub release.
	$(GORELEASER) release --clean
	