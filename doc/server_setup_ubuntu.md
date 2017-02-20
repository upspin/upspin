# Running `upspinserver` on Ubuntu 16.04

These instructions are part of the instructions for
[Setting up `upspinserver`](/doc/server_setup.md).
Please make sure you have read that document first.

## Introduction

These instructions assume you have access to an Debian or Ubuntu linux
server, and that the server is reachable at your chosen host name.
(`upspin.example.com`)

You can create a suitable VM in your Google Cloud Project by visiting the
Compute section of the [Cloud Console](https://cloud.google.com/console) and
clicking 'Create VM'. (The `n1-standard-1 machine type should be sufficient.)

> Note that these instructions have been verified to work against Ubuntu 16.04.
> The exact commands may differ slightly on your system.

Once the server is running you should log in to it as root and configure it to
run `upspinserver` by following these instructions.

## Create a user for `upspinserver`

The following commands must be executed on the server as the super user, `root`.

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
$ cd $HOME
$ mkdir .ssh
$ chmod 0700 .ssh
$ cat > .ssh/authorized_keys
(Paste your SSH public key here and type Control-D and Enter)
```

## Build `upspinserver` and copy it to the server

From your workstation, run these commands:

```
$ GOOS=linux GOARCH=amd64 go build upspin.io/cmd/upspinserver
$ scp upspinserver upspin@upspin.example.com:.
```

## Run `upspinserver` on server startup

The following commands must be executed on the server as the super user, `root`.

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
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

> Note that here we pass the flag `-https=localhost:8443` to the server,
> instructing it to listen on a high port on `localhost`.
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

## Redirect port `443` to `localhost:8443`

The following commands must be executed on the server as the super user, `root`.

Install the `xinetd` server, which will run as `root` listening on port `443`
and redirecting requests to our `upspinserver` on `localhost:8443`.

```
$ apt-get install xinetd
```

Create a file `/etc/xinetd.d/upspinserver` with this contents:

```
service upspinserver
{
        disable         = no
        flags           = REUSE
        wait            = no
        user            = root
        socket_type     = stream
        protocol        = tcp
        port            = 443
        redirect        = localhost 8443
        log_on_success  -= PID HOST DURATION EXIT
}
```

Open `/etc/services` and find the line for the `https` service.
It should look something like this:

```
https	443/tcp
```

Then append the string `upspinserver` to that line, so it looks like this:

```
https	443/tcp	upspinserver
```

Finally, restart `xinetd` to enable this configuration:

```
$ systemctl restart xinetd.service
```

## Continue

You can now continue following the instructions in
[Setting up `upspinserver`](/doc/server_setup.md).
