#!/bin/bash -e

go build -tags gendoc -o upspin.gendoc
./upspin.gendoc gendoc
rm upspin.gendoc
