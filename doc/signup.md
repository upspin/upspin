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
The mail system you use must be able to sign messages with
[DKIM](http://www.dkim.org/), which proves that the originator of an email
message owns the email address and that the agent sending the email owns the
domain name for that email address.
Almost any modern mail service as Gmail, Yahoo mail and Hotmail supports DKIM.

Next you need to decide whether you are going to deploy your own Upspin
directory and store servers, or use those maintained by someone else.

While it is straightforward to deploy one's own servers, it is a more complex
process that is documented in
[Deploying Upspin servers to Google Cloud](/doc/deploying_to_google_cloud.md).
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
prepares instructions for the key server to remember the user's details.
It also creates a local copy of the information called an "`config`" file that
it stores in a local directory, typically `$HOME/upspin`.
Config files are discussed in detail in [TODO LINK].

The second step is to send mail to the key server to finalize the registration.

Here is the first step.
Run this shell command, substituting your email address and directory and store
servers:

```
$ upspin signup -dir=dir.example.com -store=store.example.com you@gmail.com
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

To register your key with the key server,
copy this email message and send it to signup@key.upspin.io:

I am you@gmail.com;

My public key is:
p256;
4953994978686598553220695820665578334776881714507948749731602561414105826492;
66910428438522843239264968938397625098537992797641837719834448519317121655042;

My directory server is:
remote,dir.example.com:443;

My store server is:
remote,store.example.com:443;

Signature:
105424705881407947899850441112873315959074883283421704267305718920731238199673;
23440675362566872099122712067769200456811520379123850064222464518954024259340;

(End of message.)
```

---

The output is self-explanatory.
Its key points are that it has written a config file for you, created your
keys, and output the instructions to recover your keys if you lose them one day.
Finally it prints the mail to send to tell the Upspin key server about you.
Please read it carefully.

As it says, you need to send mail to `signup@key.upspin.io` to complete your
registration.
This message tells the key server your details and instructs it to save them.
(Your public key is needed for securing and sharing Upspin files, and it's safe
to share.)


Copy the message exactly as printed by the upspin signup command, and mail it
to `signup@key.upspin.io`.
It should succeed, and you will soon receive a mail message back confirming
your registration.
The confirmation mail will look like this:

---

```
From: keyserver@upspin.io
Subject: Welcome to Upspin

Your Upspin account you@gmail.com has been registered with the public key
server.

For instructions on setting up your directory tree, see TODO.
```

---

## Creating your Upspin directory

Once you are registered in the key server, the next step is to create a
directory that will host your Upspin tree.
This will of course be done in the directory server you have registered above.

If you are planning to run your own Upspin directory and store servers, you
must deploy them now, once you are registered with the key server.
See
[Deploying Upspin servers to Google Cloud](/doc/deploying_to_google_cloud.md)
for instructions to do that.

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
cache server that improves performance.
See TODO for details.
