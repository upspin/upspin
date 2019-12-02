# Upspin Overview

## Introduction

Upspin provides a global name space to name all your files.
Given an Upspin name, a file can be shared securely, copied efficiently without
"download" and "upload", and accessed from anywhere that has a network
connection.

Its target audience is personal users, families or groups of friends.
Although it might have application in corporate environments, that is not its
motivation.

Upspin provides a uniform naming mechanism for all data, along with
easy-to-understand and easy-to-use secure sharing, as well as end-to-end
encryption that guarantees privacy.

Upspin is not an "app" or a web service, but rather a suite of software
components, intended to run in the network and on devices connected to it, that
together provide a secure information storage and sharing network.
Upspin is a layer of infrastructure that other software and services can build
on to facilitate secure access and sharing.

Upspin looks a bit like a global file system, but its real contribution is a
set of interfaces, protocols and components from which an information
management system can be built, with properties such as security and access
control suited to a modern, networked world.

The rest of this document describes the problem Upspin is aiming to solve,
provides an outline of its structure and how it works, and describes some uses
both familiar and novel.

## The problem

As information has moved from personal computers to networked servers, it has
also been separated from the people that use it.
Users have lost significant control of their data.
Instead of owning a CD or DVD, one pays to access a song or movie from a
provider such as Apple or Google, but the access can be rescinded if the user
loses the account.
Photos that one uploads to Facebook or Twitter are managed by, and effectively
belong to, those providers.

Other than occasional workarounds using a URL, information given to these
services becomes accessible only through those services.
If one wants to post a Facebook picture on one's Twitter feed, one does that by
downloading the data from Facebook and then uploading it to Twitter.
Shouldn't it be possible to have the image flow directly from Facebook to
Twitter?

This "information silo" model we have migrated to over the last few years makes
sense for the service providers but penalizes the users, those who create and
should therefore be in charge of their data.
There are surely advantages to hosting all one's photos on a network service
rather than maintaining the archive oneself, but those advantages come with a
significant loss of control.

The situation also means that which data is available to a user depends on the
device being used.
For example, without special prearrangement, the data stored on one's smart
phone is not visible from one's computer, and vice versa.

Another issue is sharing.
Users have little control over who can access their data; essentially the
choice is "public", where everyone can, or "private", where only the user can,
and the workaround for other scenarios is the old pattern of upload, download,
and maybe mail or reposting or some other *ad hoc* mechanism.

There is also a security question.
Although it makes sense to share data in the network, that usually means the
service provider has access to the information itself.
Users should be able to gain the advantages of networked services but maintain
a measure of privacy when they so desire, and the rise of end-to-end encryption
in services demonstrates a way forward.

All of these problems are in principle easy to solve.
If every item of interest had a unique name, and every person, server, PC or
phone could evaluate that name to access the item, these problems would fall
away.
Add to that proper end-to-end security and a coherent sharing model, and a
future with uniform, secure, ubiquitous access to data becomes feasible.
Download and upload, unauthorized access, siloed data, even email attachments
could become relics of the past.

Upspin is an attempt to enable this future by providing uniform access and
sharing methods for all of a user's data.
Data can be accessed by any authenticated entity, be that a user or a server in
the network, but only when the user grants explicit permission to that entity.

The rest of this document outlines the pieces of Upspin and how it addresses
the problems of the current information world.

## Users

Users in Upspin are identified by an email address that, within Upspin, is
called the user name.
When the user is first registered with the system, the address is used to
verify the user's identity; after that, it just serves as the user's
identifier, for example, `ann@example.com`.

Some email services provide a way to modify the user name to allow multiple
addresses to refer to the same user.
The usual syntax is to follow the user's name before the `@` with a plus sign
and a *suffix*.
Upspin uses this technique to allow a user to own multiple Upspin services.
For example, `ann+camera@example.com` might be the Upspin user name for an
Internet-connected video camera owned by `ann@example.com`.

User names with suffixes belong to their primary user (the one without the
suffix).
Primary users can therefore make changes to entries on the key server for their
name with a suffix.
For example, `ann@example.com` may change the keys and the store and directory
endpoints for `ann+camera@example.com`.
The converse, however, is not allowed: `ann+camera@example.com` cannot make
changes to `ann@example.com`.

User names with suffixes can be created only by their owners.
For this reason, they do not require a working email address.
Suffixed users are created using the `upspin` command.

For example, `ann@example.com` can create the user 'ann+testing@example.com`
by running:

```
$ upspin createsuffixeduser ann+testing@example.com
```

## Naming

Every Upspin file name has the same basic structure.
It begins with the user's name—an email address—followed by a slash-separated
Unix-like path name.

For example,

```
ann@example.com/dir/file
```

is an Upspin path name.
We say that `ann@example.com` is its *owner*.
As another example, the path name

```
ann@example.com/
```

is called the *user root* for `ann@example.com`, analogous to Unix's `$HOME`.
Looking at the full path, `ann@example.com/dir/file`, there is a directory
`dir` in the user root; it in turn contains the named file.

Any user with appropriate permission can access the contents of this file by
using Upspin services to evaluate the full path name.

Upspin names usually identify regular static files and directories, but the
naming scheme itself makes no guarantees about the kind of data served.
Some servers may offer dynamic content such as information generated by devices.

Also, some Upspin names identify *links*, which are analogous to Unix symbolic
links.
A link allows a name in one tree to refer to an item anywhere else in the
Upspin name space, including a different user's tree.
Thus links provide a way for a single user to organize into her own tree the
full set of Upspin names of interest.

Upspin has no concept of a local path name.
All Upspin path names are fully qualified, that is, they always begin with a
user name.

## Access

Upspin provides an easy-to-use model for sharing folders with any other Upspin
user.
The access control mechanism determines who can read, write, delete, or even
discover the existence of Upspin files.

A separate document [Access Control](/doc/access_control.md) describes it in
detail, but the basic idea is simple.

By default, nothing is shared.
When a user is added to Upspin, all data created by that user is invisible to
everyone else.

If the user wishes to share a directory (the unit at which sharing is defined),
she adds a file called `Access` to that directory.
In that file she describes, using a simple textual format, the rights she
wishes to grant and the users she wishes to grant them to.
For instance, an `Access` file that contains

```
read: joe@here.com, moe@there.com
```

allows `joe@here.com` and `moe@there.com` to read any of the files in the
directory holding the `Access` file, and also in its subdirectories.

If a subdirectory contains another `Access` file, from that subdirectory
downwards that `Access` file completely replaces the rights granted by the
parent's `Access` file.

The owner (the user whose email address begins the Upspin path name) always has
permission to read all the files in the owner's tree, as well as to update any
`Access` files within.

There is also a mechanism for defining groups of users for the purpose of
access control.
Full details are available in the access control document.

## Structure

Upspin is implemented by three key pieces, each a networked server that
provides a simple RPC interface to its clients.
The pieces are:

1) A key server.
This is the server that holds the public (not private!) keys for all the users
in the system.
It also holds the network address for the directory server holding each user's
tree.
For the global Upspin ecosystem, this service is provided by a server running
at `key.upspin.io`.
All users can save their public keys there, and can in turn ask the key server
for the public keys of any other user in the system.

2) A storage server.
These servers hold the actual data for the items in the system.
We expect there to be many storage servers, typically one or more per user or
perhaps family or organization.
Items are stored not by their name but by a *reference*, which for the default
implementation is a hash computed from the contents of the data.

3) A directory server.
These servers give names to the data held in the storage servers, on behalf of
the Upspin users.
Like storage servers, we expect there to be many directory servers.
Often, a directory server will hold only a single user's tree, or perhaps the
trees of a single family.
The Upspin naming model allows this to work well.
Moreover, the separation of storage and naming means that the directory
structure for a single user is a modest amount of data even when it references
many large files.

In summary, `key.upspin.io` provides a single place to store all public keys.
Users' actual data is stored across multiple directory and storage servers in
the network; these servers are run by the users themselves (or by agents on
their behalf).

A typical operation, say getting the contents of a file, is implemented using
these services.
For example, to read the file `ann@example.com/file`, the operation proceeds
like this:

*   Extract the user name of its owner, which is always the beginning of
the Upspin name: `ann@example.com`.
*   Look up `ann@example.com` in the key server at `key.upspin.io` to find the
network address of her directory server.
*   Contact that directory server and ask it to evaluate the full Upspin name.
It returns the network address of the storage server holding the contents, and
the references under which they are stored.
*   Contact the storage server to retrieve the data.

Of course this sequence is packaged into a library, called the *client*, that
is provided as part of the Upspin project.
It is also available through command-line tools or through a FUSE plugin for
Unix systems that turns the Upspin name space into a Unix file tree.

The storage and directory servers may be run anywhere, but we expect most to be
run as cloud services for easy availability, scalability, and maintenance.

The interfaces in Upspin make it easy to insert caching layers between the
client and servers to mitigate the cost of remote
network access.

## Security

Although Upspin allows a user to store and share data without encryption, we
expect most users will want their data to be protected from unauthorized access,
and so the system offers high security as the default.

There are two separate issues about security.
One is deciding who can access an item; that is the subject of the Access
section of this document.
Here we talk about a deeper guarantee, the promise that even if an intruder
breaks into the network servers, the user's data is still protected.

By default, the content of files stored in Upspin is encrypted.
(File metadata stored in the directory hierarchy, such as the file name and the
list of users permitted to read the file, is guarded only by access controls in
the directory server, as the server must be able to navigate the tree.) The
user keeps, in a private location not part of Upspin, a key that is used both
during encryption of the data before it is written, and during decryption when
read back.
Both the encryption and decryption happen on the user's client machine, not in
the network or on Upspin servers.
This is called end-to-end encryption, and prevents a snoop
from being able to read the user's data by tapping the network or the
storage server.

To share a file with a second user, that user must also be able to decrypt it.
Upspin handles this automatically, using encryption techniques that allow two
users to share encrypted data without disclosing their private keys to each
other.
The public keys of all users are registered in a central server to enable
sharing even between strangers.

The details of the encryption algorithm and the security guarantees they can
make are described in a separate [Upspin Security document](/doc/security.md).

Upspin also provides authentication mechanisms to block unauthorized users from
accessing the network servers in the first place.
The solution to this is also covered in the security document.

## Examples

With the software components, protocols, and security mechanisms Upspin
provides one can construct secure, shared, distributed information systems.
An obvious example is a distributed file system, and the reference
implementation of the system provides exactly that: a global collection of
uniquely-named files that can be read, written, and shared securely.
It also provides, for Unix systems at least, a mechanism (the `upspinfs`
daemon) to connect that distributed file system to the local file tree.

But it is important to understand that Upspin can name information from any
data service, not just traditional files.
In this section we mention a few possibilities, but many more can be imagined.

First, due to the content-addressable form of the standard storage server's
references for static storage, the server can provide a simple, efficient
mechanism for backups, called a snapshot, and present it as an Upspin service
alongside the data it is preserving.
In effect, a snapshot provides a historical view of the tree at regular
intervals, so a user can view the data from the past using regular Upspin (or
other) tools.
It is presented by the same server and constructed automatically, and cheaply,
by building a secondary tree of historical roots of the user's tree, named by
date.
It is identified by the suffix `+snapshot` attached to the user name.

For example, `ann@example.com` would be able to access her tree from December
3, 2016 through the Upspin tree rooted at

```
ann+snapshot@example.com/2016/12/03/
```

The files in that tree are not copies of the originals, but immutable
references to the same files as they appeared on that date.

The APIs for Upspin are easy to implement, making it simple to transform
existing data services into named, file-like data that can be joined to the
Upspin (or through `upspinfs`, Unix) name space.
One could imagine simple but convenient connectors to do things like provide a
Twitter feed through a file system interface, or a greppable issue tracker for
GitHub bug reports, or an aggregator for music or video that provides a unified
view of all of one's entertainment subscriptions.

As a more dynamic example, earlier we mentioned the idea of a connected device
such as a video camera.
The owner of the camera could register a special user to host the camera or, as
with the snapshot, serve it through a suffixed name like
`ann+camera@example.com`.
The full path might be `ann+camera@example.com/video.jpg`, with the idea that
every read of the file retrieves the most recent frame.

## What is included

Upspin is an open source project, so naturally all the source code is available.
(See the section on contributing if you are interested in helping to develop
the system.)

That source serves several purposes.
It includes definitions of the fundamental interfaces in the system (see the
file `upspin/upspin.go`).
Any service that implements one of the server interfaces called `KeyServer`,
`DirServer`, or `StoreServer` can participate in the Upspin ecosystem.
The source tree includes reference implementations of all three of these, of
course, and these are what is run at `upspin.io`.

The source tree also includes a couple of unusual implementations that use the
Upspin interfaces to provide access to services not usually thought of as
"files".
These include the snapshot mechanism, integral to the storage server and
described in the previous section, as well as a few experimental
components in the directory `upspin.io/exp`.

Finally, the system includes a cache server that interposes between the remote
servers and the local client.
This implement exactly the same interfaces, so its existence is transparent to
the client.

At `key.upspin.io` we run a `KeyServer` and allow authenticated users to
register there and share in that name space.
We encourage anyone interested in using the system to register.

For the `DirServer` and `StoreServer`, you will need to deploy your own
instances.
If you want to use the reference implementations, you can run your own instance
of `upspinserver` by following
[the server setup instructions](/doc/server_setup.md).
Or you could write your own (and we encourage you to think about doing so).
It's up to you.
Upspin is just a way to access things; what and where those things are is your
decision.

## Signing up

To join the Upspin system, a user needs to have a set of keys to access the
servers.
The private key is kept private and is the user's responsibility to save and
maintain.
The public key on the other hand must be made public so all participating
servers can authenticate the user.
The public key is made truly public by creating a user record to hold it in the
key server at [key.upspin.io](https://key.upspin.io).
The process for doing this is described in a
[separate document](/doc/signup.md).

Once registered in the key server, the user can access existing content stored
in Upspin, assuming permission is granted.
To store new data requires setting up a place to store it, that is, finding or
running Upspin directory and store servers to host the data.
To do so may require working with friends or family, signing up to an
organization that runs Upspin servers for outside users, or launching personal
instances on local machines or cloud systems.
The addresses of the user's servers are then stored in the public key server's
record for the user, at which point the user can start saving data in the
system.
Again the details are presented [separately](/doc/signup.md).

## Comparison with existing systems

Upspin has a number of similarities and differences when compared to other
systems that aim to give users secure shared access to data.
Rather than make explicit comparisons to such systems, this section focuses on
the salient or unusual aspects of its design that, when put together, make
Upspin unique.

The fundamental purpose of Upspin is *universal* access to *secure*, *sharable*
data.
Those three italicized terms all matter.

*Universal* means that no single entity maintains the data; Upspin is in effect
a federation.
(Key management works well with a single space of keys, but it is not a
requirement that a single server host all the keys; the current single server
can and likely will develop mirrors and replicas.) Any Upspin user can, with
permission, access any item on any Upspin server.

*Secure* means data can be, and usually will be, protected through end-to-end
encryption that not only guards it from prying eyes, but provides a controlled,
safe mechanism for sharing, by giving keys only to those who have the right to
unlock the data.

*Sharable* means that there is an easy-to-use, easy-to-understand mechanism for
sharing items with individuals, groups, or the public.
Despite the fine-grained nature of the access permission model, it's always
easy to see who is allowed to access a file, easy to grant access, and easy to
revoke it.
Many systems provide either all-or-nothing models (public and private), or
allow fine-grained sharing but in a way that is not visible as part of the data
space itself, and not enforced by end-to-end encryption.

The target user for Upspin is also unusual.
Upspin is intended for securing and sharing personal data, and is not designed
for corporate information systems.
Although it could be used for corporate work, the fine-grained sharing model
and use of individual keys could make that clumsier than it would be for most
commercial systems.
For individuals or small groups, however, Upspin works well.

Also, Upspin's system architecture is unusual.
It is at its core just a set of simple APIs that anyone may implement; it is
not a data service one must acquire from a particular provider.
Although there are reference implementations that provide many of the features
we feel are central to the system, we expect and hope that many other
implementations will arise, allowing a wide variety of information services to
coexist in the Upspin space.

One particularly distinct element of Upspin's design is the separation of
directory and storage servers.
This separation has a number of properties, most important of which is that it
guarantees that the directory servers are not granted any access to users' data
beyond knowing where it is located.
Directory servers never even see user's data, even unencrypted data, other than
file names and access permission information.
A user's data need not even be hosted by the same organization that hosts the
user's directory.

## Installing and Contributing

The source for Upspin is hosted on Gerrit
([upspin.googlesource.com](https://upspin.googlesource.com))
and mirrored to GitHub.
Install Go if you haven't
already ([golang.org/dl](https://golang.org/dl/)) and then run

```
$ git clone https://upspin.googlesource.com/upspin
$ cd upspin
$ go install cmd/...
```

There are useful auxiliary commands and interfaces to various cloud storage
providers in ([upspin.googlesource.com](https://upspin.googlesource.com)).
Guidelines for contributing back to the project are in a
[separate document](https://github.com/upspin/upspin/blob/master/CONTRIBUTING.md).
