CLUSTER := aviary
NODE_IMAGE := kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5
NFS_PROVISIONER_CHART := 1.8.0

RUNNER_IMAGE := aviary-runner:dev
HARNESS_BASE_IMAGE := aviary-harness-base:dev
OPENCODE_IMAGE := aviary-harness-opencode:dev
CODEX_IMAGE := aviary-harness-codex:dev

KUBECTL := kubectl --context kind-$(CLUSTER)
HELM := helm --kube-context kind-$(CLUSTER)

.PHONY: cluster-up cluster-down images e2e

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

cluster-down:
	kind delete cluster --name $(CLUSTER)

images:
	docker build --file images/runner/Dockerfile --tag $(RUNNER_IMAGE) .
	docker build --tag $(HARNESS_BASE_IMAGE) images/harness-base
	docker build --tag $(OPENCODE_IMAGE) images/harness-opencode
	docker build --tag $(CODEX_IMAGE) images/harness-codex
	kind load docker-image --name $(CLUSTER) $(RUNNER_IMAGE) $(OPENCODE_IMAGE) $(CODEX_IMAGE)

e2e:
	test/e2e/run.sh $(HARNESS)
