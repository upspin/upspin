#!/bin/bash -e

export PROJECT=upspin-prod
if [ "$1" != "-prod" ] && [ "$1" != "--prod" ]; then
	echo "NOTE: deploying to test cluster; use --prod to deploy to production"
	PROJECT=upspin-test
fi

# Make image and upload it to Google Container Registry.
./build.sh

# Refresh production configuration.
sed 's/PROJECT/'$PROJECT'/' deployment.yaml | kubectl apply -f -
kubectl apply -f service.yaml
# Kill any running frontend pods (they'll be re-created).
kubectl delete pods -l app=frontend

