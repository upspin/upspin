#!/bin/bash
#
# This script builds server binaries and deploys them to GCP sequentially.
# Ensure you have proper SSH keys to login to upspin.io.
#
# Usage:
#
# ./deploy-servers.sh [user|directory|store|frontend] [-d] [-b] [-t]
#
# If a server name is not given, all are rebuilt and redeployed.
# -d deploy only -- does not rebuild servers.
# -b build only -- does not deploy servers.
# -r restarts only -- does not build nor deploy servers.
# -t when deploying, deploy testing instances only.
#    Only store and directory available as testing.
#    Does not affect the build command.

errors=()
root=""
deployonly=0
buildonly=0
restartonly=0
testing=""
default_serverlist=(user directory store frontend)

# Builds the named binary statically.
function build {
    server=$1
    pushd "$root/cmd/$server" >/dev/null
    echo "=== Building $server ..."
    runsafely env CGO_ENABLED=0 go build -v -o "/tmp/$server"
    popd >/dev/null
}

# Deploys the named binary to GCE and restarts it.
function deploy {
    server=$1
    echo "=== Deploying $server$testing ..."
    # Copy binary to GCE
    runsafely scp "/tmp/$server" upspin.io:/tmp
    # Stop service and move binary
    stop "$server"
    runsafely ssh upspin.io "sudo cp /tmp/$server /var/www/$server$testing"
    if [ "$server" == "frontend" ]; then
        runsafely ssh upspin.io "cd /var/www; sudo setcap CAP_NET_BIND_SERVICE=+eip /var/www/frontend"
    fi
    start "$server"
}

# Restarts a service on upspin.io
function restart {
    server=$1
    stop "$server"
    start "$server"
}

# Stops a service on upspin.io
function stop {
    server=$1
     echo "Stopping service $server$testing"
    runsafely ssh upspin.io "sudo supervisorctl stop upspin-$server$testing"
}

# Starts a stopped service on upspin.io
function start {
    server=$1
     echo "Starting service $server$testing"
    runsafely ssh upspin.io "sudo supervisorctl start upspin-$server$testing"
}

# Finds the root of project upspin by looking in the current directory and in $GOPATH and puts it in $root
function find_root {
    local test_dir="$GOPATH/src/upspin.googlesource.com/upspin.git"
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
            -t|--testing)
            testing="-test"
            ;;
            store)
            serverlist[${#serverlist[*]}]="store"
            ;;
            directory)
            serverlist[${#serverlist[*]}]="directory"
            ;;
            user)
            serverlist[${#serverlist[*]}]="user"
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
        if [[ $testing && ($server != "store" && $server != "directory") ]]; then
            echo "There is no testing instance for $server"
            exit  # this could be a continue, but it's probably not what the user intended. Be safe.
        fi
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
