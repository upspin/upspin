#!/bin/bash -e

# Create a temporary config so the defaults point to example.com.
mkdir /tmp/gendoc$$

trap "rm -rf /tmp/gendoc$$" 0

upspin keygen /tmp/gendoc$$ > /dev/null 2>&1

cat >/tmp/gendoc$$/config <<!
username: ann@example.com
packing: ee

keyserver: remote,key.example.com:443
dirserver: remote,dir.example.com:443
storeserver: remote,store.example.com:443
secrets: /tmp/gendoc$$
!

go build -tags gendoc -o upspin.gendoc
./upspin.gendoc -config /tmp/gendoc$$/config gendoc
rm upspin.gendoc
