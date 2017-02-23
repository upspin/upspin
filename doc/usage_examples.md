# Usage Examples

## Introduction

This document describes common usage scenarios and how to address them with the
`upspin.io/cmd/upspin` tool. It is assumed that the user has fully signed up as
`user@example.com` as described in [Signing up a new user](/doc/signup.md) and
has [set up `upspinserver`](/doc/server_setup.md) as a combined
dirserver/storeserver for the domain `example.com`.

## Allowing a Family Member or Friend to use the Upspinserver

Initially, a store and dir server will allow any Upspin user to write objects
or create directories. You should restrict write access to only the users who
you intend to use these servers. This is achieved by creating access control
files that the servers read. Once those files are in place, access is
restricted to the users listed in those files (which must include the directory
server’s user, because the directory server stores its data in the store
server).

The easiest way to set this mechanism up is to use the `upspin setupwriters`
command. The arguments list the users (or wildcards) to be granted access.

```
$ upspin setupwriters -domain=example.com user@example.com anotherone@example.com third@example.com
# ... or with wildcards
$ upspin setupwriters -domain=example.com user@example.com *@example.com
```

Every time you call`upspin setupwriters` the access control files would be
written from scratch. So the following two commands would result in
`second@example.com` not being in `Writers`
and Keys on Another Machine

```
$ upspin setupwriters -domain=example.com user@example.com second@example.com
$ upspin setupwriters -domain=example.com user@example.com third@example.com
```

Luckily there is an easy way to edit `Writers` file locally on your machine. to
your local machine, add a line and `put` it back: You need to use the server
user config directly.

```
upspin -config=$HOME/upspin/deploy/example.com/config get upspin@example.com/Group/Writers > Writers
# modify Writers with your a text editor of your choice
upspin -config=$HOME/upspin/deploy/example.com/config put upspin@example.com/Group/Writers < Writers
```

Alternatively you could use `upspinfs
-config=$HOME/upspin/deploy/example.com/config <mountput>` to FUSE-mount as the
upspin server user and call the editor with the FUSE-mounted path to `Writers`.

## Using the Upspin Configuration
To use your Upspin user configuration and keys on a different machine, you need
to transfer two things to that machine:

1. The folder `$HOME/upspin` that contains the configuration.
2. The public/private key pair for the user which are located in `$HOME/.ssh/public.upspinkey` and `$HOME/.ssh/secret.upspinkey`.

Copy `~/upspin` and `~/.ssh/*.upspinkey` to the second device into the same
paths under `$HOME` and make sure that they have the same restrictive
permissions than on the source machine. One way to do that is to use the
following commands on the destination machine.

```bash
$ scp -r -p sourcemachine:upspin ~/upspin
$ test -d .ssh || (mkdir ~/.ssh && chmod 0700 ~/.ssh)
$ scp -p sourcemachine:.ssh/*.upspinkey ~/.ssh
```

It is intentionally that simple, so that people can easily create
redundant devices and so that they can viscerally appreciate the need to
protect ~/.ssh from theft.

