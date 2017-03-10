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

Next you need to decide whether you are going to deploy your own Upspin
directory and store servers, or use those maintained by someone else.

While it is straightforward to deploy one's own servers, it is a more complex
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

Upspin is written in Go, so the first step is to install the Go tool chain.
You will need Go version 1.8 or later.
You can get it from
[https://golang.org/doc/install](https://golang.org/doc/install).

Once you have Go installed, Upspin can be fetched by running, in a terminal,
the command

```
$ go get -u upspin.io/...
```

This will install all the libraries, plus tools such as the `upspin` command
that provides a command-line interface to create, access, share, and administer
data stored in Upspin.
If you are on a Unix system, it will also install the `upspinfs` program, which
uses FUSE to connect the Upspin name space to your local file tree.

## Generating keys and registering your identity

Upspin's security model is based on public key encryption, in which each Upspin
user has a pair of keys called the public and private keys.
The public key is registered with the public key server and is available to
everyone, while the private key is kept in secret by the user, such as on a
local workstation or other private device.

To register your details with the key server takes two steps.

First, the `upspin signup` command generates a key pair, saves it locally, and
sends the user details and the public key to the key server.
It also creates a local copy of the information called an "`config`" file that
it stores in a local directory, typically `$HOME/upspin`.
Config files are discussed in detail in [Upspin configuration](/doc/config.md).
You should read that document to see how to set up your Upspin environment,
including things like local caches.

The second step is to receive an email message from the key server and to click
the confirmation link that it contains.
Visiting that link proves to the key server that you control the email address
that you are registering and completes the signup process.

You may use your regular email address or an
Upspin-specific one; either way is fine.
The address is published in key server logs as well as in any Upspin
path name you share, so be sure your email account has whatever
spam, anonymity, or other protection you feel is necessary.

No email will be sent to the address after this signup step.
All future Upspin operations, even updating later to a new key pair,
will be validated exclusively with the key pair generated during signup.
Someone with future access to your email can't masquerade as you in Upspin.
Conversely, if you lose your keys even your email account is not enough
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

The output is self-explanatory.
Its key points are that it has written a config file for you, created your
keys, and output the instructions to recover your keys if you lose them one day.
Finally it prints the mail to send to tell the Upspin key server about you.
Please read it carefully.

As it says, you need to wait for an email message from the key server to
proceed.
The message contains a link.
Clicking that link proves to the key server that you own the email address, and
the key server will provide a copy of your public key to any Upspin user that
requests it.
(Your public key is needed for securing and sharing Upspin files, and it's safe
to share.)

**Pay attention to the text in the output about remembering your "secret seed".
It provides a way to regenerate your keys if you lose them.**

_Note: If used interactively with a shell that keeps a command history, the
using `keygen` with the `-secretseed` option may cause the secret to be saved in the history file.
If so, the history file should be cleared after running `keygen`._


## Creating your Upspin directory

Once you are registered in the key server, the next step is to create a
directory that will host your Upspin tree.
This will of course be done in the directory server you have registered above.

If you are planning to run your own Upspin directory and store servers, you
must deploy them now, once you are registered with the key server.
See [Setting up `upspinserver`](/doc/server_setup.md) for instructions to do that.

If you are planning to join an existing Upspin directory and store server, ask
the administrator to add your username to the store's Writers group.
(They'll know what to do.) This will grant you permission to create your tree
in that directory and store data in that store server.

With the servers running and granting you access permission, and with your
correct information registered in the key server, all you need to do to get
started is create a user root, which is just a `"make directory"` command using
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

The upspin command has many other subcommands.
Run


```
$ upspin -help
```

for a list.
Each subcommand also has help:

```
$ upspin cp -help
```

Although the upspin command supports all the functionality of the system, for
smoother operation you'll want to install the FUSE daemon, `upspinfs`, and a
cache server that improves performance. The cache server is particularly
important, and the setup instructions are in the [Upspin configuration](/doc/config.md)
document.

For details about `upspinfs`, run

```
$ go doc upspinfs
```

TODO: There should be more about `upspinfs`.
