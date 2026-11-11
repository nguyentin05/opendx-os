# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Build, code generation and cluster targets for dxos-operator.
# Derived from the kubebuilder v4 scaffold, trimmed to the targets this project uses.

IMG ?= dxos-operator:dev
KUBECTL ?= kubectl
CONTAINER_TOOL ?= docker
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

LOCALBIN ?= $(shell pwd)/bin
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
KUSTOMIZE ?= $(LOCALBIN)/kustomize
ENVTEST ?= $(LOCALBIN)/setup-envtest

CONTROLLER_TOOLS_VERSION ?= v0.22.0
KUSTOMIZE_VERSION ?= v5.8.1
ENVTEST_VERSION ?= v0.25.0
# Kubernetes minor used by envtest; matches k8s.io/api in go.mod.
ENVTEST_K8S_VERSION ?= 1.37

.PHONY: all help build run test test-integration test-envtest test-controller manifests generate samples fmt vet \
	docker-build install uninstall deploy undeploy build-installer controller-gen kustomize envtest

all: build

help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  %-18s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

##@ Development

manifests: controller-gen ## Generate the CRD and the manager ClusterRole into config/.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd paths="./..." output:crd:artifacts:config=config/crd/bases
	hack/add-spdx-headers.sh config

generate: controller-gen ## Generate DeepCopy methods for API types.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

SAMPLE_WORKSPACE ?= 11111111-1111-4111-8111-111111111111
samples: ## Export config/samples from the supported application packages.
	go run ./cmd/dxos-operator export-crd -workspace $(SAMPLE_WORKSPACE) ../apps/procurement > config/samples/dxos_v1alpha1_procurement.yaml
	go run ./cmd/dxos-operator export-crd -workspace $(SAMPLE_WORKSPACE) ../apps/process-fixture > config/samples/dxos_v1alpha1_process_fixture.yaml
	hack/add-spdx-headers.sh config/samples

fmt: ## Run go fmt.
	go fmt ./...

vet: ## Run go vet, including build-tagged suites.
	go vet ./...
	go vet -tags=integration ./...
	go vet -tags=envtest,integration ./...

test: ## Run unit tests (no external services).
	go test ./... -count=1

test-integration: ## Run PostgreSQL integration tests; requires TEST_DATABASE_URL.
	@test -n "$$TEST_DATABASE_URL" || { echo "TEST_DATABASE_URL is required"; exit 1; }
	go test -race -tags=integration ./... -count=1

test-envtest: envtest ## Run CRD schema tests against an envtest API server (no database).
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		go test -tags=envtest ./internal/controller/... -count=1

test-controller: envtest ## Run reconcile tests (envtest + PostgreSQL); requires TEST_DATABASE_URL.
	@test -n "$$TEST_DATABASE_URL" || { echo "TEST_DATABASE_URL is required"; exit 1; }
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		go test -race -tags=envtest,integration ./internal/controller/... -count=1

##@ Build

build: ## Build the dxos-operator binary into bin/.
	CGO_ENABLED=0 go build -o $(LOCALBIN)/dxos-operator ./cmd/dxos-operator

run: ## Run the controller from the host against the current kubeconfig.
	go run ./cmd/dxos-operator controller

docker-build: ## Build the container image.
	$(CONTAINER_TOOL) build -t $(IMG) .

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

install: manifests kustomize ## Install the CRD into the cluster of the current kubeconfig.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

uninstall: manifests kustomize ## Remove the CRD. Set ignore-not-found=true to ignore missing resources.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

deploy: manifests kustomize ## Deploy CRD, RBAC and the manager Deployment.
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

undeploy: kustomize ## Remove everything deployed by `make deploy`.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

build-installer: manifests generate kustomize ## Render a single install manifest into dist/install.yaml.
	mkdir -p dist
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/default > dist/install.yaml

##@ Tools

$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

# go-install-tool installs $(2)@$(3) as $(1) unless that exact version is present.
define go-install-tool
@[ -f "$(1)" ] && [ "$$(cat "$(1).version" 2>/dev/null)" = "$(3)" ] || { \
set -e; \
echo "Downloading $(2)@$(3)"; \
GOBIN="$(LOCALBIN)" go install $(2)@$(3); \
echo "$(3)" > "$(1).version"; \
}
endef
