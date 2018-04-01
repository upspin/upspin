# Setting up upspinserver

## The easy way

The [Upspin tools](/dl/) include a program called `upspin-ui` that automates
the deployment of an `upspinserver` to Google Cloud Platform.
If you wish to deploy to GCP, try using `upspin-ui` instead of following this
guide.
See the [signup document](signup.md) for more information.

## Conventions
Throughout this document, we will mark commands to be run on your
local machine with the shell prompt `local$` and commands to be
run on your server with `server%`.

For example:

```
local$ upspin signup -server=upspin.example.com you@gmail.com
```
and
```
server% sudo systemctl stop upspinserver.service
```

## Introduction
This document describes the process for creating an Upspin installation by deploying
an `upspinserver`, a combined Upspin Store and Directory server, to
a Linux-based machine.

The installation will use the central Upspin key server (`key.upspin.io`) for
authentication, which permits inter-operation with other Upspin servers.

There are multiple versions of `upspinserver`, each depending on where the
associated storage is kept, either on the server's local disk or with a cloud
storage provider.
The binaries that use cloud storage providers each have a suffix that
identifies the provider, such as `upspinserver-gcp` for the Google Cloud
Platform.
These binaries are also kept in distinct repositories, such as `gcp.upspin.io`
for the Google Cloud Platform.

The process follows these steps:

- [sign up](#signup) for an Upspin user account
- [configure](#domain) a domain name and create an Upspin user for the server,
- if necessary, [set up the cloud](#cloud
) storage service,
- [deploy](#deploy) the `upspinserver` to a Linux-based server,
- [configure](#configure) the `upspinserver`.

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

## Sign up for an Upspin account {#signup}

To register your public key with the central key server run `upspin signup`,
passing your chosen host name as its `-server` argument
and your chosen Upspin user name as its final argument.
Then follow the onscreen instructions.

The [Signing up a new user](/doc/signup.md) document describes this process in
detail.
If you change your mind about the host name, you can update with `upspin user -put`.

## Set up your domain {#domain}

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
local$ upspin setupdomain -domain=example.com
```

It should produce output like this:

```
Domain configuration and keys for the user
	upspin@example.com
were generated and placed under the directory:
	/home/you/upspin/deploy/example.com
If you lose the keys you can re-create them by running this command
	upspin keygen -secretseed zapal-zuhiv-visop-gagil.dadij-lnjul-takiv-fomin /home/you/upspin/deploy/example.com
Write this command down and store it in a secure, private place.
Do not share your private key or this command with anyone.

To prove that you@gmail.com is the owner of example.com,
add the following record to example.com's DNS zone:

	NAME	TYPE	TTL	DATA
	@	TXT	15m	upspin:aff6a1083da7f1cdb182d43aa3

(Note that '@' here means root, not a literal '@' subdomain).

Once the DNS change propagates the key server will use the TXT record to verify
that you@gmail.com is authorized to register users under example.com.
At a later step, the 'upspin setupserver' command will register your server
user for you automatically.

After that, the next step is to run 'upspin setupstorage' (to configure a cloud
storage provider) or 'upspin setupserver' (if you want to store Upspin data on
your server's local disk).
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

Note that some registrars will display the root subdomain name as `@`; you
should not type in the `@` character.

On a Unix machine you can verify that your record is in place (it may take a
few minutes to propagate) by running:

```
local$ host -t TXT example.com
```

Once the TXT record is in place, the key server will permit you to register the
newly-created users that will identify the servers you will deploy (as well as
any other users you may choose to give Upspin user names within `example.com`).
At a later step, the `upspin setupserver` command will register your server
user for you automatically.


## Set up storage and build the `upspinserver` binary

The following sub-sections each describe how to obtain and build a
`upspinserver` binary and set up the storage for a particular location,
such as the server's local disk or a cloud storage provider.

Follow the instructions appropriate for your chosen storage location.

You will need to build an `upspinserver` binary for the server's operating
system and processor architecture.
We will assume 64-bit Linux in this document.


### Local disk

To run off local disk you need to build the `upspin.io/cmd/upspinserver` binary:

```
local$ GOOS=linux GOARCH=amd64 go build upspin.io/cmd/upspinserver
```

The default is to store data in $HOME/upspin/storage.
TODO upspin-setupstorage stuff

**If you choose to store your Upspin data on your server's local disk then
in the event of a disk failure all your Upspin data will be lost.**

### Specific instructions for cloud services {#cloud}

+ [Google Cloud Services](/doc/server_setup_gcp.md)
+ [Google Drive](/doc/server_setup_drive.md)
+ [Amazon Web Services](/doc/server_setup_aws.md)
+ [Dropbox](/doc/server_setup_dropbox.md)
+ [Backblaze B2](/doc/server_setup_b2.md)

## Set up a server and deploy the `upspinserver` binary {#deploy}

Now provision a server and deploy the `upspinserver` binary to it.

### Provision a server

You can run an `upspinserver` on any server, including Linux, macOS, Windows,
and [more](https://golang.org/doc/install#requirements), as long as it has a
publicly-accessible IP address and can run Go programs.

> Note that Upspin has been mostly developed under Linux and macOS.
> You may encounter issues running it on other platforms.

For a personal Upspin installation, a server with 1 CPU core, 2GB of memory,
and 20GB of available disk space should be sufficient.

If you're using the Google Cloud Platform, you can provision a suitable Linux
VM by visiting the Compute section of the
[Cloud Console](https://cloud.google.com/console) and clicking "Create VM".

> If you're unfamiliar with Google Cloud's virtual machines, here are some sane
> defaults: choose the `n1-standard-1` machine type, select the Ubuntu 16.04
> boot disk image, check "Allow HTTPS traffic", and under "Networking" make
> sure the "External IP" is a reserved static address (rather than
> ephemeral).

Once provisioned, make a note of the server's IP address.

### Create a DNS record

With a server provisioned, you must create a DNS record for its host name.
As you did earlier with the `TXT` record, visit your registrar to create an `A`
record that points your chosen host name (`upspin.example.com`) to the server's
IP address.

### Deploy `upspinserver`

Now deploy your `upspinserver` binary to your server and configure it to run on
startup and serve on ports `80` and `443`.

You may do this however you like, but you may wish to follow one of these
guides:

- [Running `upspinserver` on Ubuntu 16.04](/doc/server_setup_ubuntu.md)
- (More coming soon...)


## Test connectivity

At this point, you should have an `upspinserver` running on your server in
"setup mode", which means that it is ready to be configured by the `upspin
setupserver` command.
This state is indicated by a log message printed on startup:

```
Configuration file not found. Running in setup mode.
```

Test that the `upspinserver` is accessible from the outside by making an HTTP
request to it. Using your web browser, navigate to the URL of your
`upspinserver` (`https://upspin.example.com/`). You should see the text:

```
Unconfigured Upspin Server
```

If the page fails to load, check the `upspinserver` logs for clues.


## Configure `upspinserver` {#configure}

On your workstation, run `upspin setupserver` to send your server keys and
configuration to the `upspinserver` instance:

```
local$ upspin setupserver -domain=example.com -host=upspin.example.com
```

This registers the server user with the public key server, copies the
configuration files from your workstation to the server, restarts the server
and makes the Upspin user roots for `upspin@example.com` (the server user)
and `you@gmail.com`.

It also creates a special `Group` file for the store server,
`upspin@example.com/Group/Writers`,
whose contents are the names of Upspin users allowed to store data in
the server.
If later you decide to allow more people to use your system, you must update
this file.
See the documentation for `upspin setupwriters` for more information about
this.

It should produce output like this:

```
Successfully put "upspin@example.com" to the key server.
Configured upspinserver at "upspin.example.com:443".
Created root "you@gmail.com".
```

If you make a mistake configuring your server, you can start over by
removing `$HOME/upspin/server` and re-running `upspin setupserver`.
Note that the `$HOME/upspin/server` directory contains your directory server
data, and—if you are using the local disk for storage—any store server objects.
Deleting these files effectively deletes all the data you have put into Upspin.
If you are using a cloud service you may want to delete the contents of your
storage bucket before running `upspin setupserver` again to avoid paying to
store orphaned objects.


## Use your server

You should now be able to communicate with your Upspin installation using the
`upspin` command and any other Upspin-related tools.

To test that you can write and read to your Upspin tree, first create a file:

```
local$ echo Hello, Upspin | upspin put you@gmail.com/hello
```

The `upspin put` command reads data from standard input and writes it to a file
in the root of your Upspin tree named "hello".

Then read the file back, and you should see the greeting echoed back to you.

```
local$ upspin get you@gmail.com/hello
Hello, Upspin
```

If you see the message, then congratulations!
You have successfully set up an `upspinserver`.


## Purging your storage

> TODO: move this to an administrative document.

For a number of reasons, you may wish to discard all your stored data:

1. Upspin is in its early days. As a result we may make incompatible
   changes in the storage or directory formats. This should be rare but
   it may happen.
2. When experimenting with the system, you may create a lot of garbage.
   We hope to have a garbage collector for storage soon, but do not
   have one yet. The only way to clean up is to purge everything and
   start again.
3. Even with a garbage collector, you may find that it is easier to purge
   and restart from scratch than selectively delete files, especially
   when experimenting.

We detail here how to perform the purge if you are running an `upspinserver` on
machine running Ubuntu 16.04 or later.
You will have to tailor these instructions to your own environment
if you are doing something different.

On your server machine, as root, stop the `upspinserver`,
and remove the local server configuration.
This will remove all information about user trees.

```
local$ ssh upspin@upspin.example.com
server% sudo systemctl stop upspinserver.service
server% sudo rm -r ~upspin/upspin/server
```

If you configured your server to use Google Cloud Storage with `upspin
setupstorage-gcp` then you should also purge all references from your storage
bucket.
Run the following command, substituting your own bucket name for
`example-com-upspin`.
(If you have forgotten its name, use `gsutil ls` to list all your bucket names.)
You can do this anywhere you have authenticated as the account used
to set up your Google Cloud instance.

```
local$ gsutil -m rm 'gs://example-com-upspin/**'
```

The `-m` speeds things up by working in parallel.

Now that all your Upspin data has been purged, restart the server.

```
local$ ssh upspin@upspin.example.com
server% sudo systemctl start upspinserver.service
```

Since you have removed its configuration information, the `upspinserver` won't
serve regular Upspin requests until you run `upspin setupserver`.

Reconfigure the server from a host that has your original `$HOME/upspin/deploy`
directory tree.
This gives the server its Upspin keys, the initial contents of its `Writers`
file, and authentication information for accessing cloud storage (if any).

```
local$ upspin setupserver -domain=example.com -host=upspin.example.com
```

Now the server should be ready to use once more.
If you want snapshots, configure them with `upspin snapshot`.
