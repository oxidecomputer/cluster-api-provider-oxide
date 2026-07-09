# Image URL to use all building/pushing image targets
IMAGE_REPO ?= ghcr.io/oxidecomputer/cluster-api-provider-oxide
IMAGE_TAG ?= dev
IMG ?= $(IMAGE_REPO):$(IMAGE_TAG)
KO_DOCKER_REPO ?= $(IMAGE_REPO)
KOCACHE ?= ~/.ko
MAKEFILE_PATH := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
TOOLS_MOD := $(MAKEFILE_PATH)/tools/go.mod
GO_TOOL := go tool -modfile=$(TOOLS_MOD)
NAMESPACE ?= capox-system

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
all: precommit

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

.PHONY: generate
generate: ## Generate code and CRDs w/ controllergen
	go generate ./...
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd paths="./..." \
		output:crd:artifacts:config=charts/cluster-api-provider-oxide/crds \
		output:rbac:artifacts:config=charts/cluster-api-provider-oxide/generated
	mv charts/cluster-api-provider-oxide/generated/role.yaml \
		charts/cluster-api-provider-oxide/generated/clusterrole.yaml

.PHONY: verify
verify: release-check ## Run linters and vetting on codebase with no commit checks and no mutations.
	"$(GOLANGCI_LINT)" config verify
	$(GOLANGCI_LINT) run
	go vet ./...

.PHONY: fix
fix: golangci-lint ## Perform lint, fmt, and mod/sum file fixes
	go fmt ./...
	go mod tidy
	cd tools/ && go mod tidy
	$(GOLANGCI_LINT) run --fix

.PHONY: test
test: generate setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test -v $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: precommit
precommit: fix verify test release-snapshot ## Run all precommit checks (lint, fix, fmt, local tests, local snapshot release). NOTE: does not run e2e tests

KIND_CLUSTER_NAME ?= capox-e2e
ARTIFACTS    ?= $(PWD)/_artifacts

.PHONY: setup-test-e2e
setup-test-e2e:
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER_NAME)"*) \
			echo "Kind cluster '$(KIND_CLUSTER_NAME)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER_NAME)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER_NAME) ;; \
	esac

.PHONY: test-e2e
test-e2e: generate tools setup-test-e2e render-e2e-ccm render-e2e-capox ## Run e2e tests on a local KinD cluster
	@mkdir -p $(ARTIFACTS)
	$(KIND) get kubeconfig --name $(KIND_CLUSTER_NAME) > $(ARTIFACTS)/kubeconfig

# Tear down the Kind cluster only if the tests pass; on failure, leave
# everything in place for debugging.
	KUBECONFIG=$(ARTIFACTS)/kubeconfig \
		go test ./tests/e2e/ -timeout 30m -v -args \
		  -e2e.config=$(PWD)/tests/e2e/config/oxide.yaml \
		  -e2e.artifacts-folder=$(ARTIFACTS) \
		  -ginkgo.v \
	&& $(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	$(KIND) get kubeconfig --name $(KIND_CLUSTER_NAME) > $(ARTIFACTS)/kubeconfig
	KUBECONFIG=$(ARTIFACTS)/kubeconfig $(KUBECTL) delete crd clusters.cluster.x-k8s.io --timeout=30s
	@$(KIND) delete cluster --name $(KIND_CLUSTER_NAME)

CCM_RELEASE ?= ccm
# Leave CCM_VERSION empty to let Helm resolve the latest published chart;
# set it (e.g. CCM_VERSION=0.7.0) to pin a specific version.
CCM_VERSION ?=

.PHONY: render-e2e-ccm
render-e2e-ccm:
	@mkdir -p tests/e2e/data/ccm
	helm template $(CCM_RELEASE) \
	  oci://ghcr.io/oxidecomputer/helm-charts/oxide-cloud-controller-manager \
	  $(if $(CCM_VERSION),--version $(CCM_VERSION),) \
	  --namespace kube-system \
	  > tests/e2e/data/ccm/oxide-ccm.yaml

.PHONY: render-e2e-capox
render-e2e-capox: generate build-kind
	@mkdir -p tests/e2e/data/capox
	# clusterctl defaults a provider's target namespace from a Namespace object in
	# the components YAML (inspectTargetNamespace). helm template never emits one,
	# so prepend it; clusterctl then stamps it onto every namespaced object.
	printf 'apiVersion: v1\nkind: Namespace\nmetadata:\n  name: $(NAMESPACE)\n---\n' \
		> tests/e2e/data/capox/capox.yaml
	# Render the exact image ref build-kind captured, so the e2e deployment uses
	# the image just built and loaded into kind.
	REF="$$(cat $(IMAGE_REF_FILE))"; \
	helm template capox charts/cluster-api-provider-oxide \
		--namespace $(NAMESPACE) \
		--include-crds \
		--set image.repository="$${REF%:*}" \
		--set image.tag="$${REF##*:}" >> tests/e2e/data/capox/capox.yaml

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
KO ?= KO_CACHE=$(KO_CACHE) $(GO_TOOL) ko
KUSTOMIZE ?= $(GO_TOOL) kustomize
export HELM_OCI_REPO=$(IMAGE_REPO)/helm-charts
GORELEASER ?= $(GO_TOOL) goreleaser
ENVTEST ?= go tool setup-envtest # this tool is in the main go.mod so the version stays in-sync
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint-custom

.PHONY: tools
tools: kubectl $(LOCALBIN) setup-envtest golangci-lint ## Install all required dev tools to the repo's local bin/
	GOBIN="$(LOCALBIN)" go install -modfile=$(TOOLS_MOD) tool

.PHONY: kubectl
kubectl: $(LOCALBIN)
	@command -v $(KUBECTL) >/dev/null 2>&1 && exit 0; \
		v=$$(curl -fsSL "https://dl.k8s.io/release/stable-$(ENVTEST_K8S_VERSION).txt"); \
		curl -fsSL -o "$(LOCALBIN)/kubectl" "https://dl.k8s.io/release/$$v/bin/$$(go env GOOS)/$$(go env GOARCH)/kubectl"; \
		chmod +x "$(LOCALBIN)/kubectl"

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

.PHONY: setup-envtest
setup-envtest:
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: golangci-lint
golangci-lint:
	@echo "Building custom golangci-lint with plugins..."
	go tool -modfile $(TOOLS_MOD) golangci-lint custom --destination $(LOCALBIN) --name golangci-lint-custom


# ko prints the pushed image reference (repo@sha256:...) on stdout; capture it
# so downstream targets (e.g. apply) can deploy the exact image just built.
# Recipes run in separate shells, so the handoff goes through a file with a
# name fixed at the Make level rather than a shell variable or mktemp.
IMAGE_REF_FILE := $(shell echo "$${TMPDIR:-/tmp}")/capox-image-ref

.PHONY: build
build: generate ## Builds a container image using ko and pushes to $KO_DOCKER_REPO
	$(KO) build ./cmd | tee $(IMAGE_REF_FILE)

.PHONY: build-kind
build-kind:
	# A KO_DOCKER_REPO under kind.local selects ko's kind publisher, which writes
	# the image straight into the kind nodes -- no docker daemon or `kind load`
	# round-trip. Do NOT add --local: it takes precedence over kind.local and
	# silently routes to the docker daemon publisher instead. The repo needs a
	# path component ("/...") or containerd normalizes the name and ko's tagging
	# step fails. ko tags the image <repo>:<digest-hex> and prints that ref;
	# capture it so downstream targets deploy the exact image just built
	# (pullPolicy must not be Always).
	KO_DOCKER_REPO=kind.local/cluster-api-provider-oxide KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) \
		$(KO) build --bare ./cmd | tee $(IMAGE_REF_FILE)

.PHONY: run
run: generate ## Run a controller from your host.
	go run ./cmd/main.go

.PHONY: manifests
manifests: generate
	mkdir -p $(ARTIFACTS)
# helm never emits a Namespace object, so prepend one. The sed strips the
# helm-specific labels the chart renders (helm.sh/chart, managed-by: Helm);
# nothing in this manifest is managed by a helm release.
	printf 'apiVersion: v1\nkind: Namespace\nmetadata:\n  name: $(NAMESPACE)\n---\n' > $(ARTIFACTS)/infrastructure-components.yaml
	helm template capox charts/cluster-api-provider-oxide \
		--namespace $(NAMESPACE) \
		--include-crds \
		--set image.repository=$(IMAGE_REPO) \
		--set image.tag=$(IMAGE_TAG) \
		| sed -e '/^[[:space:]]*helm\.sh\/chart:/d' \
		      -e '/^[[:space:]]*app\.kubernetes\.io\/managed-by:/d' \
		>> $(ARTIFACTS)/infrastructure-components.yaml

##@ Dev Deployment

.PHONY: install-crds
install-crds: generate ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	helm upgrade --install capox-crds-dev charts/cluster-api-provider-oxide-crds

.PHONY: uninstall-crds
uninstall-crds: ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	helm uninstall capox-crds-dev charts/cluster-api-provider-oxide-crds

.PHONY: deploy
deploy: generate ## Deploy controller to the K8s cluster specified in ~/.kube/config. This pulls a remote image, but uses the local helm chart.
	helm upgrade --install capox-dev charts/cluster-api-provider-oxide \
		--create-namespace \
		--namespace $(NAMESPACE) \
		--set image.repository=$(IMAGE_REPO) \
		--set image.tag=$(IMAGE_TAG) \
		--wait

# Internal: deploy whatever image ref a build target captured in
# $(IMAGE_REF_FILE). The chart renders "repository:tag", so splitting the ref
# on its last colon recomposes to the exact ref ko printed — a digest ref
# (repo@sha256 + hex) for registry builds, a digest-hex tag for --local
# builds. Either way the ref is unique per build, so every deploy rolls the
# pods.
.PHONY: helm-apply
helm-apply:
	REF="$$(cat $(IMAGE_REF_FILE))"; \
	helm upgrade --install capox-dev charts/cluster-api-provider-oxide \
		--create-namespace \
		--namespace $(NAMESPACE) \
		--set image.repository="$${REF%:*}" \
		--set image.tag="$${REF##*:}" \
		--wait

.PHONY: apply
apply: generate build helm-apply ## Build/push the image with ko and deploy that exact digest to the current kube context.

.PHONY: deploy-kind
deploy-kind: build-kind helm-apply ## Build the image, load it into KinD, and deploy the controller

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	helm uninstall capox-dev --namespace $(NAMESPACE) --wait

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef

##@ CI and Release

.PHONY: ci-verify
ci-verify: fix verify test ## Verify fmt, linters, mod/sum files, etc. and fail if any mutate.
	@git diff --exit-code || { \
		echo; \
		echo "ERROR: working tree is dirty after 'make ci-verify'."; \
		echo "Run 'make fix' locally and commit the result."; \
		exit 1; \
	}

# Release is driven by GoReleaser (config in .goreleaser.yaml), which uses ko to
# build and push the multi-arch images. Both CI and local runs go through these
# targets so they share one interface. Registry auth comes from the environment
# (`docker login ghcr.io`); in GitHub Actions that is handled by the release
# workflow. GoReleaser also needs GITHUB_TOKEN to create the GitHub release.

.PHONY: release-check
release-check:
	$(GORELEASER) check

.PHONY: release-snapshot
release-snapshot: ## Build the release artifacts locally without publishing (images go to a local registry).
	$(GORELEASER) release --snapshot --clean

.PHONY: release
release: ## Build and push the multi-arch (linux/amd64,linux/arm64) images to ghcr and cut a GitHub release.
	$(GORELEASER) release --clean
	