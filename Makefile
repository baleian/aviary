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
FAKE_SUPERVISOR_IMAGE := aviary-fake-supervisor:dev
# The harness images that make test-harness tests. Another one is tested with
# make test-harness HARNESS_IMAGES=<image>
HARNESS_IMAGES ?= $(OPENCODE_IMAGE) $(CODEX_IMAGE)

KUBECTL := kubectl --context kind-$(CLUSTER)
HELM := helm --kube-context kind-$(CLUSTER)
# Runs go in the image that builds the Supervisor, so the host needs no Go.
# It is called with the directory of a module, and with options for docker if
# there are any. The volume keeps what go downloads and builds.
GO = docker run --rm --user $$(id -u):$$(id -g) --env HOME=/tmp --env GOCACHE=/go/cache \
	--volume aviary-go:/go --volume $(CURDIR):/src --workdir /src/$(1) $(2) $(GO_IMAGE) go
GO_TEST := test -race -shuffle=on -count=1 -timeout 2m
# What the tests make of the chart, and the image of Envoy that the chart
# runs as the Egress Proxy.
RENDER := helm template aviary deploy/charts/aviary --namespace aviary-system
ENVOY_IMAGE = $(shell sed --quiet 's/^  image: \(envoyproxy.*\)/\1/p' deploy/charts/aviary/values.yaml)
# What the tests of the Supervisor run against, and how the tests reach it.
SERVICES := docker compose --file test/compose.yaml --project-name aviary-test-supervisor
IN_SERVICES := --network aviary-test-supervisor_default \
	--env AVIARY_TEST_DATABASE_URL=postgres://postgres:test@postgres:5432/postgres \
	--env AVIARY_TEST_VALKEY_URL=valkey://default:test@valkey:6379

# The value of a variable in .env, or nothing if there is no .env.
dotenv = $$(test ! -f .env || sed --quiet 's/^$(1)=//p' .env)

.PHONY: proto sqlc test test-supervisor test-runner test-mock-llm test-chart test-harness cluster-up cluster-down build images deploy e2e

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

# The tests that need no cluster. TESTS narrows a run to some of the tests
# of one of them, for example
# make test-supervisor TESTS='-run TestEndTurn ./internal/store'
# make test-runner TESTS=cut
test: test-supervisor test-runner test-mock-llm test-chart

# The services are started anew for every run and removed after it, also
# when a test fails.
test-supervisor: TESTS ?= ./...
test-supervisor:
	$(SERVICES) up --detach --wait --force-recreate
	$(call GO,supervisor,$(IN_SERVICES)) $(GO_TEST) $(TESTS); \
		status=$$?; $(SERVICES) down --volumes; exit $$status

test-runner:
	cargo test --locked --package aviary-runner $(TESTS)

test-mock-llm: TESTS ?= ./...
test-mock-llm:
	$(call GO,test/mock-llm) $(GO_TEST) $(TESTS)

# Renders the chart with its own values and with those in test/chart, checks
# what it makes of them and that it refuses what it can make nothing of, and
# has Envoy check the configuration that it is given.
test-chart: TESTS ?= ./chart/...
test-chart:
	helm lint --strict --quiet deploy/charts/aviary
	mkdir -p .out/chart/llm
	echo key > .out/chart/llm/api-key
	$(RENDER) > .out/chart/default.yaml
	$(RENDER) --values test/chart/set.yaml > .out/chart/set.yaml
	$(RENDER) --values test/chart/closed.yaml > .out/chart/closed.yaml
	! $(RENDER) --set llm.url=llm.example.com/v1 --set llm.model=a-model > /dev/null 2>&1
	! $(RENDER) --set llm.url=https://llm.example.com/v1 > /dev/null 2>&1
	! $(RENDER) --set 'egressProxy.allowedHosts={https://example.com}' > /dev/null 2>&1
	$(call GO,test,--env AVIARY_TEST_RENDERED=/src/.out/chart) $(GO_TEST) $(TESTS)
	for values in default set closed; do \
		$(call GO,test) run ./internal/unpack /src/.out/chart/$$values.yaml /src/.out/chart/$$values && \
		docker run --rm --volume $(CURDIR)/.out/chart/$$values:/etc/envoy:ro \
			--volume $(CURDIR)/.out/chart/llm:/etc/aviary/llm:ro \
			$(ENVOY_IMAGE) --mode validate --log-level error --config-path /etc/envoy/envoy.yaml || exit 1; \
	done

# Tests the harness images as the pods of turns run them, in containers and
# without a cluster. A few at a time: a harness takes some memory.
test-harness: build
	AVIARY_HARNESS_IMAGES="$(HARNESS_IMAGES)" cargo test --locked --package aviary-runner --test harness -- \
		--ignored --test-threads 3 $(TESTS)

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

build:
	docker build --file images/supervisor/Dockerfile --tag $(SUPERVISOR_IMAGE) .
	docker build --file images/runner/Dockerfile --tag $(RUNNER_IMAGE) .
	docker build --tag $(HARNESS_BASE_IMAGE) images/harness-base
	docker build --tag $(OPENCODE_IMAGE) images/harness-opencode
	docker build --tag $(CODEX_IMAGE) images/harness-codex
	docker build --file images/mock-llm/Dockerfile --tag $(MOCK_LLM_IMAGE) .
	docker build --file images/fake-supervisor/Dockerfile --tag $(FAKE_SUPERVISOR_IMAGE) .

images: build
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
