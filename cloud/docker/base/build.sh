#!/bin/bash -e

CERTS=roots.crt
if [ ! -f $CERTS ]; then
	/usr/bin/security find-certificate -a -p /System/Library/Keychains/SystemRootCertificates.keychain > $CERTS
fi

cdbuild -project upspin-test -name base
