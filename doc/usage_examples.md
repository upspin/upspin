# Usage Examples

## Introduction

This document ullustrates how to use Upspin and the `upspin` command to do some
common Upspin tasks.
It is assumed that the user has fully signed up as `ann@example.com` as
described in [Signing up a new user](/doc/signup.md) and has [set up
`upspinserver`](/doc/server_setup.md) as a combined dirserver/storeserver for
the domain `example.com`.
Substitude your own user and domain names in the examples that follow.

## Allowing a family member to share your Upspin instllation

Initially, a store and directory server will allow any Upspin user to write
objects or create directories.
You should restrict write access to only the users who you intend to use these
servers.
This is achieved by creating access control files that the servers read.
When you followed [the server setup instructions](/doc/server_setup_md),
`upspin setupserver` has done this for you already without being verbose about
it.

Once those files are in place, access is restricted to the users listed in
those files (which must include the directory server’s user, because the
directory server stores its data in the store server).

The easiest way to modify the access control file is to use the `upspin
setupwriters` command. The arguments list the users (or wildcards) to be
granted access.

```
$ upspin setupwriters -domain=example.com ann@example.com anotherone@example.com third@example.com
# ... or with wildcards
$ upspin setupwriters -domain=example.com ann@example.com *@example.com
```

Every time you call `upspin setupwriters` the access control files would be
written from scratch.
So the following two commands would result in `second@example.com` not being in
`Writers`.

```
$ upspin setupwriters -domain=example.com ann@example.com second@example.com
$ upspin setupwriters -domain=example.com ann@example.com third@example.com
```

Luckily there is an easy way to edit the `Writers` file locally on your
machine.
`upspin get` its contents to your local machine, edit to your liking, and
`upspin put` it back on the server.
For this to work, explicitely make `upspin` use the server user config with
`-config`.

```
upspin -config=$HOME/upspin/deploy/example.com/config get upspin@example.com/Group/Writers > Writers
# modify Writers with your a text editor of your choice
upspin -config=$HOME/upspin/deploy/example.com/config put upspin@example.com/Group/Writers < Writers
```

Alternatively you could use `upspinfs
-config=$HOME/upspin/deploy/example.com/config <mountput>` to FUSE-mount as the
upspin server user and call the editor with the FUSE-mounted path to `Writers`.

## Using the Upspin Configuration and Keys on Another Machine

To use your Upspin user configuration and keys on a different machine, you need
to transfer two things to that machine:

1. The file `$HOME/upspin/config` which contains the user configuration.
2. The public/private key pair for the user, which is located in
   `$HOME/.ssh/public.upspinkey` and `$HOME/.ssh/secret.upspinkey`.

Copy `~/upspin` and `~/.ssh/*.upspinkey` to the second device into the same
paths under `$HOME` and make sure that they have the same restrictive
permissions as on the source machine.
One way to do that is to use the following commands on the destination machine.

```bash
$ test -d ~/upspin || (mkdir ~/upspin && chmod 0700 ~/upspin)
$ scp -p sourcemachine:upspin/config ~/upspin
$ test -d .ssh || (mkdir ~/.ssh && chmod 0700 ~/.ssh)
$ scp -p sourcemachine:.ssh/*.upspinkey ~/.ssh
```

It is intentionally that simple, so that people can easily create redundant
devices and so that they can appreciate the need to protect ~/.ssh
from theft.

