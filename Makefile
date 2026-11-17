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
# The cluster that the e2e makes for itself, and the harnesses that it runs
# turns of. The first of them runs what does not depend on the harness.
E2E_CLUSTER := aviary-e2e
E2E_KUBECTL := kubectl --context kind-$(E2E_CLUSTER)
E2E_HARNESSES ?= opencode codex
# Runs go in the image that builds the Supervisor, so the host needs no Go.
# It is called with the directory of a module, and with options for docker if
# there are any. The volume keeps what go downloads and builds.
IN_GO = docker run --rm --user $$(id -u):$$(id -g) --env HOME=/tmp --env GOCACHE=/go/cache \
	--volume aviary-go:/go --volume $(CURDIR):/src --workdir /src/$(1) $(2) $(GO_IMAGE)
GO = $(call IN_GO,$(1),$(2)) go
GO_TEST := test -race -shuffle=on -count=1 -timeout 2m
# The generators of the Go code in supervisor/internal/gen.
BUF_GENERATE = $(call GO) run github.com/bufbuild/buf/cmd/buf@v$(BUF) generate
SQLC_RUN := docker run --rm --user $$(id -u):$$(id -g) \
	--volume $(CURDIR)/supervisor:/src --workdir /src $(SQLC_IMAGE)
# What the tests make of the chart, and the image of Envoy that the chart
# runs as the Egress Proxy.
RENDER := helm template aviary deploy/charts/aviary --namespace aviary-system
ENVOY_IMAGE = $(shell sed --quiet 's/^  image: \(envoyproxy.*\)/\1/p' deploy/charts/aviary/values.yaml)
# The Egress Proxies that the tests of the proxy run against, and the names
# under which those proxies find the tests.
PROXIES = ENVOY_IMAGE=$(ENVOY_IMAGE) docker compose --file test/proxy/compose.yaml --project-name aviary-test-proxy
AMONG_PROXIES := --network aviary-test-proxy_default --env AVIARY_TEST_PROXY=1 \
	--network-alias aviary-supervisor.aviary-system --network-alias model.test \
	--network-alias allowed.test --network-alias other.test \
	--network-alias tls.test --network-alias unnamed.test
# What the tests of the Supervisor run against, and how the tests reach it.
SERVICES := docker compose --file test/compose.yaml --project-name aviary-test-supervisor
IN_SERVICES := --network aviary-test-supervisor_default \
	--env AVIARY_TEST_DATABASE_URL=postgres://postgres:test@postgres:5432/postgres \
	--env AVIARY_TEST_VALKEY_URL=valkey://default:test@valkey:6379

# The value of a variable in .env, or nothing if there is no .env.
dotenv = $$(test ! -f .env || sed --quiet 's/^$(1)=//p' .env)

.PHONY: proto sqlc lint test test-supervisor test-runner test-mock-llm test-chart test-proxy test-harness cluster-up cluster-down build images secrets deploy e2e e2e-up e2e-run e2e-down

# Writes the Go code for proto/ to supervisor/internal/gen. Run it after
# changing a .proto file.
proto:
	$(BUF_GENERATE)

# Writes the Go code for the Supervisor's queries, which are in
# supervisor/internal/store/queries.sql, to supervisor/internal/gen/db. Run it
# after changing a query or adding a migration.
sqlc:
	$(SQLC_RUN) generate

# What the formatters and the linters make of the code, and whether the
# generated code is what the generators make of its sources now.
lint:
	@files=$$($(call IN_GO) gofmt -l supervisor test) || exit 1; \
		[ -z "$$files" ] || { echo "gofmt would change these files:"; echo "$$files"; exit 1; }
	for module in supervisor test test/mock-llm; do $(call GO,$$module) vet ./... || exit 1; done
	cargo fmt --all --check
	cargo clippy --locked --all-targets -- --deny warnings
	rm -rf .out/gen
	$(BUF_GENERATE) --output .out/gen
	diff --recursive .out/gen/supervisor/internal/gen/aviary supervisor/internal/gen/aviary
	$(SQLC_RUN) diff

# The tests that need no cluster, all at once. Each of them is a target of
# its own, and TESTS narrows a run of one to some of its tests, for example
# make test-supervisor TESTS='-run TestEndTurn ./internal/store'
# make test-runner TESTS=cut
test:
	$(MAKE) --jobs --output-sync=target test-supervisor test-runner test-mock-llm test-chart test-proxy

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

# Runs the Egress Proxy with what the chart makes of each file of values in
# test/proxy, and sends it requests. The tests are the Supervisor and the
# hosts of the proxy, so the proxy finds them under those names.
test-proxy: TESTS ?= ./proxy/...
test-proxy:
	mkdir -p .out/proxy/llm
	printf the-real-key > .out/proxy/llm/api-key
	$(call GO,test) run ./internal/selfsigned tls.test /src/.out/proxy/tls
	for values in listed open closed; do \
		$(RENDER) --values test/proxy/$$values.yaml > .out/proxy/$$values.yaml && \
		$(call GO,test) run ./internal/unpack /src/.out/proxy/$$values.yaml /src/.out/proxy/$$values || exit 1; \
	done
	$(PROXIES) up --detach --force-recreate
	$(call GO,test,$(AMONG_PROXIES)) $(GO_TEST) $(TESTS); \
		status=$$?; [ $$status = 0 ] || $(PROXIES) logs --no-color --tail 40; \
		$(PROXIES) down --volumes; exit $$status

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
# from its password. The key that the Supervisor seals secrets with is made
# once: what was sealed with it does not open with another.
secrets:
	$(KUBECTL) create namespace aviary-system --dry-run=client --output yaml | $(KUBECTL) apply -f -
	$(KUBECTL) get secret postgres-app --namespace aviary-data --output json | \
		jq '{apiVersion, kind, type, data, metadata: {name: "aviary-database"}}' | \
		$(KUBECTL) apply --namespace aviary-system -f -
	$(KUBECTL) get secret valkey-users --namespace aviary-data --output json | \
		jq '{apiVersion, kind, type, metadata: {name: "aviary-valkey"}, stringData: {uri: ("valkey://default:" + (.data.default | @base64d) + "@valkey.aviary-data:6379")}}' | \
		$(KUBECTL) apply --namespace aviary-system -f -
	$(KUBECTL) get secret aviary-encryption --namespace aviary-system >/dev/null 2>&1 || \
		$(KUBECTL) create secret generic aviary-encryption --namespace aviary-system \
			--from-literal=k1=$$(openssl rand -base64 32)

# The image ID makes the pod restart when the image changes. The model
# endpoint is taken from .env if there is one: its address and the model's
# name become chart values, and its key a secret that only the Egress Proxy
# reads.
deploy: secrets
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

# Runs the e2e in a cluster of its own, which it makes anew, so that nothing
# of an earlier run or of the cluster that make cluster-up makes plays a part.
# The cluster is removed when the tests have passed. After a failure it
# stays, and what its pods logged is in .out/e2e: make e2e-run runs the tests
# in it again, and make e2e-down removes it.
e2e: e2e-up
	$(MAKE) e2e-run
	$(MAKE) e2e-down

e2e-up:
	$(MAKE) cluster-down cluster-up images secrets CLUSTER=$(E2E_CLUSTER)
	$(E2E_KUBECTL) apply --filename test/e2e/mock-llm.yaml
	helm --kube-context kind-$(E2E_CLUSTER) upgrade --install aviary deploy/charts/aviary \
		--namespace aviary-system --values test/e2e/values.yaml --wait
	$(E2E_KUBECTL) --namespace aviary-e2e rollout status deployment/mock-llm --timeout 120s

# The tests reach the cluster over the network of its nodes.
e2e-run:
	mkdir -p .out/e2e
	kind get kubeconfig --internal --name $(E2E_CLUSTER) > .out/e2e/kubeconfig
	$(call GO,test,--network kind --env AVIARY_E2E_KUBECONFIG=/src/.out/e2e/kubeconfig \
		--env AVIARY_E2E_HARNESSES="$(E2E_HARNESSES)") test -count=1 -timeout 20m -v ./e2e/... $(TESTS); \
		status=$$?; [ $$status = 0 ] || { \
			for part in supervisor egress-proxy; do \
				$(E2E_KUBECTL) --namespace aviary-system logs --selector app.kubernetes.io/component=$$part \
					--prefix --tail -1 > .out/e2e/$$part.log 2>&1; \
			done; \
			$(E2E_KUBECTL) get pods,jobs --all-namespaces --output wide > .out/e2e/pods.txt 2>&1; \
			$(E2E_KUBECTL) get events --all-namespaces --sort-by .lastTimestamp > .out/e2e/events.txt 2>&1; \
			echo "The e2e failed. The cluster $(E2E_CLUSTER) is left as it is, and what it tells is in .out/e2e."; \
		}; exit $$status

e2e-down:
	$(MAKE) cluster-down CLUSTER=$(E2E_CLUSTER)
