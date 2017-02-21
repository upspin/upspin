#!/bin/bash

# This script deploys the Upspin servers running under test.upspin.io.

go install && upspin-deploy -project=upspin-test -domain=test.upspin.io -zone=us-central1-c -keyserver="" "$@"
