# Turns the manifests that kustomize renders into a template of the chart:
#
#	AVIARY_VALUE_<name>     the template substrate.<name>
#	AVIARY_INCLUDE_<name>   a line of the list or map that the template
#	                        substrate.<name> renders, at its indentation
#	ko://go.ate.dev/substrate/cmd/<image>, ${ENVOY_DATAPLANE_IMAGE}
#	                        the image of the release
#	gs://gvisor/<path>      <path> under substrate.gvisor.location
#
# and adds to the configuration of the egress gateway what _attribution.tpl
# defines: its filters before the router of the CONNECT leg, which comes after
# the leg's ext_proc of stat_prefix egress_identity, its filter of the legs
# that read requests in place of the #ATE_MITM_EXTPROC_FILTER markers, its
# cluster in place of the #ATE_MITM_EXTPROC_CLUSTER marker, its fields after
# the actor of each JSON line of the access log, those of a request in the
# lines of the legs mitm and cleartext, and the share of the requests that the
# log writes in place of the 1% of the release. It fails if the configuration
# does not have each of these places as often as the release has them.
#
# Braces in the manifests stay text.
BEGIN {
	ko = "ko://go.ate.dev/substrate/cmd/"
}

# include prints a line that puts the template substrate.<name> at the
# indentation of line.
function include(line, name,    indent) {
	indent = match(line, /[^ ]/) - 1
	printf "%s{{- include \"substrate.%s\" . | trim | nindent %d }}\n", substr(line, 1, indent), name, indent
}

{
	line = $0
	gsub(/[{][{]/, "@@open@@", line)
	gsub(/[}][}]/, "@@close@@", line)
	gsub(/@@open@@/, "{{ \"{{\" }}", line)
	gsub(/@@close@@/, "{{ \"}}\" }}", line)

	if (match(line, /AVIARY_INCLUDE_[A-Za-z]+/)) {
		name = substr(line, RSTART + length("AVIARY_INCLUDE_"), RLENGTH - length("AVIARY_INCLUDE_"))
		indent = match(line, /[^ ]/) - 1
		printf "%s{{- include \"substrate.%s\" . | nindent %d }}\n", substr(line, 1, indent), name, indent
		next
	}
	while (match(line, /AVIARY_VALUE_[A-Za-z]+/)) {
		name = substr(line, RSTART + length("AVIARY_VALUE_"), RLENGTH - length("AVIARY_VALUE_"))
		line = substr(line, 1, RSTART - 1) "{{ include \"substrate." name "\" . }}" substr(line, RSTART + RLENGTH)
	}
	while (match(line, /ko:\/\/go\.ate\.dev\/substrate\/cmd\/[a-z-]+/)) {
		name = substr(line, RSTART + length(ko), RLENGTH - length(ko))
		line = substr(line, 1, RSTART - 1) "{{ include \"substrate.image\" (list $ \"" name "\") }}" substr(line, RSTART + RLENGTH)
	}
	gsub(/[$][{]ENVOY_DATAPLANE_IMAGE[}]/, "{{ include \"substrate.image\" (list $ \"envoy-dataplane\") }}", line)
	gsub(/gs:\/\/gvisor\//, "{{ include \"substrate.gvisor\" . }}/", line)

	if (line ~ /stat_prefix: egress_identity$/) {
		connect = 1
	}
	if (connect == 1 && line ~ /^ *- name: envoy\.filters\.http\.router$/) {
		include(line, "attributionFilters")
		connect = 2
		filters++
	}
	if (line ~ /^ *#ATE_MITM_EXTPROC_FILTER/) {
		include(line, "attributionProcessor")
		processors++
		next
	}
	if (line ~ /^ *#ATE_MITM_EXTPROC_CLUSTER/) {
		include(line, "attributionCluster")
		clusters++
		next
	}
	samples += gsub(/percent_sampled: [{]numerator: 1, denominator: HUNDRED[}]/,
		"percent_sampled: {numerator: {{ include \"substrate.requestLogPercent\" . }}, denominator: HUNDRED}", line)
	if (line ~ /^ *leg: /) {
		reads = line ~ /^ *leg: (mitm|cleartext)$/
	}
	print line
	if (line ~ /^ *actor: "%FILTER_STATE\(dev\.ate\.actor\.identity:PLAIN\)%"$/) {
		if (reads) {
			include(line, "attributionRequestFields")
			requests++
		} else {
			include(line, "attributionFields")
			fields++
		}
	}
}

END {
	if (filters != 1 || processors != 2 || clusters != 1 || fields != 2 || requests != 2 || samples != 2) {
		printf "helm.awk: the egress gateway has %d CONNECT routers, %d filter markers, %d cluster markers, %d actors in the log of connections, %d in that of requests and %d sampled logs, want 1, 2, 1, 2, 2 and 2\n",
			filters, processors, clusters, fields, requests, samples > "/dev/stderr"
		exit 1
	}
}
