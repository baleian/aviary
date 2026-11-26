# The settings of a checkout: lines of NAME=value in .env, which Git ignores.
# They take precedence over the environment, and the commands that make runs
# have them in theirs.
ifneq ($(wildcard .env),)
include .env
export $(shell sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\) *=.*/\1/p' .env)
endif

# The public registries that the images come from. In a network that does
# not reach one, .env, the environment or the command line names its mirror:
# a registry, with a path if the mirror has one, such as
# mirror.example.com/docker-hub.
DOCKER_IO ?= docker.io
GCR_IO ?= gcr.io
GHCR_IO ?= ghcr.io
PUBLIC_ECR_AWS ?= public.ecr.aws
QUAY_IO ?= quay.io
REGISTRY_K8S_IO ?= registry.k8s.io
export DOCKER_IO GCR_IO
# The repository of the Mountpoint for Amazon S3 CSI driver, whose releases
# have its chart, or a mirror of it.
MOUNTPOINT_S3_CSI_REPO ?= https://github.com/awslabs/mountpoint-s3-csi-driver

CLUSTER := aviary
KIND_CONFIG ?= hack/kind/cluster.yaml
# A directory of this machine in which the local cluster keeps its volumes
# and the Secrets that their data depends on, so that a new cluster continues
# with the data of the last one. Without it, the data goes with the cluster.
DATA ?=
# The e2e runs in a cluster of its own. Clusters with different names can run
# the e2e at the same time.
E2E_CLUSTER ?= aviary-e2e
E2E_OUT := .out/e2e/$(E2E_CLUSTER)
E2E_KUBECONFIG := $(CURDIR)/$(E2E_OUT)/host.kubeconfig
# A run that names a target of the e2e works on the cluster of the e2e with
# all of its targets. That cluster binds no port on the host, keeps no data
# and has a kubeconfig of its own: kind locks the kubeconfig that it writes,
# and the kubeconfig of the user stays untouched.
E2E := $(filter e2e e2e-up e2e-run e2e-down,$(MAKECMDGOALS))
ifneq ($(E2E),)
CLUSTER := $(E2E_CLUSTER)
KIND_CONFIG := hack/kind/e2e-cluster.yaml
override DATA :=
export KUBECONFIG := $(E2E_KUBECONFIG)
endif
KEPT := $(abspath $(patsubst ~/%,$(HOME)/%,$(DATA)))
NODE_IMAGE := $(DOCKER_IO)/kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5
# Agent Substrate, which runs the sandboxes: the release that make substrate
# installs with its installer. The installer runs on this machine, in the
# source of the release, and both come from the Go module proxy.
SUBSTRATE := v1.0.1
SUBSTRATE_OUT := .out/substrate-$(SUBSTRATE)
# The harness images of the cluster, at its registry, by digest.
HARNESS_VALUES := .out/$(CLUSTER)-harnesses.yaml
# The chart of the Mountpoint for Amazon S3 CSI driver, which make versitygw
# installs in the local cluster: the package of its release, verified against
# its checksum. The chart in its source refuses to be installed.
MOUNTPOINT_S3_CSI := 2.9.2
MOUNTPOINT_S3_CSI_SHA256 := fc66f3fd2e747f5f9d4735e468329c100f9f34a87e38751f810446745ebe3d28
MOUNTPOINT_S3_CSI_CHART := .out/aws-mountpoint-s3-csi-driver-$(MOUNTPOINT_S3_CSI).tgz
GO_IMAGE := $(DOCKER_IO)/library/golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9
SQLC_IMAGE := $(DOCKER_IO)/sqlc/sqlc:1.31.1@sha256:70f53171d27b2424e9358869975455a6e955a5aa8e58a998a270a6e34e525537

SUPERVISOR_IMAGE := aviary-supervisor:dev
RUNNER_IMAGE := aviary-runner:dev
HARNESS_BASE_IMAGE := aviary-harness-base:dev
OPENCODE_IMAGE := aviary-harness-opencode:dev
CODEX_IMAGE := aviary-harness-codex:dev
CLAUDE_CODE_IMAGE := aviary-harness-claude-code:dev
MOCK_LLM_IMAGE := aviary-mock-llm:dev
WEB_IMAGE := aviary-web:dev
# Images that test-harness tests, for example
# make test-harness HARNESS_IMAGES=<image>
HARNESS_IMAGES ?= $(OPENCODE_IMAGE) $(CODEX_IMAGE) $(CLAUDE_CODE_IMAGE)
# The builds take the mirrors of the registries and, if the environment names
# them, those of the packages: GOPROXY, NPM_CONFIG_REGISTRY, CRATES_IO for
# crates.io and DEB_DEBIAN_ORG for http://deb.debian.org.
BUILD := docker build $(addprefix --build-arg ,DOCKER_IO GCR_IO GOPROXY NPM_CONFIG_REGISTRY CRATES_IO DEB_DEBIAN_ORG)

KUBECTL := kubectl --context kind-$(CLUSTER)
HELM := helm --kube-context kind-$(CLUSTER)
# With DATA, every node of the cluster mounts the directory of the volumes
# where the cluster makes them, and a Secret that the data depends on is kept
# with it. KEPT_MOUNTS adds the mount to the nodes of a kind configuration.
# KEPT_SECRET applies the Secret of the directory, before a target would
# create another, and KEEP_SECRET writes it there: their arguments are its
# name and its namespace.
ifneq ($(KEPT),)
KEPT_MOUNTS := awk -v volumes='$(KEPT)/volumes' '{ print } /^  - role:/ { printf "    extraMounts:\n      - {hostPath: \"%s\", containerPath: /var/local-path-provisioner}\n", volumes }'
KEPT_SECRET = [ ! -e '$(KEPT)/secrets/$(1).json' ] || $(KUBECTL) apply --filename '$(KEPT)/secrets/$(1).json'
KEEP_SECRET = secret=$$($(KUBECTL) get secret $(1) --namespace $(2) --output json) && \
	(umask 077 && printf '%s\n' "$$secret" | \
		jq '{apiVersion, kind, type, data, metadata: {name: .metadata.name, namespace: .metadata.namespace}}' > '$(KEPT)/secrets/$(1).json')
endif
# The registries that have a mirror, each as registry=mirror, for the nodes
# of the cluster.
NODE_MIRRORS := $(filter-out docker.io=docker.io gcr.io=gcr.io ghcr.io=ghcr.io public.ecr.aws=public.ecr.aws quay.io=quay.io registry.k8s.io=registry.k8s.io, \
	docker.io=$(DOCKER_IO) gcr.io=$(GCR_IO) ghcr.io=$(GHCR_IO) public.ecr.aws=$(PUBLIC_ECR_AWS) quay.io=$(QUAY_IO) registry.k8s.io=$(REGISTRY_K8S_IO))
# The harnesses that the e2e tests. The first also runs the tests that do not
# depend on a harness.
E2E_HARNESSES ?= opencode codex claude-code
# Runs Go in a container, so the host needs no Go toolchain. The arguments
# are the module directory and extra docker options. The aviary-go volume
# caches modules and builds. Go takes its module proxy from GOPROXY, if the
# environment names one.
IN_GO = docker run --rm --user $$(id -u):$$(id -g) --env HOME=/tmp --env GOCACHE=/go/cache --env GOPROXY \
	--volume aviary-go:/go --volume $(CURDIR):/src --workdir /src/$(1) $(2) $(GO_IMAGE)
GO = $(call IN_GO,$(1),$(2)) go
GO_TEST := test -race -shuffle=on -count=1 -timeout 2m
SQLC_RUN := docker run --rm --user $$(id -u):$$(id -g) \
	--volume $(CURDIR)/supervisor:/src --workdir /src $(SQLC_IMAGE)
# Cargo takes the crates of crates.io from the registry in CRATES_IO, if any.
CARGO := cargo$(if $(CRATES_IO), --config 'source.crates-io.replace-with="mirror"' --config 'source.mirror.registry="$(CRATES_IO)"')
RENDER := helm template aviary deploy/charts/aviary --namespace aviary-system
NGINX_IMAGE = $(DOCKER_IO)/$(shell sed -n 's/^FROM [^ ]*\/\(nginxinc[^ ]*\).*/\1/p' images/web/Dockerfile)
# The tests of the web server stand in for the API behind it.
WEB_SERVERS = NGINX_IMAGE=$(NGINX_IMAGE) docker compose --file test/web/compose.yaml --project-name aviary-test-web
AMONG_WEB_SERVERS := --network aviary-test-web_default --env AVIARY_TEST_WEB=1 --network-alias api.test
SERVICES := docker compose --file test/compose.yaml --project-name aviary-test-supervisor
IN_SERVICES := --network aviary-test-supervisor_default \
	--env AVIARY_TEST_POSTGRES_URL=postgres://postgres:test@database-postgres:5432/postgres \
	--env AVIARY_TEST_MYSQL_URL=mysql://root:test@database-mysql:3306/ \
	--env AVIARY_TEST_VALKEY_URL=valkey://default:test@events-valkey:6379 \
	--env AVIARY_TEST_REDIS_URL=redis://default:test@events-redis:6379 \
	--env AVIARY_TEST_SECRET_STORE_URL=http://secret-store:8200

.PHONY: sqlc lint test test-supervisor test-plugin test-runner test-mock-llm test-web test-web-server test-chart test-docs test-harness chart cluster-up cluster-down substrate postgres mysql valkey redis openbao vault versitygw keycloak headlamp grafana web web-dev docs docs-dev build images secrets deploy e2e e2e-up e2e-run e2e-down

# Generates the Go code of the SQL queries. Run it after changing a query or
# a migration.
sqlc:
	$(SQLC_RUN) generate

# Checks formatting, runs the linters, and checks that the generated code is
# up to date.
lint: web/node_modules/.package-lock.json docs/node_modules/.package-lock.json
	@files=$$($(call IN_GO) gofmt -l supervisor test) || exit 1; \
		[ -z "$$files" ] || { echo "gofmt would change these files:"; echo "$$files"; exit 1; }
	for module in supervisor test test/mock-llm test/plugin; do $(call GO,$$module) vet ./... || exit 1; done
	cargo fmt --all --check
	$(CARGO) clippy --locked --all-targets -- --deny warnings
	$(SQLC_RUN) diff
	cd web && npm run lint
	cd docs && npm run lint

# Runs the tests that need no cluster, in parallel. TESTS narrows a single
# target, for example
# make test-supervisor TESTS='-run TestStore/EndTurn ./store/postgres'
# make test-runner TESTS=cut
test:
	$(MAKE) --jobs $(if $(filter output-sync,$(.FEATURES)),--output-sync=target) test-supervisor test-plugin test-runner test-mock-llm test-web test-web-server test-chart test-docs

test-supervisor: TESTS ?= ./...
test-supervisor:
	$(SERVICES) up --detach --wait --force-recreate
	$(call GO,supervisor,$(IN_SERVICES)) $(GO_TEST) $(TESTS); \
		status=$$?; $(SERVICES) down --volumes; exit $$status

# Builds the Supervisor from another module, with plugins of its own.
test-plugin: TESTS ?= ./...
test-plugin:
	$(call GO,test/plugin) $(GO_TEST) $(TESTS)

test-runner:
	$(CARGO) test --locked --package aviary-runner $(TESTS)

test-mock-llm: TESTS ?= ./...
test-mock-llm:
	$(call GO,test/mock-llm) $(GO_TEST) $(TESTS)

test-web: web/node_modules/.package-lock.json
	cd web && npm test -- $(TESTS)

# Builds the documentation, which checks the links between its pages, and
# checks that its reference has the requests and the metrics of the
# Supervisor.
test-docs: TESTS ?= ./docs/...
test-docs: docs
	$(call GO,test) $(GO_TEST) $(TESTS)

# Renders the chart with its default values, with those in test/chart and
# with a claim of the installation for the depot, checks the output, and
# checks that invalid values are refused.
test-chart: TESTS ?= ./chart/...
test-chart:
	helm lint --strict --quiet deploy/charts/aviary
	mkdir -p .out/chart
	$(RENDER) > .out/chart/default.yaml
	$(RENDER) --values test/chart/set.yaml > .out/chart/set.yaml
	$(RENDER) --values test/chart/large.yaml > .out/chart/large.yaml
	$(RENDER) --set depot.volume.existingClaim=objects > .out/chart/claim.yaml
	! $(RENDER) --set depot.volume.accessMode=ReadOnlyMany > /dev/null 2>&1
	! $(RENDER) --set runtime.harnesses.opencode.image=aviary-harness-opencode:dev > /dev/null 2>&1
	$(call GO,test,--env AVIARY_TEST_RENDERED=/src/.out/chart) $(GO_TEST) $(TESTS)

# Runs the server of the web UI's image with the configuration of the image,
# in front of an API and alone, and sends it requests. A page and a file
# stand in for the web UI.
test-web-server: TESTS ?= ./web/...
test-web-server:
	mkdir -p .out/web/assets
	echo '<!doctype html><title>the web UI</title>' > .out/web/index.html
	printf '%4096s' '' > .out/web/assets/app.js
	$(WEB_SERVERS) up --detach --force-recreate
	$(call GO,test,$(AMONG_WEB_SERVERS)) $(GO_TEST) $(TESTS); \
		status=$$?; [ $$status = 0 ] || $(WEB_SERVERS) logs --no-color --tail 40; \
		$(WEB_SERVERS) down --volumes; exit $$status

# Tests the harness images in containers, without a cluster. Few run at a
# time, since every harness needs memory.
test-harness: build
	AVIARY_HARNESS_IMAGES="$(HARNESS_IMAGES)" $(CARGO) test --locked --package aviary-runner --test harness -- \
		--ignored --test-threads 3 $(TESTS)

# The installer of Agent Substrate, built for this machine, and the source of
# the release next to it, in which the installer runs.
$(SUBSTRATE_OUT)/ate-setup:
	mkdir -p $(SUBSTRATE_OUT)/source
	os=$$(uname -s | tr '[:upper:]' '[:lower:]') && \
	case $$(uname -m) in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; *) echo "No build of the installer for $$(uname -m)." >&2; exit 1;; esac && \
	$(call IN_GO,,--env GOOS=$$os --env GOARCH=$$arch --env GOFLAGS=-mod=mod) sh -c ' \
		source=$$(go mod download -json go.ate.dev/substrate@$(SUBSTRATE) | sed -n "s/^.\"Dir\": \"\(.*\)\",$$/\1/p") && \
		cp -R "$$source/." /src/$(SUBSTRATE_OUT)/source/ && chmod -R u+w /src/$(SUBSTRATE_OUT)/source && \
		cd /src/$(SUBSTRATE_OUT)/source && go build -trimpath -o ../ate-setup.new ./cmd/ate-setup'
	mv $(SUBSTRATE_OUT)/ate-setup.new $@

$(MOUNTPOINT_S3_CSI_CHART):
	mkdir -p $(@D)
	curl --fail --silent --show-error --location --output $@.new \
		$(MOUNTPOINT_S3_CSI_REPO)/releases/download/helm-chart-aws-mountpoint-s3-csi-driver-$(MOUNTPOINT_S3_CSI)/$(@F)
	openssl dgst -sha256 -r $@.new | grep -q '^$(MOUNTPOINT_S3_CSI_SHA256) ' || \
		{ echo "The checksum of $@.new is not $(MOUNTPOINT_S3_CSI_SHA256)." >&2; exit 1; }
	mv $@.new $@

# Packages the chart as .out/aviary-<version>.tgz.
chart:
	helm package deploy/charts/aviary --destination .out

# Installs the packages of the console in the versions that
# web/package-lock.json names.
web/node_modules/.package-lock.json: web/package-lock.json
	cd web && npm ci

# Installs the packages of the documentation in the versions that
# docs/package-lock.json names.
docs/node_modules/.package-lock.json: docs/package-lock.json
	cd docs && npm ci

cluster-up:
ifneq ($(KEPT),)
	mkdir -p '$(KEPT)/volumes' '$(KEPT)/secrets' .out
	$(KEPT_MOUNTS) $(KIND_CONFIG) > .out/cluster.yaml
endif
	kind get clusters 2>/dev/null | grep -qx $(CLUSTER) || \
		kind create cluster --name $(CLUSTER) --image $(NODE_IMAGE) --config $(if $(KEPT),.out/cluster.yaml,$(KIND_CONFIG))
ifneq ($(KEPT),)
# A cluster that was made without the directory cannot be given it.
	docker inspect --format '{{range .Mounts}}{{println .Type .Destination}}{{end}}' $(CLUSTER)-control-plane | \
		grep -qx 'bind /var/local-path-provisioner' || \
		{ echo "The cluster $(CLUSTER) keeps no data, as it was made without DATA: make cluster-down deletes it." >&2; exit 1; }
	$(KUBECTL) replace --force --filename hack/kind/storage-class.yaml
endif
# The nodes pull the images of a registry that has a mirror from the mirror.
	for mirror in $(NODE_MIRRORS); do \
		registry=$${mirror%%=*}; mirror=$${mirror#*=}; \
		case $$mirror in */*) root=$${mirror%%/*}/v2/$${mirror#*/};; *) root=$$mirror/v2;; esac; \
		for node in $$(kind get nodes --name $(CLUSTER)); do \
			docker exec $$node mkdir -p /etc/containerd/certs.d/$$registry && \
			printf '[host."https://%s"]\n  capabilities = ["pull", "resolve"]\n  override_path = true\n' $$root | \
				docker exec --interactive $$node tee /etc/containerd/certs.d/$$registry/hosts.toml > /dev/null || exit 1; \
		done; \
	done
	$(KUBECTL) create namespace aviary-data --dry-run=client --output yaml | $(KUBECTL) apply -f -

cluster-down:
	kind delete cluster --name $(CLUSTER)

# Installs Agent Substrate in the cluster with its installer: its control
# plane, with a PostgreSQL and an object storage of its own, and gVisor for
# the sandboxes. The Supervisors of aviary-system are its credential provider
# aviary.io, may call its router and own its atespaces. The nodes answer ARP
# for the addresses that they route, which gVisor needs on kind. A registry
# in the cluster holds the harness images.
substrate: $(SUBSTRATE_OUT)/ate-setup
	for node in $$(kind get nodes --name $(CLUSTER)); do \
		docker exec $$node sysctl -q -w net.ipv4.conf.all.proxy_arp=1 || exit 1; \
	done
	cd $(SUBSTRATE_OUT)/source && ../ate-setup deploy ate-system --kind --kind-cluster-name $(CLUSTER) --context kind-$(CLUSTER) \
		--image-repo ghcr.io/agent-substrate/substrate --image-tag $(SUBSTRATE) --sandbox-classes=gvisor \
		--credential-provider='{"name": "aviary.io", "address": "aviary-supervisor.aviary-system.svc:50051"}' \
		--ingress-allowed-spiffe-ids=spiffe://cluster.local/ns/ate-system/sa/atenet-client,spiffe://cluster.local/ns/aviary-system/sa/aviary-supervisor \
		--ateapi-authz-bootstrap-owners=system:serviceaccount:ate-system:ate-client,system:serviceaccount:aviary-system:aviary-supervisor \
		--rollout-timeout 10m
	$(KUBECTL) apply --filename hack/kind/registry.yaml
	$(KUBECTL) --namespace ate-system rollout status deployment/kind-registry --timeout 300s

# The next four targets install the data layer in the local cluster. Aviary
# needs one database and one event log, for example
# make cluster-up postgres valkey
postgres:
	$(call KEPT_SECRET,postgres-users)
	$(KUBECTL) get secret postgres-users --namespace aviary-data >/dev/null 2>&1 || \
		$(KUBECTL) create secret generic postgres-users --namespace aviary-data \
			--from-literal=aviary=$$(openssl rand -hex 24)
	$(call KEEP_SECRET,postgres-users,aviary-data)
	$(KUBECTL) apply -f hack/kind/postgres.yaml
	$(KUBECTL) --namespace aviary-data rollout status statefulset/postgres --timeout 300s

mysql:
	$(call KEPT_SECRET,mysql-users)
	$(KUBECTL) get secret mysql-users --namespace aviary-data >/dev/null 2>&1 || \
		$(KUBECTL) create secret generic mysql-users --namespace aviary-data \
			--from-literal=root=$$(openssl rand -hex 24) --from-literal=aviary=$$(openssl rand -hex 24)
	$(call KEEP_SECRET,mysql-users,aviary-data)
	$(KUBECTL) apply -f hack/kind/mysql.yaml
	$(KUBECTL) --namespace aviary-data rollout status statefulset/mysql --timeout 300s

valkey:
	$(KUBECTL) get secret valkey-users --namespace aviary-data >/dev/null 2>&1 || \
		$(KUBECTL) create secret generic valkey-users --namespace aviary-data \
			--from-literal=default=$$(openssl rand -hex 24)
	$(KUBECTL) apply -f hack/kind/valkey.yaml
	$(KUBECTL) --namespace aviary-data rollout status deployment/valkey --timeout 300s

redis:
	$(KUBECTL) get secret redis-users --namespace aviary-data >/dev/null 2>&1 || \
		$(KUBECTL) create secret generic redis-users --namespace aviary-data \
			--from-literal=default=$$(openssl rand -hex 24)
	$(KUBECTL) apply -f hack/kind/redis.yaml
	$(KUBECTL) --namespace aviary-data rollout status deployment/redis --timeout 300s

# The next two targets install a secret store in the local cluster, for
# trying the secret stores of projects. Aviary itself does not need one.
openbao:
	$(KUBECTL) get secret openbao-root --namespace aviary-data >/dev/null 2>&1 || \
		$(KUBECTL) create secret generic openbao-root --namespace aviary-data \
			--from-literal=token=$$(openssl rand -hex 24)
	$(KUBECTL) apply -f hack/kind/openbao.yaml
	$(KUBECTL) --namespace aviary-data rollout status deployment/openbao --timeout 180s

vault:
	$(KUBECTL) get secret vault-root --namespace aviary-data >/dev/null 2>&1 || \
		$(KUBECTL) create secret generic vault-root --namespace aviary-data \
			--from-literal=token=$$(openssl rand -hex 24)
	$(KUBECTL) apply -f hack/kind/vault.yaml
	$(KUBECTL) --namespace aviary-data rollout status deployment/vault --timeout 180s

# Installs the Versity S3 Gateway in the local cluster, for trying the depot
# on a bucket, and the Mountpoint for Amazon S3 CSI driver, which mounts the
# bucket of the gateway as a volume and signs its requests with the keys of
# the gateway. Aviary keeps its objects in the bucket when installed with
# make deploy VALUES=hack/kind/aviary-versitygw-values.yaml
versitygw: $(MOUNTPOINT_S3_CSI_CHART)
	$(KUBECTL) get secret versitygw-root --namespace aviary-data >/dev/null 2>&1 || \
		$(KUBECTL) create secret generic versitygw-root --namespace aviary-data \
			--from-literal=access=$$(openssl rand -hex 10) --from-literal=secret=$$(openssl rand -hex 24)
	$(KUBECTL) get secret versitygw-root --namespace aviary-data --output json | \
		jq '{apiVersion, kind, type, metadata: {name: "aws-secret"}, stringData: {key_id: (.data.access | @base64d), access_key: (.data.secret | @base64d)}}' | \
		$(KUBECTL) apply --namespace kube-system -f -
	$(HELM) upgrade --install aws-mountpoint-s3-csi-driver $(MOUNTPOINT_S3_CSI_CHART) \
		--namespace kube-system --wait
	$(KUBECTL) create namespace aviary-system --dry-run=client --output yaml | $(KUBECTL) apply -f -
	$(KUBECTL) apply -f hack/kind/versitygw.yaml
	$(KUBECTL) --namespace aviary-data rollout status statefulset/versitygw --timeout 300s

# Installs Keycloak in the local cluster, for trying Aviary with
# authentication. Aviary uses it when installed with
# make deploy VALUES=hack/kind/aviary-keycloak-values.yaml
keycloak:
	$(KUBECTL) apply -f hack/kind/keycloak.yaml
	$(KUBECTL) --namespace aviary-data rollout status deployment/keycloak --timeout 300s
	@echo "Keycloak is at http://localhost:8081"

# Installs Headlamp in the local cluster, a web UI for its resources, logs
# and events, at http://localhost:4466.
headlamp:
	$(KUBECTL) apply -f hack/kind/headlamp.yaml
	$(KUBECTL) --namespace headlamp rollout status deployment/headlamp --timeout 300s
	@echo "Headlamp is at http://localhost:4466"

# Installs Prometheus, which scrapes the metrics of Aviary, and Grafana with
# the dashboard in deploy/grafana, at http://localhost:3000.
grafana:
	$(KUBECTL) create namespace monitoring --dry-run=client --output yaml | $(KUBECTL) apply -f -
	$(KUBECTL) create configmap grafana-dashboards --namespace monitoring \
		--from-file deploy/grafana/aviary.json --dry-run=client --output yaml | \
		$(KUBECTL) apply --server-side -f -
	$(KUBECTL) apply -f hack/kind/prometheus.yaml -f hack/kind/grafana.yaml
	$(KUBECTL) --namespace monitoring rollout status deployment/prometheus --timeout 300s
	$(KUBECTL) --namespace monitoring rollout status deployment/grafana --timeout 300s
	@echo "Grafana is at http://localhost:3000"

# Builds the web UI and installs it in the local cluster, after make deploy,
# at http://localhost:8082. Its server passes the requests of /v1 on to the
# API. The image ID restarts its pod when the image changes.
web:
	$(BUILD) --file images/web/Dockerfile --tag $(WEB_IMAGE) .
	kind load docker-image --name $(CLUSTER) $(WEB_IMAGE)
	$(KUBECTL) apply --filename hack/kind/web.yaml
	image=$$(docker image inspect --format '{{.Id}}' $(WEB_IMAGE)) && \
	$(KUBECTL) --namespace aviary-system patch deployment aviary-web --type merge \
		--patch '{"spec": {"template": {"metadata": {"annotations": {"aviary.io/image-id": "'$$image'"}}}}}'
	$(KUBECTL) --namespace aviary-system rollout status deployment/aviary-web --timeout 120s
	@echo "The web UI is at http://localhost:8082"

# Serves the console at http://localhost:5173 from its source in web/. A
# change to the source shows in the browser when the file is saved. The
# console is answered from example data, in the browser, unless API names an
# API for it to use, for example that of the local cluster:
# make web-dev API=http://localhost:8080
# If the API authenticates its callers, the environment names the OpenID
# Connect issuer and the client that the console signs them in with, for
# example those of make keycloak:
# AVIARY_OIDC_ISSUER=http://keycloak.localhost:8081/realms/aviary \
# AVIARY_OIDC_CLIENT_ID=aviary make web-dev API=http://localhost:8080
web-dev: web/node_modules/.package-lock.json
	cd web && $(if $(API),AVIARY_API=$(API) )npm run dev

# Builds the documentation into docs/dist, as a site of static files. The
# build fails if a link between its pages leads nowhere. Astro, which builds
# the site, reports nothing of its use.
docs: docs/node_modules/.package-lock.json
	cd docs && ASTRO_TELEMETRY_DISABLED=1 npm run build

# Serves the documentation at http://localhost:4321 from its source in
# docs/. A change to a page shows in the browser when its file is saved.
docs-dev: docs/node_modules/.package-lock.json
	cd docs && ASTRO_TELEMETRY_DISABLED=1 npm run dev

build:
	$(BUILD) --file images/supervisor/Dockerfile --tag $(SUPERVISOR_IMAGE) .
	$(BUILD) --file images/runner/Dockerfile --tag $(RUNNER_IMAGE) .
	$(BUILD) --build-arg RUNNER_IMAGE=$(RUNNER_IMAGE) --tag $(HARNESS_BASE_IMAGE) images/harness-base
	$(BUILD) --tag $(OPENCODE_IMAGE) images/harness-opencode
	$(BUILD) --tag $(CODEX_IMAGE) images/harness-codex
	$(BUILD) --tag $(CLAUDE_CODE_IMAGE) images/harness-claude-code
	$(BUILD) --file images/mock-llm/Dockerfile --tag $(MOCK_LLM_IMAGE) .

# Loads the images into the nodes of the cluster, and puts the harness images
# into its registry, from a node, as Substrate takes them: by digest, at
# localhost:5001. HARNESS_VALUES names them for the chart.
images: build
	kind load docker-image --name $(CLUSTER) $(SUPERVISOR_IMAGE) $(MOCK_LLM_IMAGE)
	kind load docker-image --name $(CLUSTER) --nodes $(CLUSTER)-control-plane $(OPENCODE_IMAGE) $(CODEX_IMAGE) $(CLAUDE_CODE_IMAGE)
	mkdir -p .out
	registry=$$($(KUBECTL) --namespace ate-system get service kind-registry --output jsonpath='{.spec.clusterIP}'):5000 && \
	ctr="docker exec $(CLUSTER)-control-plane ctr --namespace k8s.io" && \
	printf 'runtime:\n  harnesses:\n' > $(HARNESS_VALUES).new && \
	for harness in opencode codex claude-code; do \
		image=aviary-harness-$$harness:dev; \
		$$ctr images tag --force docker.io/library/$$image $$registry/$$image > /dev/null && \
		$$ctr images push --plain-http $$registry/$$image > /dev/null && \
		digest=$$($$ctr images ls name==$$registry/$$image | awk 'NR == 2 { print $$3 }') && \
		[ -n "$$digest" ] && \
		printf '    %s:\n      image: localhost:5001/aviary-harness-%s@%s\n' $$harness $$harness $$digest >> $(HARNESS_VALUES).new || exit 1; \
	done
	mv $(HARNESS_VALUES).new $(HARNESS_VALUES)

# Creates the Secrets that the chart reads, and fails unless the cluster has
# one database and one event log. Their URLs are built from their passwords.
# The encryption key is created once: secrets encrypted with it cannot be
# decrypted with another.
secrets:
	@installed() { $(KUBECTL) get secret "$$@" --namespace aviary-data --ignore-not-found --output name | wc -l; }; \
	databases=$$(installed postgres-users mysql-users) && [ "$$databases" -eq 1 ] || \
		{ echo "The cluster has $$databases databases, and Aviary needs one: make postgres or make mysql installs one." >&2; exit 1; }; \
	logs=$$(installed valkey-users redis-users) && [ "$$logs" -eq 1 ] || \
		{ echo "The cluster has $$logs event logs, and Aviary needs one: make valkey or make redis installs one." >&2; exit 1; }
	$(KUBECTL) create namespace aviary-system --dry-run=client --output yaml | $(KUBECTL) apply -f -
	$(KUBECTL) label namespace aviary-system ate.dev/api-access=true --overwrite
	if $(KUBECTL) get secret mysql-users --namespace aviary-data >/dev/null 2>&1; then \
		$(KUBECTL) get secret mysql-users --namespace aviary-data --output json | \
			jq '{apiVersion, kind, type, metadata: {name: "aviary-database"}, stringData: {uri: ("mysql://aviary:" + (.data.aviary | @base64d) + "@mysql.aviary-data:3306/aviary")}}'; \
	else \
		$(KUBECTL) get secret postgres-users --namespace aviary-data --output json | \
			jq '{apiVersion, kind, type, metadata: {name: "aviary-database"}, stringData: {uri: ("postgresql://aviary:" + (.data.aviary | @base64d) + "@postgres.aviary-data:5432/aviary")}}'; \
	fi | $(KUBECTL) apply --namespace aviary-system -f -
	if $(KUBECTL) get secret redis-users --namespace aviary-data >/dev/null 2>&1; then \
		$(KUBECTL) get secret redis-users --namespace aviary-data --output json | \
			jq '{apiVersion, kind, type, metadata: {name: "aviary-events"}, stringData: {uri: ("redis://default:" + (.data.default | @base64d) + "@redis.aviary-data:6379")}}'; \
	else \
		$(KUBECTL) get secret valkey-users --namespace aviary-data --output json | \
			jq '{apiVersion, kind, type, metadata: {name: "aviary-events"}, stringData: {uri: ("valkey://default:" + (.data.default | @base64d) + "@valkey.aviary-data:6379")}}'; \
	fi | $(KUBECTL) apply --namespace aviary-system -f -
	$(call KEPT_SECRET,aviary-encryption)
	$(KUBECTL) get secret aviary-encryption --namespace aviary-system >/dev/null 2>&1 || \
		$(KUBECTL) create secret generic aviary-encryption --namespace aviary-system \
			--from-literal=k1=$$(openssl rand -base64 32)
	$(call KEEP_SECRET,aviary-encryption,aviary-system)

# Installs Aviary in the cluster, after make substrate and make images. The
# image ID restarts the pods that run the Supervisor image when the image
# changes. A model provider at localhost is a model server on the machine
# that runs the cluster: the name host.aviary-system.svc.cluster.local, which
# hack/kind/aviary-values.yaml gives the sandboxes for it, has the address
# that Docker gives containers for their host. VALUES adds values files, for
# example
# make deploy VALUES=my-values.yaml
deploy: secrets
	host=$$(docker run --rm --add-host host:host-gateway --entrypoint getent $(NODE_IMAGE) ahostsv4 host | head -n 1 | cut -d ' ' -f 1) && \
	[ -n "$$host" ] && \
	printf 'apiVersion: v1\nkind: Service\nmetadata: {name: host, namespace: aviary-system}\nspec: {clusterIP: None}\n---\napiVersion: discovery.k8s.io/v1\nkind: EndpointSlice\nmetadata:\n  name: host\n  namespace: aviary-system\n  labels: {kubernetes.io/service-name: host}\naddressType: IPv4\nendpoints:\n  - addresses: ["%s"]\n' "$$host" | \
		$(KUBECTL) apply -f -
	image=$$(docker image inspect --format '{{.Id}}' $(SUPERVISOR_IMAGE)) && \
	$(HELM) upgrade --install aviary deploy/charts/aviary \
		--namespace aviary-system \
		--values hack/kind/aviary-values.yaml --values $(HARNESS_VALUES) \
		--set-string 'supervisor.podAnnotations.aviary\.io/image-id='$$image \
		--set-string 'stateMetrics.podAnnotations.aviary\.io/image-id='$$image \
		--set-string 'depot.podAnnotations.aviary\.io/image-id='$$image \
		$(addprefix --values ,$(VALUES)) \
		--wait
	$(KUBECTL) apply --filename hack/kind/api.yaml
	@echo "The API is at http://localhost:8080"

# Runs the e2e in a new cluster of its own, which is deleted if the tests
# pass. After a failure the cluster stays, with its logs and kubeconfig in
# .out/e2e/<cluster>:
# KUBECONFIG=.out/e2e/aviary-e2e/host.kubeconfig kubectl get pods --all-namespaces
# make e2e-run reruns the tests, and make e2e-down deletes the cluster. The
# cluster has PostgreSQL and Valkey, unless the targets before e2e make it,
# for example with MySQL, Redis and Keycloak:
# make cluster-up mysql redis keycloak e2e E2E_CLUSTER=aviary-e2e-mysql
e2e: e2e-up
	$(MAKE) e2e-run
	$(MAKE) e2e-down

ifneq ($(E2E),)
# The e2e starts from a new cluster.
cluster-up: | cluster-down
endif

# Installs Aviary in the cluster of the e2e as the tests need it, with the
# mock model server and OpenBao, and with Keycloak if the cluster has it.
e2e-up:
ifeq ($(filter cluster-up,$(MAKECMDGOALS)),)
	$(MAKE) cluster-down cluster-up postgres valkey CLUSTER=$(CLUSTER) KIND_CONFIG=$(KIND_CONFIG) DATA=
endif
	$(MAKE) substrate openbao images secrets CLUSTER=$(CLUSTER) DATA=
	$(KUBECTL) apply --filename test/e2e/mock-llm.yaml --filename hack/kind/api.yaml
	keycloak=$$($(KUBECTL) get deployment keycloak --namespace aviary-data --ignore-not-found --output name) && \
	$(HELM) upgrade --install aviary deploy/charts/aviary \
		--namespace aviary-system --values test/e2e/values.yaml --values $(HARNESS_VALUES) \
		$${keycloak:+--values hack/kind/aviary-keycloak-values.yaml} --wait
	$(KUBECTL) --namespace aviary-e2e rollout status deployment/mock-llm --timeout 120s

# The tests run on the kind network and reach the API at the node port that
# hack/kind/api.yaml opens. They sign in if the cluster has Keycloak.
e2e-run:
	mkdir -p $(E2E_OUT)
	kind get kubeconfig --internal --name $(CLUSTER) > $(E2E_OUT)/kubeconfig
	keycloak=$$($(KUBECTL) get deployment keycloak --namespace aviary-data --ignore-not-found --output name) && \
	$(call GO,test,--network kind --env AVIARY_E2E_KUBECONFIG=/src/$(E2E_OUT)/kubeconfig \
		--env AVIARY_E2E_API=http://$(CLUSTER)-control-plane:30080 \
		--env AVIARY_E2E_IDENTITY=$${keycloak:+keycloak} \
		--env AVIARY_E2E_HARNESSES="$(E2E_HARNESSES)") test -count=1 -timeout 20m -v ./e2e/... $(TESTS); \
		status=$$?; [ $$status = 0 ] || { \
			for part in supervisor state-metrics depot; do \
				$(KUBECTL) --namespace aviary-system logs --selector app.kubernetes.io/component=$$part \
					--prefix --tail -1 > $(E2E_OUT)/$$part.log 2>&1; \
			done; \
			for part in ate-api-server atenet-router atenet-egress; do \
				$(KUBECTL) --namespace ate-system logs deployment/$$part --all-containers --prefix --tail -1 \
					> $(E2E_OUT)/$$part.log 2>&1; \
			done; \
			$(KUBECTL) --namespace ate-system logs daemonset/atelet --all-containers --prefix --tail -1 > $(E2E_OUT)/atelet.log 2>&1; \
			$(KUBECTL) --namespace aviary-runtime logs --selector ate.dev/worker-pool --prefix --tail -1 > $(E2E_OUT)/workers.log 2>&1; \
			$(KUBECTL) get pods,workerpools.ate.dev --all-namespaces --output wide > $(E2E_OUT)/pods.txt 2>&1; \
			$(KUBECTL) get events --all-namespaces --sort-by .lastTimestamp > $(E2E_OUT)/events.txt 2>&1; \
			echo "e2e failed: the cluster $(CLUSTER) is kept, and its logs are in $(E2E_OUT)"; \
		}; exit $$status

e2e-down: cluster-down
	rm -f $(E2E_KUBECONFIG) $(E2E_OUT)/kubeconfig
