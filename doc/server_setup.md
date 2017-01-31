# Setting up `upspinserver`

## Introduction

This document describes how to create an Upspin installation by deploying
`upspin.io/cmd/upspinserver` to a Linux-based server.

It assumes that you will be storing your data in Google Cloud Storage,
a restriction that may be relaxed in time.

The process follows these steps:

- Sign up for an Upspin user account,
- Configure a domain name and create an Upspin user for the server,
- Create a Google Cloud Project and set up a Google Cloud Storage bucket,
- Deploy `upspinserver` to a Linux-based server,
- Configure `upspinserver`.

Each of these steps (besides deployment) has a corresponding `upspin`
sub-command to assist you with the process.

## Pre-requisites

To deploy an upspinserver you need to decide on values for:

- A domain name that you control to use for the Upspin installation.
  (We will use `example.com` in this document.)
  Note that the domain need not be dedicated to your Upspin installation; it
  just acts as a name space inside which you can create Upspin users for
  administrative purposes.

- Your Upspin user name (an email address).
  (We will use `you@gmail.com` in this document.) 
  This user will be the administrator of your chosen domain.
  The address may be under your chosen domain, provided you have a working
  email service for that address.

- The host name of the server on which `upspinserver` will run.
  (We will use `upspin.example.com` in this document.)
  Note that this need not be a host inside your chosen domain, but it often is.

## Sign up for an Upspin account

Run `upspin signup` passing your chosen host name as its `-dir` and `-store`
arguments and your chosen Upspin user name as its final argument,
and follow the onscreen instructions.

For example:

```
$ upspin signup -dir=upspin.example.com -store=upspin.example.com you@gmail.com
```

The [Signing up a new user](/doc/signup.md) document describes this process in
detail.

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

and should print something like this:

```
$ upspin setupdomain -domain=example.com

Domain configuration and keys for the user
  upspin@example.com
were generated and placed under the directory:
  /home/you/upspin/deploy/example.com

To prove that you@gmail.com is the owner of example.com,
add the following record to example.com's DNS zone:

  NAME  TYPE  TTL DATA
  @ TXT 15m upspin:a82cb859ca2f40954b4d6-239f281ccb9159

Once the DNS change propagates the key server will use the TXT record to verify
that you@gmail.com is authorized to register users under example.com.

After that, the next step is to run 'upspin setupstorage'.
```

Follow the instructions: place a new TXT field in the `example.com`'s DNS entry
to prove to the key server that you control the DNS record for the domain
`example.com`.
Once the DNS records have propagated, `you@gmail.com` will in effect be
administrator of Upspin's use of `example.com`.

As a guide, here's what the DNS record looks like in Google Domains:

![DNS Entries](/images/txt_dns.png)

Regardless of the registrar you use, it should be clear how to add the TXT
records.

Once the TXT record is in place, the key server will permit you to register the
newly-created users that will identify the servers you will deploy (as well as
any other users you may choose to give Upspin user names within `example.com`).
At a later step, the `upspin setupserver` command will register your server
user for you automatically.

## Set up Google Cloud services

### Create a Google Cloud Project

First create a Google Cloud Project and associated Billing Account by visiting the
[Cloud Console](https://cloud.google.com/console).
(See the corresponding documentation
[here](https://support.google.com/cloud/answer/6251787?hl=en) and
[here]([https://support.google.com/cloud/answer/6288653?hl=en)
for help.)

Then, install the Google Cloud SDK by following
[the official instructions](https://cloud.google.com/sdk/downloads).

Finally, use the `gcloud` tool to ensure that the required APIs are enabled:

```
$ gcloud components install beta
$ gcloud auth login
$ gcloud --project <project> beta service-management enable iam.googleapis.com storage_api
```

### Create a Google Cloud Storage bucket

Use the `gcloud` tool to obtain "application default credentials" so that the
`upspin setupstorage` command can make changes to your Google Cloud Project:

```
$ gcloud auth application-default login
```

Then use `upspin setupstorage` to automatically create a storage bucket
and an associated service account for accessing the bucket:

```
$ upspin -project=cloud-project-id setupstorage -domain=example.com example-com-upspin
```

Note that the bucket name must be globally unique among all Google Cloud
Storage users, so it is prudent to include your domain name in the bucket name.

## Set up a server and deploy `upspinserver`

These instructions assume you have access to an Debian or Ubuntu linux
server, and that the server is reachable at your chosen host name.
(`upspin.example.com`)
You can create a suitable VM in your Google Cloud Project by visiting the
Compute section of the [Cloud Console](https://cloud.google.com/console) and
clicking 'Create VM'.

Once the server is running you should log in to it as root and configure it to
run upspinserver by following these instructions.

### Create a user for `upspinserver`

Create a Unix account named `upspin`.
For the password, use a secure password generator to create a long, unguessable
password.
The rest of the questions it asks should have sane defaults, so pressing
Enter for each should be sufficient.

```
$ adduser upspin
```

Give yourself SSH access to the server (a convenience):

```
$ su upspin
$ cd 
$ mkdir .ssh
$ chmod 0700 .ssh
$ cat > .ssh/authorized_keys
(Paste your SSH public key here and type Control-D and Enter)
```

### Build `upspinserver` and copy it to the server

From your workstation, run these commands:

```
$ GOOS=linux GOARCH=amd64 go build upspin.io/cmd/upspinserver
$ scp upspinserver upspin@upspin.example.com
```

### Run `upspinserver` on server startup

These instructions assume that your Linux server is running `systemd`.

Create the file `/etc/systemd/system/upspinserver.service` that contains
the following service definition.

```
[Unit]
Description=Upspin server

[Service]
ExecStart=/home/upspin/upspinserver -https=localhost:8443
User=upspin
Group=upspin

[Install]
WantedBy=multi-user.target
```

> Note that here we pass the flag `-https=localhost:8443` to the server,
> instructing it to listen on a high port on localhost.
> While we do want our `upspinserver` to serve requests port `443` on our
> public IP address, only the `root` user can bind to ports below `1024` and we
> don't want to run upspinserver as the super user.
> In the next section of this document we will configure the server to redirect
> requests to port `443` to `localhost:8443`.
		
Use `systemctl` to enable the service:

```
$ systemctl enable /etc/systemd/system/upspinserver.service
```

Use `systemctl` to start the service:

```
$ systemctl start upspinserver.service
```

You may also use `systemctl stop` and `systemctl restart` to
stop and restart the server, respectively.

You can use `journalctl` to see the log output of the server:

```
$ journalctl -f -u upspinserver.service

```

### Redirect port `443` to `localhost:8443`

TODO: describe how to do this with xinetd

### Test connectivity

Using your web browser, navigate to the URL of your `upspinserver`
(`https://upspin.example.com/`).
You should see the text:

```
Unconfigured Upspin Server
```

If the page fails to load, check the server logs for clues using `journalctl`
as described above.


## Configure `upspinserver`

On your workstation, run `upspin setupserver` to send your server keys and
configuration to the `upspinserver` instance:

```
$ upspin setupserver -domain=example.com -host=upspin.example.com
```

This registers the server user with the public key server, copies the
configuration files from your workstation to the server, restarts the server,
makes the `upspin@example.com` root and puts the
`upspin@example.com/Group/Writers` file, and makes the Upspin root for
`you@gmail.com`.

It should produce output like this:

```
Successfully put "upspin@example.com" to the key server.
Configured upspinserver at "upspin.example.com:443".
Created root "you@gmail.com".
```

To reconfigure the `upspinserver` you can delete its state in
`/home/upspin/upspin/server`,
restart the `upspinserver`,
and re-run `upspin setupserver`.

## Use your server

You should now be able to communicate with your Upspin installation using the
`upspin` command and any other Upspin-related tools.

To test that you can write and read to your Upspin tree, first create a file:

```
$ upspin put you@gmail.com/hello
hello
^D
```

This command reads data from standard input and writes it to a file in the root
of your Upspin tree named "hello".
(Note that the `^D` shown above is entered by pressing Control-D and enter,
as a signal to the shell that we have reached the "end of file".)

Then read the file back, and you should see the greeting echoed back to you.

```
$ upspin get you@gmail.com/hello
hello
```

