#!/bin/bash -e

# This script builds and uploads the base Docker image, from which all our
# other Docker images are derived. This only needs to be done once per cloud
# project.

project="$1"
if [ "$1" == "" ]; then
	echo "You must specify the cloud project as the first argument"
	exit 1
fi

certfile=ca-certificates.crt
if [ ! -f $certfile ]; then
	local=/etc/ssl/certs/ca-certificates.crt
	if [ -f $local ]; then
		# Use the locally available file (present on modern Unixes).
		cp $local $certfile
	else
		# This generates the file under macOS.
		/usr/bin/security find-certificate -a -p /System/Library/Keychains/SystemRootCertificates.keychain > $certfile
	fi
fi

cdbuild -project $project -name base
