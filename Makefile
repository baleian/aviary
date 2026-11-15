CLUSTER := aviary
NODE_IMAGE := kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5
NFS_PROVISIONER_CHART := 1.8.0
CNPG_CHART := 0.29.1
VALKEY_CHART := 1.0.0
GO_IMAGE := golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9
BUF := 1.75.0
SQLC_IMAGE := sqlc/sqlc:1.31.1@sha256:70f53171d27b2424e9358869975455a6e955a5aa8e58a998a270a6e34e525537

SUPERVISOR_IMAGE := aviary-supervisor:dev
RUNNER_IMAGE := aviary-runner:dev
HARNESS_BASE_IMAGE := aviary-harness-base:dev
OPENCODE_IMAGE := aviary-harness-opencode:dev
CODEX_IMAGE := aviary-harness-codex:dev
MOCK_LLM_IMAGE := aviary-mock-llm:dev

KUBECTL := kubectl --context kind-$(CLUSTER)
HELM := helm --kube-context kind-$(CLUSTER)
# Runs go for the Supervisor in the image that builds it, so the host needs
# no Go. The volume keeps what go downloads and builds. Called with options
# for docker, it runs go with them.
GO = docker run --rm --user $$(id -u):$$(id -g) --env HOME=/tmp --env GOCACHE=/go/cache \
	--volume aviary-go:/go --volume $(CURDIR)/supervisor:/src --workdir /src $(1) $(GO_IMAGE) go
# What the tests of the Supervisor run against, and how the tests reach it.
SERVICES := docker compose --file test/compose.yaml --project-name aviary-test-supervisor
IN_SERVICES := --network aviary-test-supervisor_default \
	--env AVIARY_TEST_DATABASE_URL=postgres://postgres:test@postgres:5432/postgres \
	--env AVIARY_TEST_VALKEY_URL=valkey://default:test@valkey:6379

# The value of a variable in .env, or nothing if there is no .env.
dotenv = $$(test ! -f .env || sed --quiet 's/^$(1)=//p' .env)

.PHONY: proto sqlc test test-supervisor cluster-up cluster-down images deploy e2e

# Writes the Go code for proto/ to supervisor/internal/gen. Run it after
# changing a .proto file. It runs in the image that builds the Supervisor, so
# the host needs no Go.
proto:
	docker run --rm --user $$(id -u):$$(id -g) --env HOME=/tmp \
		--volume $(CURDIR):/src --workdir /src $(GO_IMAGE) \
		go run github.com/bufbuild/buf/cmd/buf@v$(BUF) generate

# Writes the Go code for the Supervisor's queries, which are in
# supervisor/internal/store/queries.sql, to supervisor/internal/gen/db. Run it
# after changing a query or adding a migration.
sqlc:
	docker run --rm --user $$(id -u):$$(id -g) \
		--volume $(CURDIR)/supervisor:/src --workdir /src $(SQLC_IMAGE) generate

# The tests that need no cluster.
test: test-supervisor

# The services are started anew for every run and removed after it, also
# when a test fails. TESTS narrows a run to some of the tests, for example
# make test-supervisor TESTS='-run TestEndTurn ./internal/store'
TESTS ?= ./...
test-supervisor:
	$(SERVICES) up --detach --wait --force-recreate
	$(call GO,$(IN_SERVICES)) test -race -shuffle=on -count=1 -timeout 2m $(TESTS); \
		status=$$?; $(SERVICES) down --volumes; exit $$status

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
	docker build --file images/mock-llm/Dockerfile --tag $(MOCK_LLM_IMAGE) .
	kind load docker-image --name $(CLUSTER) $(SUPERVISOR_IMAGE) $(RUNNER_IMAGE) $(OPENCODE_IMAGE) $(CODEX_IMAGE) $(MOCK_LLM_IMAGE)

# CloudNativePG writes the database's connection secret next to the database.
# A pod can only read secrets in its own namespace, so the secret is copied to
# the Supervisor's. Valkey has no such secret, so its URL is put together
# from its password. The image ID makes the pod restart when the image changes.
# The model endpoint is taken from .env if there is one: its address and the
# model's name become chart values, and its key a secret that only the Egress
# Proxy reads. The e2e does not need it: it brings a model server of its own.
deploy:
	$(KUBECTL) create namespace aviary-system --dry-run=client --output yaml | $(KUBECTL) apply -f -
	$(KUBECTL) get secret postgres-app --namespace aviary-data --output json | \
		jq '{apiVersion, kind, type, data, metadata: {name: "aviary-database"}}' | \
		$(KUBECTL) apply --namespace aviary-system -f -
	$(KUBECTL) get secret valkey-users --namespace aviary-data --output json | \
		jq '{apiVersion, kind, type, metadata: {name: "aviary-valkey"}, stringData: {uri: ("valkey://default:" + (.data.default | @base64d) + "@valkey.aviary-data:6379")}}' | \
		$(KUBECTL) apply --namespace aviary-system -f -
	test ! -f .env || sed --quiet 's/^AVIARY_LLM_API_KEY=//p' .env | tr --delete '\n' | \
		$(KUBECTL) create secret generic aviary-llm --namespace aviary-system --from-file=api-key=/dev/stdin \
			--dry-run=client --output yaml | $(KUBECTL) apply -f -
	$(HELM) upgrade --install aviary deploy/charts/aviary \
		--namespace aviary-system \
		--set-string 'supervisor.podAnnotations.aviary\.io/image-id='$$(docker image inspect --format '{{.Id}}' $(SUPERVISOR_IMAGE)) \
		--set-string llm.url="$(call dotenv,AVIARY_LLM_BASE_URL)" \
		--set-string llm.model="$(call dotenv,AVIARY_LLM_MODEL)" \
		--wait
	@test -f .env || echo "There is no .env, so the turns have no model endpoint. It sets AVIARY_LLM_BASE_URL, AVIARY_LLM_API_KEY and AVIARY_LLM_MODEL."

e2e:
	test/e2e/run.sh $(HARNESS)
