# Setting up upspinserver

## Introduction

This document describes how to create an Upspin installation by deploying
`upspin.io/cmd/upspinserver`, a combined Upspin Store and Directory server, to
a Linux-based machine.
The installation will use the central Upspin Key server (`key.upspin.io`) for
authentication, which permits inter-operation with other Upspin servers.

The process follows these steps:

- sign up for an Upspin user account, registering your public key with the
  central server `key.upspin.io`,
- configure a domain name and create an Upspin user for the server,
- create a Google Cloud Project and set up a Google Cloud Storage bucket (this
  step may be skipped if you wish to store data on your server's local disk),
- deploy `upspinserver` to a Linux-based server,
- configure `upspinserver`.

Each of these steps (besides deployment) has a corresponding `upspin`
subcommand to assist you with the process.

## Prerequisites

To deploy an `upspinserver` you need to decide on values for:

- An Internet domain to which you can add DNS records.
  (We will use `example.com` in this document.)
  Note that the domain need not be dedicated to your Upspin installation; it
  just acts as a name space inside which you can create Upspin users for
  administrative purposes.

- Your Upspin user name (an email address).
  (We will use `you@gmail.com` in this document.)
  This user will be the administrator of your Upspin installation.
  The address may be under any domain,
  as long you can receive mail at that address.

- The host name of the server on which `upspinserver` will run.
  (We will use `upspin.example.com` in this document.)

## Sign up for an Upspin account

Run `upspin signup`, passing your chosen host name as its `-server` argument
and your chosen Upspin user name as its final argument.
Then follow the onscreen instructions.

For example:

```
$ upspin signup -server=upspin.example.com you@gmail.com
```

The [Signing up a new user](/doc/signup.md) document describes this process in
detail.
If you change your mind about the host name, you can update with `upspin user -put`.

## Set up your domain

Upspin servers also run as Upspin users, with all the rights and requirements
that demands, and so they need usernames and key pairs registered with the
Upspin key server.
The Upspin user for your server is typically under the domain you are setting up.

You need not use the signup process to create users for your servers.
Instead, the `upspin setupdomain` command will do the work for you.
The `upspin setupdomain` command assumes you want to use `upspin@` followed by
your domain name as your server user name.
(For our example, that's `upspin@example.com`.)

This command sets up users for our example domain:

```
$ upspin setupdomain -domain=example.com
```

It should produce output like this:

```
Domain configuration and keys for the user
  upspin@example.com
were generated and placed under the directory:
  /home/you/upspin/deploy/example.com
If you lose the keys you can re-create them by running this command
  $ upspin keygen -where /home/you/upspin/deploy/example.com -secretseed zapal-zuhiv-visop-gagil.dadij-lnjul-takiv-fomin
Write this command down and store it in a secure, private place.
Do not share your private key or this command with anyone.

To prove that `you@gmail.com` is the owner of `example.com`,
add the following record to example.com's DNS zone:

  NAME  TYPE  TTL DATA
  @ TXT 15m upspin:a82cb859ca2f40954b4d6-239f281ccb9159

Once the DNS change propagates the key server will use the TXT record to verify
that you@gmail.com is authorized to register users under example.com.
```

Follow the instructions: place a new TXT field in the `example.com`'s DNS entry
to prove to the key server that you control the DNS records for the domain
`example.com`.
Once the DNS records have propagated, `you@gmail.com` will in effect be
administrator of Upspin's use of `example.com`.

As a guide, here's what the DNS record looks like in Google Domains:

![DNS Entries](https://upspin.io/images/txt_dns.png)

Consult your registrar's documentation if it is not clear how to add a TXT
record to your domain.

Once the TXT record is in place, the key server will permit you to register the
newly-created users that will identify the servers you will deploy (as well as
any other users you may choose to give Upspin user names within `example.com`).
At a later step, the `upspin setupserver` command will register your server
user for you automatically.

## Set up the Google Cloud Storage service

**Note**: if you do not wish to store your data in Google Cloud Storage you may
skip this section (move on to "Set up a server"), in which case data will be
stored on the local disk of your chosen server.
**If you choose to store your Upspin data on the your server's local disk then
in the event of a disk failure all your Upspin data will be lost.**

### Create a Google Cloud Project

First create a Google Cloud Project and associated Billing Account by visiting the
[Cloud Console](https://cloud.google.com/console).
See the corresponding documentation
[here](https://support.google.com/cloud/answer/6251787?hl=en) and
[here](https://support.google.com/cloud/answer/6288653?hl=en)
for help.
For the project name, we suggest you use a string similar to your domain.
(We will use `example-com`.)

Then, install the Google Cloud SDK by following
[the official instructions](https://cloud.google.com/sdk/downloads).

Finally, use the `gcloud` tool to enable the required APIs:

```
$ gcloud components install beta
$ gcloud config set project example-com
$ gcloud auth login
$ gcloud beta service-management enable iam.googleapis.com
$ gcloud beta service-management enable storage_api
```

### Create a Google Cloud Storage bucket

Use the `gcloud` tool to obtain "application default credentials" so that the
`upspin setupstorage` command can make changes to your Google Cloud Project:

```
$ gcloud auth application-default login
```

Then use `upspin setupstorage` to create a storage bucket and an associated
service account for accessing the bucket.
Note that the bucket name must be globally unique among all Google Cloud
Storage users, so it is prudent to include your domain name in the bucket name.
(We will use `example-com-upspin`.)

```
$ upspin -project=<project> setupstorage -domain=example.com example-com-upspin
```

It should produce output like this:

```
Service account "upspinstorage@example-com.iam.gserviceaccount.com" created.
Bucket "example-com-upspin" created.
You should now deploy the upspinserver binary and run 'upspin setupserver'.
```

## Set up a server and deploy `upspinserver`

Now build `upspinserver` and deploy it to a publicly-accessible server.

### Provision a server

You can run `upspinserver` on any server, including Linux, MacOS, Windows,
and [more](https://golang.org/doc/install#requirements), as long as it has a
publicly-accessible IP address and can run Go programs.

> Note that Upspin has been mostly developed under Linux and MacOS.
> You may encounter issues running it on other platforms.

For a personal Upspin installation, a server with 1 CPU core, 2GB of memory,
and 20GB of available disk space should be sufficient.

You can provision a suitable Linux VM in your Google Cloud Project by visiting
the Compute section of the [Cloud Console](https://cloud.google.com/console)
and clicking "Create VM".

> If you're unfamiliar with Google Cloud's virtual machines, here are some sane
> defaults: choose the `n1-standard-1` machine type, select the Ubuntu 16.04
> boot disk image, check "Allow HTTPS traffic", and under "Networking" make
> sure the the "External IP" is a reserved static address (rather than
> ephemeral).

Once provisioned, make a note of the server's IP address.

### Create a DNS record

With a server provisioned, you must create a DNS record for its host name.
As you did earlier with the `TXT` record, visit your registrar to create an `A`
record that points your chosen host name (`upspin.example.com`) to the server's
IP address.

### Deploy `upspinserver`

Now build `upspin.io/cmd/upspinserver` and configure your server to run
it on startup and serve on port `443`.

You may do this however you like, but you may wish to follow one of these
guides:

- [Running `upspinserver` on Ubuntu 16.04](/doc/server_setup_ubuntu.md)
- (More coming soon...)

## Test connectivity

Using your web browser, navigate to the URL of your `upspinserver`
(`https://upspin.example.com/`).
You should see the text:

```
Unconfigured Upspin Server
```

If the page fails to load, check the `upspinserver` logs for clues.


## Configure `upspinserver`

On your workstation, run `upspin setupserver` to send your server keys and
configuration to the `upspinserver` instance:

```
$ upspin setupserver -domain=example.com -host=upspin.example.com
```

This registers the server user with the public key server, copies the
configuration files from your workstation to the server, restarts the server,
makes the Upspin user root for `upspin@example.com` and puts the
`upspin@example.com/Group/Writers` file, and makes the Upspin user root for
`you@gmail.com`.

It should produce output like this:

```
Successfully put "upspin@example.com" to the key server.
Configured upspinserver at "upspin.example.com:443".
Created root "you@gmail.com".
```

If you make a mistake configuring `upspinserver` you can start over by
removing `$HOME/upspin/server` and re-running `upspin setupserver`. 
Note that the `upspin/server` directory contains your directory server data
and—if you are using the local disk for storage—any store server objects. 
If you are using Google Cloud Storage you may want to delete the contents of
your storage bucket before running `upspin setupserver` again to avoid paying
to store orphaned objects.


## Use your server

You should now be able to communicate with your Upspin installation using the
`upspin` command and any other Upspin-related tools.

To test that you can write and read to your Upspin tree, first create a file:

```
$ echo Hello, Upspin | upspin put you@gmail.com/hello
```

The `upspin put` command reads data from standard input and writes it to a file
in the root of your Upspin tree named "hello".

Then read the file back, and you should see the greeting echoed back to you.

```
$ upspin get you@gmail.com/hello
Hello, Upspin
```
If you see the message, then congratulations!
You have successfully set up an `upspinserver`.
