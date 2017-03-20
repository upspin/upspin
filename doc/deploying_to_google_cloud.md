# Deploying Upspin servers to Google Cloud Platform

## Overview

This document describes how to run one's own Upspin servers, which includes
deploying binaries for the Upspin servers to Google Cloud Platform and using
them to serve user data.
It assumes you already have the Upspin software installed,
as described at the bottom of the [overview document](/doc/overview.md).

The server binaries are built from the source in the directories
`cmd/dirserver` and `cmd/storeserver` in the repository stored at
https://upspin.googlesource.com/upspin.

To use Google Cloud Platform services you must first create a Billing Account
(to pay for the services) and a Project (a group of related services identified
by a name called the *project ID*).
Then you must create Upspin users for your servers using the `upspin
setupdomain` command, and use the `upspin-deploy` command to set up the
necessary services and build and deploy the servers.
Once they're running, Google Cloud Platform will choose IP addresses for them
and you can then point your domain to those addresses.

The following sections describe this process in detail.

Note that all these steps require knowledge about building, deploying, and
administering cloud services.
You must be comfortable with those responsibilities if you choose to run your
own Upspin servers.

### Obtain a domain name

When your servers are deployed they must be addressable by other users and
servers.
To achieve this you need a domain name.
You can obtain a domain name from a domain registrar.
You may use an existing domain you own or a new one bought for the purpose of
hosting Upspin servers.

The directory and store servers are given the names "dir" and "store" within
the chosen domain.
For this document, we will assume your domain is `example.com`, which implies
the servers will be addressed as `dir.example.com` and `store.example.com`.

### Register an administrative Upspin user name

Once you know the domain names of your directory and storage servers, you can
register an Upspin user name with the key server to act as the administrator
for this domain.
For details on this process, see the [Signup document](/doc/signup.md).

### Create project and billing account with GCP

To deploy new Upspin servers, you must create a billing account and a project
using the [Cloud Console](https://console.cloud.google.com/).
The project ID can be any available string, but for clarity it's helpful to
pick something that resembles your domain name.
Throughout the rest of this document your project ID will be referred to as
`example-project` but of course you should substitute your own when reading the
instructions.

#### Create server users and prove domain ownership

Before proceeding you need to be registered as an Upspin user.
This user will become the administrator of your chosen domain, and to keep
things simple it's better if the administrator is from a domain, such as
`gmail.com`, separate from the one you are setting up.
Follow the instructions in
[Signing up a new Upspin user](/doc/signup.md)
to register as an Upspin user.

Upspin servers also run as Upspin users, with all the rights and requirements
that demands, and so they need usernames and key pairs registered with the
Upspin key server.
These users typically *are* in the domain you are setting up.
For our example these usernames will be `upspin-dir@example.com` and
`upspin-store@example.com`.

You need not use the signup process to create users for your servers.
Instead, the `upspin setupdomain` command will do the work for you.

Throughout this document, all commands are to be run on your
local machine and so are marked with the shell prompt `local$`
for clarity.

This command sets up users for our example domain:

```
local$ upspin -project=example-project setupdomain -cluster -domain=example.com
```

and should print something like this:

---

```
Keys and config files for the server users
  upspin-dir@example.com
  upspin-store@example.com
were generated and placed under the directory:
  /home/you/upspin/deploy/example-project
If you lose the keys you can re-create them by running these commands
  $ upspin keygen -where /home/you/upspin/deploy/example.com/dirserver -secretseed dadij-lnjul-takiv-fomin.zapal-zuhiv-visop-gagil
  $ upspin keygen -where /home/you/upspin/deploy/example.com/storeserver -secretseed zapal-zuhiv-visop-gagil.dadij-lnjul-takiv-fomin
Write them down and store them in a secure, private place.
Do not share your private keys or these commands with anyone.

To prove that you@gmail.com is the owner of example.com,
add the following record to example.com's DNS zone:

  NAME  TYPE  TTL DATA
  @ TXT 15m upspin:a82cb859ca2f40954b4d6-239f281ccb9159

Once the DNS change propagates the key server will use the TXT record to
verify that you@gmail.com is authorized to create users under example.com.
To create the server users for this domain, run this command:

  $ upspin -project=example-project setupdomain -cluster -put-users example.com
```

---

Follow the instructions: place a new TXT field in the `example.com`'s DNS entry
to prove to the key server that you control the DNS record for the domain
`example.com`.
Once the DNS records have propagated, `you@gmail.com` will in effect be
administrator of Upspin's use of `example.com`.

As a guide, here's what the DNS record looks like in Google Domains:

![DNS Entries](https://upspin.io/images/txt_dns.png)

Regardless of the registrar you use, it should be clear how to add the TXT
records.

Once the TXT record is in place, the key server will permit you to register the
newly-created users that will identify the servers you will deploy (as well as
any other users you may choose to give Upspin user names within `example.com`).

In general, one adds and edits user records in the key server using the command

```
local$ upspin user -put
```

but for the particular case of adding the server's users, it's simpler to use a
special variant of the `setupdomain` command with by the `-put-users` flag.
Run this next:

```
local$ upspin -project=example-project setupdomain -cluster -put-users example.com
```

The command should print something like this:

```
Successfully put "upspin-dir@example.com" and "upspin-store@example.com" to the
key server.
```

At this point, you are ready to deploy the services on the cloud platform.

## Create instances and deploy servers

The next step is to deploy a set of servers running on the cloud.
This is done with the `upspin-deploy` command.

Before issuing the command, however, one must have installed gcloud and
kubectl, as part of setting up an account with Google Cloud Platform (GCP).
The steps for that are covered by [GCP documentation](https://cloud.google.com/sdk/).

Install the Google Cloud SDK by following
[these instructions](https://cloud.google.com/sdk/downloads).

Install the components Upspin needs:

```
local$ gcloud components install kubectl beta
```

Ensure `gcloud`is authenticatedby running


```
local$ gcloud auth login
local$ gcloud auth application-default login
```

The following command creates a raft of Google Cloud Platform services under
`example-project` and deploys binaries to the cloud, serving requests on
various hosts of domain example.com.

```
local$ upspin-deploy -create -domain=example.com -project=example-project
```

If the above is successful, there will be two new servers running on GCP: the
dir server and the store server.
They must now be bound to the DNS names within the domain, that is,
`dir.example.com` and `store.example.com`.
The details are printed at the tail of the output of `upspin-deploy`:

---

```
==== User action required ===

You must configure your domain name servers to describe the
new servers by adding A records for these hosts and IP addresses:
  104.23.111.421  dir.example.com
  104.174.130.916 store.example.com
```

---

Check that the servers are online and you can speak HTTPS to them:

  https://dir.example.com/

  https://store.example.com/

A working server should display this message:

  `invalid gRPC request method`

## Server access control

In their starting configuration, a store and dir server will allow any Upspin
user to write objects or create directories.
You should restrict write access to only the users who you intend to use these
servers.
This is achieved by creating access control files that the servers read.
Once those files are in place, access is restricted to the users listed in
those files (which must include the directory server's user, because the
directory server stores its data in the store server).
These files are `upspin-store@example.com/Group/Writers`, plus an `Access` file
that in turn makes that file visible to the directory server's user,
`upspin-dir@example.com`.

The easiest way to set this mechanism up is to use the `upspin setupwriters`
command.
The arguments list the users (or wildcards) to be granted access.
See the Access Control document for more information.

For instance, this command gives `you@gmail.com` plus every user with a user
name in the `example.com` domain permission to write to the store server and
create an Upspin tree in the directory server.
Of course, the command you run should list the users of your own servers.

```
local$ upspin -project=example-project setupwriters -domain=example.com you@gmail.com
```

The servers are now available for general use.
You might continue reading
[Signing up a new Upspin user](/doc/signup.md), if that's how you
got here.
