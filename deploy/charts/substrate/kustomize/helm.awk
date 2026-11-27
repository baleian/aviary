# Turns the manifests that kustomize renders into a template of the chart:
#
#	AVIARY_VALUE_<name>     the template substrate.<name>
#	AVIARY_INCLUDE_<name>   a line of the list or map that the template
#	                        substrate.<name> renders, at its indentation
#	ko://go.ate.dev/substrate/cmd/<image>, ${ENVOY_DATAPLANE_IMAGE}
#	                        the image of the release
#	gs://gvisor/<path>      <path> under substrate.gvisor.location
#
# Braces in the manifests stay text.
BEGIN {
	ko = "ko://go.ate.dev/substrate/cmd/"
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
	print line
}
