# Upspin configuration: The config file

## Introduction

Every interaction with Upspin requires knowledge about the user: some or all of
the user's Upspin name, directory server, key server, security keys, and so on.
This information is described by a *configuration* that is by default stored in
a *config file* stored in `$HOME/upspin/config`.

The config file is short but its contents mediate all interactions with Upspin,
and although a user's config file is initially created by the `upspin` `signup`
command, it is sometimes necessary to adjust the configuration by editing the
file manually.
Also, for experts it is common to have multiple config files available that
describe different configurations used for administration or debugging.

The config file is therefore important enough to deserve a discussion about its
contents.
That is the purpose of this document.

## Format

A config file is a plain text file in
[YAML](https://en.wikipedia.org/wiki/YAML) format.
YAML is a simple format, and the information stored in the config file is
straightforward, so the result is very easy to understand.
Each line of the file is blank, a comment, or a line of the format

```
key: value
```

The keys identify settings, and there several defined:

* `username:` E-mail address of user.
* `packing:` Algorithm to encrypt or protect data.
* `keyserver:` Which key server to use.
* `dirserver:` Server that holds user's directory tree.
* `storeserver:` Server to write new storage.
* `cache:` Whether to use a local store and directory cache server.
* `secrets:` Directory holding private keys.
* `tlscerts:` Directory holding TLS certificates.

One can also specify values for flags used by various commands.
The syntax is:

```
cmdflags:
 command-name:
  flag-name: flag-value
  ...
```

The `cacheserver` and `upspinfs` commands honor these settings.
The flags must be in the command line flag set of the command
or will generate an error.
These flag values will supersede the value of any flags not
set to their default.
Thus one can override these settings in the command line.

Not all of these settings must be present.
In practice, you will likely need only `username`, `dirserver`, `storeserver`,
and `cache`.
The defaults for the other settings are usually fine.
Moreover, things like server addresses are multipart but can often be
simplified.
They are discussed in the next section.

Here then is what a typical config file might look like:

```
username: ann@example.com

dirserver: dir.example.com
storeserver: store.example.com
cache: localhost:8888
cmdflags:
 cacheserver:
  cachedir: /usr/augie/tmp
  cachesize: 5000000000
```

This should be mostly self-explanatory.

The following sections describe things in more detail.

## Format of server addresses

In general a server address in a config file comprises three elements: a
*transport*, a network *address*, and a network *port*.
The format is like this:

```
remote,dir.example.com:443
```

with a comma separating the transport and address and a colon separating the
address and port.

In practice, though, the transport and port are omitted because the default
transport, `remote`, defines a service provided across a network connection,
and the default port, 443, is the standard port for encrypted (TLS)
communications, as used by the HTTPS protocol.
Thus the server specification above can be shortened to

```
dir.example.com
```

Other than `remote`, the default, the only other transports are `inprocess`,
which defines a service in the process as the client and is typically used only
for debugging, and `unassigned`, which represents a server that does not exist.
These appear in config files only rarely, and only for expert use.

## Settings

This section describes the various settings available.

* The **`username`** setting is the e-mail address of the user and must be
present.

* The **`packing`** setting names an algorithm used to encrypt or otherwise
protect the data written to Upspin during a `Put` operation.
The default packing is `ee`, which stands for end-to-end encryption and is the
safest, securest packing.
Others are `plain`, which leaves the data untouched, and `eeintegrity`, which
like `plain` leaves the data untouched but adds an end-to-end integrity check
that can detect tampering.
If the packing is not set in the config file, `ee` is assumed.

* The **`keyserver`** setting names the key server used to discover other
user's public keys.
Almost always, this will be to the Upspin global key server, `key.upspin.io`,
and so if the keyserver is not named in the config file, that is the default.

* The **`dirserver`** setting names the directory server holding the user's
Upspin directory tree.
It must be set.

* The **`storeserver`** setting names the storage server to which to write any
new data created by the user.
It must be set.

* The **`cache`** setting specifies a local cache server that speeds up
interactions with Upspin by caching directories and storage blocks.
If the cache is not set by the config file, none is used.
The value can be:
	* `y[es]` to run a cache server on a default address
	* `n[o]` to not run a cache server
	* the address of the cache server (normally used for debugging)

	For more information about the cache server, run

```
go doc upspin.io/cmd/cacheserver
```

* The **`secrets`** setting identifies a directory in which the user's public
and private keys are stored.
If not set, the keys are assumed to live in the directory `.ssh` within
the user's home directory (on Unix, `$HOME/.ssh`).

* The **`tlscerts`** setting identifies a directory in which to locate TLS
certificate root authorities.
If not set, the system uses the local operating system's default set of root
authorities, which is usually a larger set than required.
