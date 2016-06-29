#!/bin/bash -e
# Copyright 2016 The Upspin Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.
#
# This script builds server images and deploys them to GCP sequentially.
#
# Usage:
#
# ./deploy-servers.sh [userserver|dirserver|storeserver|frontend] [-d] [-b] [-p]
#
# If a server name is not given, all are rebuilt and redeployed.
# -d deploy only -- does not rebuild servers.
# -b build only -- does not deploy servers.
# -r restarts only -- does not build nor deploy servers.
# -p deploy to project upspin-prod.
#
# The PROJECT environment variable specifies the Google Cloud project to use.
# Its default is "upspin-test".

root=""
deployonly=0
buildonly=0
restartonly=0
project=${PROJECT:-upspin-test}
default_serverlist=(userserver dirserver storeserver frontend)

# Builds the named binary statically.
function build {
    server=$1
    echo "=== Building $server and pushing image to $project ..."

    if [ ! -x "$(which cdbuild)" ]; then
        echo "The cdbuild tool must be installed for this to work."
        echo "Try: go get github.com/broady/cdbuild"
        exit 1
    fi

    if [ ! -d "$TMPDIR" ]; then
        TMPDIR=/tmp
    fi

    dir=$TMPDIR/$USER-upspin-$server
    if [ -d "$dir" ]; then
        rm -r "$dir"
    fi
    mkdir "$dir"

    envfiles=""
    case $server in
        dirserver)
            envfiles="
                dirserver/rc.dirserver
                dirserver/public.upspinkey
                dirserver/secret.upspinkey
                dirserver/config.gcp
                serviceaccountkey.json
            "
            ;;
        storeserver)
            envfiles="
                serviceaccountkey.json
            "
            ;;
    esac
    for f in $envfiles; do
       src="$HOME/upspin/deploy/$project/$f"
       if [ ! -f "$src" ]; then
           echo "Couldn't find file for $server in $src"
           exit 1
       fi
       dst="$dir/$(basename $f)"
       cp "$src" "$dst"
    done

    pushd "$root/cmd/$server" >/dev/null
    sed 's/PROJECT/'"$project"'/g' Dockerfile > "$dir"/Dockerfile
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -v -o "$dir/$server"
    popd >/dev/null

    pushd "$dir" >/dev/null
    cdbuild -project "$project" -name "$server"
    popd >/dev/null

    rm -r "$dir"
}

# Deploys the named service to the cluster and restart it.
function deploy {
    server="$1"
    echo "=== Deploying $server to $project ..."

    ipfile="$HOME/upspin/deploy/$project/ip/$server"
    if [ ! -f "$ipfile" ]; then
	    echo "Couldn't find ip file for $server in $ipfile"
	    exit 1
    fi
    ip="$(cat $ipfile)"

    sed 's/PROJECT/'"$project"'/g' "$root/cmd/admin/deployment/${server}.yaml" | kubectl apply -f -
    sed 's/PROJECT/'"$project"'/g' "$root/cmd/admin/service/${server}.yaml" | sed 's/IPADDR/'"$ip"'/g' | kubectl apply -f -
    restart "$1"
}

# Restarts a service in the cluster.
function restart {
    server="$1"
    echo "=== Restarting $server in $project ..."
    kubectl delete pods -l app="$server"
}

# Finds the root of project upspin by looking in the current directory and in $GOPATH and puts it in $root
function find_root {
    local test_dir="$GOPATH/src/upspin.io"
    if [ -f "$test_dir/upspin/upspin.go" ]; then
        root=$test_dir
    elif [ -f "../../upspin/upspin.go" ]; then
        pushd ../..
        root=$(pwd)
        popd
    else
        root="not found"
    fi
}

function main {
    local serverlist=()
    while [[ "$#" -gt 0 ]]; do
        local key="$1"

        case $key in
            -d|--deploy-only)
            deployonly=1
            ;;
            -b|--build-only)
            buildonly=1
            ;;
            -r|--restart-only)
            restartonly=1
            ;;
            -p|--prod)
            project=upspin-prod
            ;;
            storeserver)
            serverlist[${#serverlist[*]}]="storeserver"
            ;;
            dirserver)
            serverlist[${#serverlist[*]}]="dirserver"
            ;;
            userserver)
            serverlist[${#serverlist[*]}]="userserver"
            ;;
            frontend)
            serverlist[${#serverlist[*]}]="frontend"
            ;;
            *)
            echo "Error parsing option $key"
            exit
            ;;
        esac
        shift
    done

    find_root
    echo "Root of Upspin: $root"

    if [[ ${#serverlist[@]} == 0 ]]; then
        serverlist=("${default_serverlist[@]}")
    fi

    if [[ $deployonly -gt 0 && $buildonly -gt 0 ]]; then
        echo "Nothing to do."
        exit 1
    fi

    if [[ $restartonly -gt 0 && ($buildonly -gt 0 || $deployonly -gt 0) ]]; then
        echo "Invalid combination of options"
        exit 1
    fi

    echo "Going to work the following servers: ${serverlist[@]}"

    gcloud config set project "$project"
    # TODO(adg): make the zone configurable
    gcloud config set compute/zone us-central1-c
    gcloud container clusters get-credentials cluster-1

    for server in "${serverlist[@]}"; do
        if [ $restartonly == 1 ]; then
            restart $server
            continue
        fi
        if [ $deployonly == 0 ]; then
            build $server
        fi
        if [ $buildonly == 0 ]; then
            deploy $server
        fi
    done
}

main "$@"
