CLUSTER := aviary
NODE_IMAGE := kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5
NFS_PROVISIONER_CHART := 1.8.0
CNPG_CHART := 0.29.1

SUPERVISOR_IMAGE := aviary-supervisor:dev
RUNNER_IMAGE := aviary-runner:dev
HARNESS_BASE_IMAGE := aviary-harness-base:dev
OPENCODE_IMAGE := aviary-harness-opencode:dev
CODEX_IMAGE := aviary-harness-codex:dev

KUBECTL := kubectl --context kind-$(CLUSTER)
HELM := helm --kube-context kind-$(CLUSTER)

.PHONY: cluster-up cluster-down images deploy e2e

cluster-up:
	kind get clusters 2>/dev/null | grep -qx $(CLUSTER) || \
		kind create cluster --name $(CLUSTER) --image $(NODE_IMAGE) --config hack/kind/cluster.yaml
	$(HELM) upgrade --install nfs-server-provisioner nfs-server-provisioner \
		--repo https://kubernetes-sigs.github.io/nfs-ganesha-server-and-external-provisioner \
		--version $(NFS_PROVISIONER_CHART) \
		--namespace nfs --create-namespace \
		--values hack/kind/nfs-server-provisioner-values.yaml \
		--wait
	$(KUBECTL) apply -f hack/kind/runtime.yaml
	$(HELM) upgrade --install cloudnative-pg cloudnative-pg \
		--repo https://cloudnative-pg.github.io/charts \
		--version $(CNPG_CHART) \
		--namespace cnpg-system --create-namespace \
		--wait
	$(KUBECTL) apply -f hack/kind/postgres.yaml
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
# the Supervisor's. The image ID makes the pod restart when the image changes.
deploy:
	$(KUBECTL) create namespace aviary-system --dry-run=client --output yaml | $(KUBECTL) apply -f -
	$(KUBECTL) get secret postgres-app --namespace aviary-data --output json | \
		jq '{apiVersion, kind, type, data, metadata: {name: "aviary-database"}}' | \
		$(KUBECTL) apply --namespace aviary-system -f -
	$(HELM) upgrade --install aviary deploy/charts/aviary \
		--namespace aviary-system \
		--set-string 'supervisor.podAnnotations.aviary\.io/image-id='$$(docker image inspect --format '{{.Id}}' $(SUPERVISOR_IMAGE)) \
		--wait

e2e:
	test/e2e/run.sh $(HARNESS)
