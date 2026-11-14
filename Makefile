CLUSTER := aviary
NODE_IMAGE := kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5
NFS_PROVISIONER_CHART := 1.8.0
CNPG_CHART := 0.29.1
VALKEY_CHART := 1.0.0
GO_IMAGE := golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9
BUF := 1.75.0

SUPERVISOR_IMAGE := aviary-supervisor:dev
RUNNER_IMAGE := aviary-runner:dev
HARNESS_BASE_IMAGE := aviary-harness-base:dev
OPENCODE_IMAGE := aviary-harness-opencode:dev
CODEX_IMAGE := aviary-harness-codex:dev

KUBECTL := kubectl --context kind-$(CLUSTER)
HELM := helm --kube-context kind-$(CLUSTER)

.PHONY: proto cluster-up cluster-down images deploy e2e

# Writes the Go code for proto/ to supervisor/internal/gen. Run it after
# changing a .proto file. It runs in the image that builds the Supervisor, so
# the host needs no Go.
proto:
	docker run --rm --user $$(id -u):$$(id -g) --env HOME=/tmp \
		--volume $(CURDIR):/src --workdir /src $(GO_IMAGE) \
		go run github.com/bufbuild/buf/cmd/buf@v$(BUF) generate

cluster-up:
	kind get clusters 2>/dev/null | grep -qx $(CLUSTER) || \
		kind create cluster --name $(CLUSTER) --image $(NODE_IMAGE) --config hack/kind/cluster.yaml
	$(HELM) upgrade --install nfs-server-provisioner nfs-server-provisioner \
		--repo https://kubernetes-sigs.github.io/nfs-ganesha-server-and-external-provisioner \
		--version $(NFS_PROVISIONER_CHART) \
		--namespace nfs --create-namespace \
		--values hack/kind/nfs-server-provisioner-values.yaml \
		--wait
	$(HELM) upgrade --install cloudnative-pg cloudnative-pg \
		--repo https://cloudnative-pg.github.io/charts \
		--version $(CNPG_CHART) \
		--namespace cnpg-system --create-namespace \
		--wait
	$(KUBECTL) apply -f hack/kind/postgres.yaml
	$(KUBECTL) get secret valkey-users --namespace aviary-data >/dev/null 2>&1 || \
		$(KUBECTL) create secret generic valkey-users --namespace aviary-data \
			--from-literal=default=$$(openssl rand -hex 24)
	$(HELM) upgrade --install valkey valkey \
		--repo https://valkey.io/valkey-helm/ \
		--version $(VALKEY_CHART) \
		--namespace aviary-data \
		--values hack/kind/valkey-values.yaml \
		--wait
	$(KUBECTL) wait --namespace aviary-data --for=condition=Ready cluster/postgres --timeout=5m

cluster-down:
	kind delete cluster --name $(CLUSTER)

images:
	docker build --file images/supervisor/Dockerfile --tag $(SUPERVISOR_IMAGE) .
	docker build --file images/runner/Dockerfile --tag $(RUNNER_IMAGE) .
	docker build --tag $(HARNESS_BASE_IMAGE) images/harness-base
	docker build --tag $(OPENCODE_IMAGE) images/harness-opencode
	docker build --tag $(CODEX_IMAGE) images/harness-codex
	kind load docker-image --name $(CLUSTER) $(SUPERVISOR_IMAGE) $(RUNNER_IMAGE) $(OPENCODE_IMAGE) $(CODEX_IMAGE)

# CloudNativePG writes the database's connection secret next to the database.
# A pod can only read secrets in its own namespace, so the secret is copied to
# the Supervisor's. Valkey has no such secret, so its URL is put together
# from its password. The image ID makes the pod restart when the image changes.
# The turn pods read the model server's address and key from the aviary-llm
# secret, which is made from .env.
deploy:
	@test -f .env || { echo ".env is missing; it must set AVIARY_LLM_BASE_URL, AVIARY_LLM_API_KEY and AVIARY_LLM_MODEL"; exit 1; }
	$(KUBECTL) create namespace aviary-system --dry-run=client --output yaml | $(KUBECTL) apply -f -
	$(KUBECTL) get secret postgres-app --namespace aviary-data --output json | \
		jq '{apiVersion, kind, type, data, metadata: {name: "aviary-database"}}' | \
		$(KUBECTL) apply --namespace aviary-system -f -
	$(KUBECTL) get secret valkey-users --namespace aviary-data --output json | \
		jq '{apiVersion, kind, type, metadata: {name: "aviary-valkey"}, stringData: {uri: ("valkey://default:" + (.data.default | @base64d) + "@valkey.aviary-data:6379")}}' | \
		$(KUBECTL) apply --namespace aviary-system -f -
	$(HELM) upgrade --install aviary deploy/charts/aviary \
		--namespace aviary-system \
		--set-string 'supervisor.podAnnotations.aviary\.io/image-id='$$(docker image inspect --format '{{.Id}}' $(SUPERVISOR_IMAGE)) \
		--wait
	$(KUBECTL) create secret generic aviary-llm --namespace aviary-runtime --from-env-file .env \
		--dry-run=client --output yaml | $(KUBECTL) apply -f -

e2e:
	test/e2e/run.sh $(HARNESS)
