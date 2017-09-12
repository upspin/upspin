# Signing up a new user

## Introduction

Before you sign up, make sure you have read the
[Upspin overview](/doc/overview.md) document.
It introduces the concepts and terminology you'll need to understand to use the
system.

You will need to choose an Upspin username, which is just an email address you
own.
Your username is how the Upspin system and its users will identify you and your
files.
Note that your chosen username will become a matter of public record in our
[key server log](https://key.upspin.io/log).

Some technical details, included for completeness:
Any valid email address is likely fine but since it will become as a user name
within Upspin, the system applies some constraints that could
invalidate some (very unusual) addresses.
Also, within Upspin user names are case-sensitive,
although in some email services the names are case-insensitive.
The full story is a bit technical and irrelevant for most users, but
if you are interested please see the description
[here](https://godoc.org/upspin.io/user#Parse).

To reiterate, any valid email address is almost certainly a valid Upspin user name.

Next you need to decide whether you are going to deploy your own Upspin
directory and store servers, or use those maintained by someone else.

While it is not too hard to deploy one's own servers, it is an involved
process that is documented in [Setting up `upspinserver`](/doc/server_setup.md).
If you can use an existing service, that will save you some time and trouble.

Note that there is no need for the email address and Upspin servers to be in
the same domain.

Once you have resolved these matters, you should know what your username will
be and also know the network addresses of the directory and store servers you
will be using.

For the rest of this document we will assume the username `you@gmail.com` and
the directory and store servers `dir.example.com` and `store.example.com`.
Naturally you should substitute your own values for these items in the
instructions below.

## Install the Upspin commands

Download an archive of the Upspin command-line tools from [the download
page](/dl/), and extract it to a directory that's in your system `PATH`.

The archive includes the `upspin` command-line tool (to create, access, share,
and administer data stored in Upspin), the `cacheserver` daemon (a cache for
remote Upspin data), and, on macOS and Linux systems, the `upspinfs` program (a
[FUSE](https://github.com/libfuse/libfuse) filesystem, to mount the Upspin file
system in your local file tree).

> If your operating system is not listed on the download page, you can obtain
> the binaries by installing Upspin from source.
> First [install Go](https://golang.org/doc/install) and then use `go get` to
> fetch Upspin and its dependencies and build them:
> ```
$ go get upspin.io/cmd/...
```
> This will install the Upspin commands to `$GOPATH/bin`, which you should add
> to your system `PATH` if you haven't already.

## Generating keys and registering your identity

Upspin's security model is based on public key encryption, in which each Upspin
user has a pair of keys called the public and private keys.
The public key is registered with the public key server and is available to
everyone, while the private key is kept in secret by the user, such as on a
local workstation or other private device.

**Note: As part of the signup process,
the system will create a secret key for you.
It is vital that you do not lose or share this key or its "secret seed"
(which is equivalent to the key itself).
If you lose your key and its secret seed
you will lose access to this Upspin identity,
including all the data you have stored and even the ability
to use your registered user name.
There is no way to recover a lost key.
The high security that Upspin offers would be compromised if
there were an account recovery mechanism.**

**Read the rest of this section carefully before proceeding.**

To register your details with the key server takes two steps.

The first step in registration is to run an `upspin signup` command,
which generates a key pair
(one secret key, one public key), saves the keys locally, and
sends the your details, including your public key to the key server.
The public key is published to the shared
Upspin key server, but the secret key
is stored only on your local computer.

The locations provided as the `server`, `dir`, and `store` parameters when
registering an identity are recorded in the key server.
Other users can then look up your name in the key server to learn the locations
of your directory and store servers.
The registration process also creates a local copy of the information
called a "config" file that it stores in a local directory, typically
`$HOME/upspin`.
Config files are discussed in detail in [Upspin configuration](/doc/config.md).
You should read that document to see how to set up your Upspin environment,
including things like local caches.

The second step is to receive an email message from the key server and to click
the confirmation link that it contains.
Visiting that link proves to the key server that you control the email address
that you are registering and completes the signup process.
From here on, the email address serves as your Upspin user name.
However, after this account verification step Upspin will never use it as an actual
email address again.
At this point you could even cancel the email account, if you chose to do so,
without affecting your Upspin user name.
In fact, even if the email account is later hijacked, the
attacker will not be able to get access to your Upspin account.

You may use your regular email address or an
Upspin-specific one; either way is fine.
The address is published in key server logs as well as in any Upspin
path name you share, so be sure your email account has whatever
spam, anonymity, or other protection you feel is necessary.

No email will be sent to the address after this signup step.
All future Upspin operations, even updating later to a new key pair,
will be validated exclusively with the key pair generated during signup.
Someone with future access to your email can't masquerade as you in Upspin.
Conversely, if you lose your keys your email account is not useful
for recovery.

Here is the first step in more detail.
Run this shell command, substituting your email address and directory and store
servers:

```
$ upspin signup -dir=dir.example.com -store=store.example.com you@gmail.com
```

Or, if the directory and store servers run on the same host, specify
that host name with the `-server` flag:

```
$ upspin signup -server=upspin.example.com you@gmail.com
```

The output of the command will look like this:

---

```
Configuration file written to:
  /home/you/upspin/config

Upspin private/public key pair written to:
  /home/you/.ssh/public.upspinkey
  /home/you/.ssh/secret.upspinkey
This key pair provides access to your Upspin identity and data.
If you lose the keys you can re-create them by running this command:
  upspin keygen -secretseed dukir-mokin-dunaz-vanog.sufus-bavab-sidiz-fufar
Write this command down and store it in a secure, private place.
Do not share your private key or this command with anyone.

A signup email has been sent to "you@gmail.com",
please read it for further instructions.
```

---

**In that output when you run that command
is a string that you can use to recreate the secret key
should you lose the key or wish to install it on a new computer.
This "secret seed" serves as a human-readable version of the key.
(The computer-readable version is just a very long number.)
Write down this secret seed (the one you receive, not the
one in the example), keep it somewhere safe and do not lose it.
It is literally your key to Upspin.**

The rest of the output is self-explanatory.
Its main points are that it has written a config file for you, created your
keys, and output the instructions to recover your keys if you lose them one day.
Finally it prints a message stating what email address will be used for the 
verification message.
Please read it carefully.

As it says, you need to wait for an email message from the key server to
proceed.
The message contains a link.
Clicking that link proves to the key server that you own the email address, and
the key server will provide a copy of your public key to any Upspin user that
requests it.
(Your public key is needed for securing and sharing Upspin files, and it's safe
to share.)

**Again, make sure to write down your "secret seed" and do not lose it.**

_Note: If used interactively with a shell that keeps a command history,
using `keygen` with the `-secretseed` option may cause the secret to be saved in the history file.
If so, the history file should be cleared after running `keygen`._

If one day you change the location of your directory and store servers, you must
update the values stored in the key server.
One easy way to do this is to update the values in `$HOME/config` and run:

```
$ upspin user | upspin user -put
```

## Creating your Upspin directory

Once you are registered in the key server, the next step is to create a
directory that will host your Upspin tree.
This will of course be done in the directory server you have registered.

If you are planning to run your own Upspin directory and store servers, you
must deploy them now, once you are registered with the key server.
See [Setting up `upspinserver`](/doc/server_setup.md) for instructions to do that.

If you are planning to join an existing Upspin directory and store server, ask
the administrator to add your username to the store's Writers group.
(They'll know what to do.) This will grant you permission to create your tree
in that directory and store data in that store server.

With the servers running and granting you access permission, and with your
correct information registered in the key server, all you need to do to get
started is create a user root, which is just a "make directory" command using
the `upspin` tool:

`$ upspin mkdir you@gmail.com/`

Once this is done, the existence of your directory and store servers is largely
invisible.
All your Upspin work will be based on your user name, which we have here as
`you@gmail.com`.

## Hello, world

To prove that your Upspin tree was created successfully, try copying a file to
the system.
The most direct way is to use the `upspin cp` command:

```
$ upspin cp hello.jpg you@gmail.com/
```

Then, to check that everything worked, copy it back and verify its contents:

```
$ upspin cp you@gmail.com/hello.jpg ciao.jpg
$ sum hello.jpg ciao.jpg
1600 21 hello.jpg
1600 21 ciao.jpg
```

The `upspin` command has many other subcommands.
Run


```
$ upspin -help
```

for a list.
Each subcommand also has help:

```
$ upspin cp -help
```

Although the `upspin` command supports all the functionality of the system, for
smoother operation you'll want to install the FUSE daemon, `upspinfs`, and a
cache server that improves performance. The cache server is particularly
important, and the setup instructions are in the [Upspin configuration](/doc/config.md)
document.

## Browsing Upspin Files on Linux and macOS

Upspin includes a tool called `upspinfs` that creates a virtual filesystem
where you can access the Upspin name space as a regular mounted file system.

Here is an example of its use.

Make a directory in which to mount the Upspin name space:

```
$ mkdir $HOME/up
```

Then run the `upspinfs` command giving that directory as its sole argument:

```
$ upspinfs $HOME/up
```

Now you have access to the full Upspin name space:

```
$ ls $HOME/up/you@gmail.com
```

The `upspinfs` command will exit when the file system is unmounted.

If you encounter an error when you run `upspinfs` the second time, such as:

```
mount helper error: fusermount: failed to open mountpoint for reading: Transport endpoint is not connected
fuse.Mount failed: fusermount: exit status 1
```

just unmount the directory and try again.

To learn more about `upspinfs`, run

```
$ go doc upspin.io/cmd/upspinfs
```
TODO: Talk about `cacheserver`.
