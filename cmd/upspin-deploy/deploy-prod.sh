#!/bin/bash

go install && upspin-deploy -project=upspin-prod -domain=upspin.io -zone=us-central1-c -keyserver= $@
