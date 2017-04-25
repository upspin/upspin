# Usage Examples

## Introduction

This document illustrates how to use Upspin and the `upspin` command to do some
common Upspin tasks.
It is assumed that the user has fully signed up as `ann@example.com` as
described in [Signing up a new user](/doc/signup.md) and has [set up
`upspinserver`](/doc/server_setup.md) as a combined dirserver/storeserver for
the domain `example.com`.
Substitute your own user and domain names in the examples that follow.

## Allowing a family member to share your Upspin installation

When you followed [the server setup instructions](/doc/server_setup.md),
`upspin setupserver` created an initial version of the access control
files for your Upspin installation that allows access for `ann@example.com` and
the server user `upspin@example.com`.

The easiest way to modify the access control file is to use the `upspin
setupwriters` command. The arguments list the users (or wildcards) to be
granted access.
The server user `upspin@example.com` is always included automatically.

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

It is easy to edit the `Writers` file locally on your machine.
Run `upspin get` to store its contents to your local machine, edit to your
liking, and `upspin put` it back on the server.
For this to work, explicitly make `upspin` use the server user config with the
`-config` flag, pointing it to the config file for the server's user
`upspin@example.com`.


```
upspin -config=$HOME/upspin/deploy/example.com/config get upspin@example.com/Group/Writers > Writers
# Modify Writers with your text editor of choice
upspin -config=$HOME/upspin/deploy/example.com/config put upspin@example.com/Group/Writers < Writers
```

## Using your Upspin configuration and keys on another machine

To use your Upspin user configuration and keys on a different machine, you need
to transfer two things to that machine:

1. The file `$HOME/upspin/config` which contains the user configuration.
2. The public/private key pair for the user, which is located in
   `$HOME/.ssh/public.upspinkey` and `$HOME/.ssh/secret.upspinkey`.

Copy `~/upspin` and `~/.ssh/*.upspinkey` to the second device into the same
paths under `$HOME` and make sure that they have the same restrictive
permissions as on the source machine.
One way to do that is to use the following commands on the destination machine,
assuming it is a Unix machine:

```
$ test -d ~/upspin || (mkdir ~/upspin && chmod 0700 ~/upspin)
$ scp -p sourcemachine:upspin/config ~/upspin
$ test -d .ssh || (mkdir ~/.ssh && chmod 0700 ~/.ssh)
$ scp -p sourcemachine:.ssh/*.upspinkey ~/.ssh
```
