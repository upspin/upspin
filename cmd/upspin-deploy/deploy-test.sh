#!/bin/bash

go install && upspin-deploy -project=upspin-test -domain=test.upspin.io -zone=us-central1-c -keyserver="" $@
