#!/bin/bash

go install && upspin-deploy -project=upspin-prod -domain=upspin.io -keyserver -frontend
