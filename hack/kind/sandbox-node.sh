#!/usr/bin/env bash
# Gives a node of the local cluster the folder where the sandboxes of the node
# keep their disks. Fast Sandbox copies a disk without copying its blocks, so
# it takes only XFS with reflink: the file system is a file of the node,
# mounted in the node, and nothing of the host changes. `make cluster-up` runs
# this inside each node, and a node that has the folder is left as it is.
set -euo pipefail

folder=/var/lib/fast-sandbox
image=/var/fast-sandbox.img

if [[ ! -e $image ]]; then
	command -v mkfs.xfs >/dev/null || {
		apt-get update -qq
		DEBIAN_FRONTEND=noninteractive apt-get install -y -qq xfsprogs >/dev/null
	}
	# The file takes what is written to it, and no more. A template is some
	# gigabytes on every node that runs it, and every sandbox its own disk.
	truncate --size 200G "$image.new"
	mkfs.xfs -q -m reflink=1 "$image.new"
	mv "$image.new" "$image"
	mkdir -p "$folder"
	echo "$image $folder xfs loop,noatime 0 0" >> /etc/fstab
fi
# A node has the loop devices that the host had when the node started, and
# the next free one may be newer than that.
for number in $(seq 0 63); do
	[[ -b /dev/loop$number ]] || mknod "/dev/loop$number" b 7 "$number"
done
findmnt "$folder" >/dev/null || mount "$folder"
