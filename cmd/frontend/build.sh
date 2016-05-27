#!/bin/bash -e

if [ -z "$PROJECT" ]; then
	echo '$PROJECT must be specified' >&2
	exit 1
fi

# Make image and upload it to Google Container Registry.
T=$TMPDIR/$USER-upspin-frontend
mkdir $T
sed 's/PROJECT/'$PROJECT'/' Dockerfile > $T/Dockerfile

GOOS=linux GOARCH=amd64 go install std
GOOS=linux GOARCH=amd64 go build -o $T/frontend
(
	set -e
	cd $T
	cdbuild -project $PROJECT -name frontend
)
rm -r $T
