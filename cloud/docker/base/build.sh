#!/bin/bash -e

project=upspin-prod
if [ "$1" != "-prod" ] && [ "$1" != "--prod" ]; then
	echo "NOTE: deploying to test cluster; use --prod to deploy to production"
	project=upspin-test
fi

certfile=ca-certificates.crt
if [ ! -f $certfile ]; then
	local=/etc/ssl/certs/ca-certificates.crt
	if [ -f $local ]; then
		# Use the locally available file.
		cp $local $certfile
	else
		# This generates the file under macOS.
		/usr/bin/security find-certificate -a -p /System/Library/Keychains/SystemRootCertificates.keychain > $certfile
	fi
fi

cdbuild -project $project -name base
