#!/bin/bash -e

project=upspin-prod
if [ "$1" != "-prod" ] && [ "$1" != "--prod" ]; then
	echo "NOTE: deploying to test cluster; use --prod to deploy to production"
	project=upspin-test
fi

# Make image and upload it to Google Container Registry.
T=$TMPDIR/$USER-upspin-frontend
mkdir $T
sed 's/PROJECT/'$project'/' Dockerfile > $T/Dockerfile

GOOS=linux GOARCH=amd64 go install std
GOOS=linux GOARCH=amd64 go build -o $T/frontend
(
	set -e
	cd $T
	cdbuild -project $project -name frontend
)
rm -r $T

# Refresh production configuration.
sed 's/PROJECT/'$project'/' deployment.yaml | kubectl apply -f -
kubectl apply -f service.yaml
# Kill any running frontend pods (they'll be re-created).
kubectl delete pods -l app=frontend

