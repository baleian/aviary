#!/usr/bin/env bash
# Makes the secrets that OpenSandbox in the local cluster is installed with:
# the key of its API, the key that its server signs the routes of sandboxes
# with, the certificate authority that its egress answers HTTPS with, and the
# keys of the object store in the shapes that its parts read. `make cluster-up`
# runs it after the namespaces are there. A key that was made is kept.
#   opensandbox-keys.sh <kubectl with its context>
set -euo pipefail

kubectl=("$@")
system=opensandbox-system
dataplane=opensandbox-dataplane
store=rustfs.aviary-data.svc.cluster.local:9000

has() { # namespace secret
	"${kubectl[@]}" get secret "$2" --namespace "$1" >/dev/null 2>&1
}
# Makes the secret, or makes it again from what it is now to hold.
put() { # namespace kind name arguments...
	"${kubectl[@]}" create "${@:2}" --namespace "$1" --dry-run=client --output yaml |
		"${kubectl[@]}" apply -f - >/dev/null
}

has "$system" opensandbox-api-key ||
	put "$system" secret generic opensandbox-api-key --from-literal=api-key="$(openssl rand -hex 24)"
has "$system" opensandbox-route-keys ||
	put "$system" secret generic opensandbox-route-keys \
		--from-literal=keys="a=$(openssl rand -base64 32)" --from-literal=active-key=a

# A template carries the certificate, and the egress of a pool signs with the
# key.
if ! has "$dataplane" opensandbox-mitm-ca; then
	work="$(mktemp -d)"
	trap 'rm -r "$work"' EXIT
	openssl req -x509 -new -newkey rsa:2048 -nodes -days 3650 -subj "/CN=OpenSandbox egress of a local cluster" \
		-addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign,cRLSign \
		-keyout "$work/tls.key" -out "$work/tls.crt" 2>/dev/null
	put "$dataplane" secret generic opensandbox-mitm-ca \
		--from-file=tls.crt="$work/tls.crt" --from-file=tls.key="$work/tls.key" --from-file=ca.crt="$work/tls.crt"
fi

key() { # name
	"${kubectl[@]}" get secret rustfs-keys --namespace aviary-data --output "jsonpath={.data.$1}" | base64 --decode
}
access="$(key access)"
secret="$(key secret)"
# The server reads this one when it has a template built.
put "$dataplane" secret generic sandbox-oss-credentials \
	--from-literal=accessKeyId="$access" --from-literal=secretAccessKey="$secret" \
	--from-literal=endpoint="http://$store" --from-literal=region=us-east-1
# The pods of a pool read this one, which the config map fast-sandbox-registry
# names.
put "$dataplane" secret docker-registry fast-sandbox-registry \
	--docker-server="$store" --docker-username="$access" --docker-password="$secret"
# The agent of a node reads this one, and checks the digest that it carries:
# that of the list as Go writes it, its fields in this order and no spaces.
list="[{\"host\":\"$store\",\"username\":\"$access\",\"password\":\"$secret\",\"writeUsername\":\"$access\",\"writePassword\":\"$secret\",\"endpoint\":\"http://$store\"}]"
put "$system" secret generic fast-sandbox-agent-registry \
	--from-literal=registry.json="{\"revision\":\"sha256:$(printf '%s' "$list" | sha256sum | cut -d' ' -f1)\",\"credentials\":$list}"
