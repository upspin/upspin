#!/bin/bash -e

CERTS=roots.crt
if [ ! -f $CERTS ]; then
	/usr/bin/security find-certificate -a -p /System/Library/Keychains/SystemRootCertificates.keychain > $CERTS
fi

# Make image and upload it to Google Container Registry.
GOOS=linux GOARCH=amd64 go install std
GOOS=linux GOARCH=amd64 go build -o frontend
cdbuild -project upspin-test -name frontend
rm frontend

# Refresh production instances.
kubectl apply -f deployment.yaml
kubectl apply -f service.yaml
