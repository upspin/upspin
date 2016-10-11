#!/bin/bash

go install && upspin-deploy -project=upspin-test -domain=test.upspin.io -keyserver -frontend
