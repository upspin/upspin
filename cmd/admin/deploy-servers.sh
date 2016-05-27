#!/bin/bash -e
# Copyright 2016 The Upspin Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.
#
# This script builds server binaries and deploys them to GCP sequentially.
# Ensure you have proper SSH keys to login to upspin.io.
#
# Usage:
#
# ./deploy-servers.sh [userserver|dirserver|storeserver|frontend] [-d] [-b] [-t]
#
# If a server name is not given, all are rebuilt and redeployed.
# -d deploy only -- does not rebuild servers.
# -b build only -- does not deploy servers.
# -r restarts only -- does not build nor deploy servers.
# -p use production cluster (default is testing).

# TODO(adg): requires cdbuild command right now
# TODO(adg): check kubectl auth

errors=()
root=""
deployonly=0
buildonly=0
restartonly=0
project="upspin-test"
default_serverlist=(userserver dirserver storeserver frontend)

# Builds the named binary statically.
function build {
    server=$1
    echo "=== Building $server and pushing image to $project ..."

    dir=$TMPDIR/$USER-upspin-$server
    mkdir $dir

    pushd "$root/cmd/$server" >/dev/null
    if [ -d env ]; then
	    cp -R env/* $dir/
    fi
    sed 's/PROJECT/'$project'/g' Dockerfile > $dir/Dockerfile
    runsafely env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -v -o "$dir/$server"
    popd >/dev/null

    pushd $dir >/dev/null
    runsafely cdbuild -project $project -name $server
    popd >/dev/null

    rm -r $dir
}

# Deploys the named service to the cluster and restart it.
function deploy {
    server=$1
    echo "=== Deploying $server to $project ..."
    sed 's/PROJECT/'$project'/g' $root/cmd/admin/deployment/${server}.yaml | kubectl apply -f -
    sed 's/PROJECT/'$project'/g' $root/cmd/admin/service/${server}.yaml | kubectl apply -f -
    restart $1
}

# Restarts a service in the cluster.
function restart {
    server=$1
    echo "=== Restarting $server in $project ..."
    kubectl delete pods -l app=$server
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

# Runs the command and captures it in case of errors.
function runsafely {
    "$@"
    local status=$?
    if [ $status -ne 0 ]; then
        local msg="=== error with $*"
        errors[${#errors[*]}]="$msg"
        echo "$msg" >&2
    fi
    return $status
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
	    project="upspin-prod"
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
        exit
    fi

    if [[ $restartonly -gt 0 && ($buildonly -gt 0 || $deployonly -gt 0) ]]; then
        echo "Invalid combination of options"
        exit
    fi

    echo "Going to work the following servers: ${serverlist[@]}"

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

    if [[ ${#errors[@]} == 0 ]]; then
        echo "Success"
    else
        echo "${#errors[@]} errors found:"
        for ((i = 0; i < ${#errors[@]}; i++)); do
            echo "${errors[$i]}"
        done
    fi
}

main "$@"
