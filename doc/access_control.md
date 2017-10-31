# Upspin Access Control

## Introduction

This document describes the access control model for Upspin.

All such information is maintained by files in the Upspin name space itself.
In particular, all the information necessary to decide the accessibility of an
item in the tree under user U's root is available to the Directory server
holding U's root.

Despite the length of this document, the general model is very simple.
Plain text files describe what rights are granted, saying for instance that a
given user may read files.
These rules apply at the directory level and are inherited by subdirectories.
By default, with no such access control files in a user's tree, that user and
only that user has the right to read or modify the files.

## Users

By user, we mean an account known to the Upspin Key service, identified by an
e-mail-like name: `ann@example.com`.
Each valid user has a *user* *root* directory held on at least one Directory
server.
Each user proves identity to Upspin servers,
their own and others',
using the user's key pair registered in the central Key server.

## Groups

A group identifies a list of users, the *members* of that group.
Each group is associated with a single user, its *owner*, and the owner is
implicitly a member of every group that the user owns.

The membership of a group is defined by the contents of a file in the Group
subdirectory of the owner's user root, and the path name of that file is the
global name of the group.
Within that file is a list of the members, separated by white space and/or
commas.

For example, `ann@machine.com` might define a group for her family.
She would define that group by creating a file, say
`ann@machine.com/Group/family`, and writing to it something like,

```
bob@gmail.com
ricardo@example.com
grandma@example.com
```

Once that file is created, the group called `ann@machine.com/Group/family` is
defined to contain those users, plus `ann@machine.com` herself, as members.

The full name `ann@machine.com/Group/family` is cumbersome, but as we will see
in the next section, when `ann@machine.com` wants to identify this group, it
will usually be in the context of her own directory tree, and just the final
component, `family`, is sufficient to identify it.

`Group` files are always readable and writable by the owner, and only the owner
can create and edit them, but otherwise they act as regular items within the
Upspin name space as far as client I/O is concerned.

`Group` files can be placed directly in the `Group` directory of the user's
root, or in subdirectories of that `Group` directory.
The name of the group will always be the full Upspin path, including user name,
of the group definition file.
The advantage of using subdirectories is that the access control mechanisms of
Upspin, which operate at the directory level (see the next section), make it
possible to have a particular group's membership be public or private as
appropriate.
`Group` files are plain files in all respects.

## Access control model

Absent any other information, every item in the user's Upspin tree is readable
and writable only by the owner, that is, the user whose root begins the path
name of the item.
By default, then, `ann@example.com/foo/bar` is a file owned and accessible only
by `ann@example.com`.
However, the access rights may be modified by the presence of an access control
file in the directory `foo` that holds `bar`, or by an access control file in
`foo`'s parent, and so on.

Access control files may be placed in any directory, including a user root.
They define the access rights that apply to the directory itself, its contents,
its subdirectories, and so on, recursively.
However, if an access control file exists in a directory, the access rights it
grants completely override those granted through the recursive inheritance
mechanism, with some special exceptions for the owner.
These exceptions are described below.

Access control files are named exactly `Access`.
Like `Group` files, they are plain text files and are stored in the owner's
Upspin tree, only the owner may write them, and read access to the `Access`
files themselves is granted by the Upspin access control mechanisms described
here.
The details about the format of the files are presented below; in this section
we concentrate on the model itself.

As an example, if `ann@example.com` creates a file `ann@example.com/Access`
that grants read and "list" (directory search) access to
`ann@example.com/Group/family`, initially anyone in her `family` group can see
the contents stored under any path name under `ann@example.com/`, including the
`Access` file itself.
However, if she creates a directory `ann@example.com/secret`, places a file in
it called `ann@example.com/secret/Access`, and in that file gives only herself
access permission, none of her family would be able to see the items in the
`secret` directory or its subdirectories.
The rights granted by the `Access` file in the `secret` directory would
override rights granted by the one in the user root.

The family would however know the existence of the `secret` directory, since it
lives in a directory with an `Access` file granting permission to search the
directory.
The directory itself could however be hidden by placing it one directory level
deeper, as in `private/secret`, and placing the restricted `Access` file in the
private directory.
Then the family would know about the existence of the `private` directory but
not the `private/secret` one.

Here is what the tree for that example would look like:

```
ann@example.com/
    User root
ann@example.com/Access
    Provides read and list access, granting access to family
ann@example.com/private
    In same directory, so visible to family
ann@example.com/private/Access
    Restricts access to `ann@example.com` only; family cannot see inside
ann@example.com/private/secret
    Invisible to family
ann@example.com/private/secret/documents
    Invisible to family
```

`Access` files may name any user or group in the Upspin system, including
groups defined by owners other than the `Access` file's owner.
That is, an `Access` file may identify a group from another server altogether;
`ann@example.com` may wish to grant access to `bob@gmail.com` or to
`bob@gmail.com/Group/family`, his family group.

As a convenience, if an `Access` file names a group whose owner is the `Access`
file's owner, which we expect to be the common case, the prefix up to `/Group/`
may be elided from the entry in the file.
For example, inside the top-level `Access` file mentioned above, the name
`family` could be used as a shorthand for `ann@example.com/Group/family` and
`work/friends` as a shorthand for `ann@example.com/Group/work/friends`.

As mentioned above, there are expansions of the access rules for the owner.
For items in the owner's own tree, the following rights are granted:

*   any file can be read
*   any directory can be listed (its contents can be viewed)
*   any `Access` or `Group` file can be created, read or modified

Moreover, only the owner is allowed to create or modify an `Access` or `Group`
file regardless of the rights granted by `Access` files.

All other rights for the owner are defined by the contents of the `Access`
files.

Encrypted packings (described in the [Upspin Security document](/doc/security.md))  in
Upspin also have the effect of limiting who can read file contents, by only
wrapping the file decryption key for certain readers.
The intent is that this list of readers derives from the `Access` file, and
will be semi-automatically updated when the `Access` file is changed or when
readers' public keys are changed, on an as-available basis.

Note that, unlike for instance in Unix, the rights for a file and its directory
are defined completely by the Access file that applies, regardless of rights
closer to the root.
For instance, if a user has access to read a file named (ignoring the owner
name) `/a/b/file` as specified by `/a/b/Access`, that right is granted even if
the directories `/`, `/a` or `/a/b` are not listable by that user.
Moreover, the full name `/a/b/file` is visible to that user regardless of the
rights in the parent directories.
Thus one may give access to a file or directory without providing access to the
intervening directories (other than, of course, the right to know the full
path).

## Format of Access files

Each permission granted by an `Access` file gives specific rights to an
associated list of users and groups.
There are two separate sets of rights, one for directories and one for plain
items, that is, files.
For files the rights are:

*   Read: The right to see the file's contents; to be specific, the right to
discover the Store server references bound to a name.
*   Write: The right to replace the file's contents; to be specific, the right
to replace the Store server references bound to a name.
Because of the semantics of I/O in Upspin, this is always wholesale replacement.

For directories, the rights are:

*   Create: The right to add new items (except `Access` and /`Group/` files;
these are always owner-only), including subdirectories, to the directory.
*   List: The right to see a directory's contents.
This comprises the right to see the names of the items contained in the
directory, their public properties such as size, and the list of users that can
access them.
It does not grant the right to know where the storage for the items resides;
that is granted by the Read right.
If a directory denies List access, the directory's own name and properties will
still be visible in its parent directory.
*   Delete: The right to delete items from the directory.
As a special precaution, a directory must be empty before it can be deleted.

Note there is no such thing as execute permission in the manner of Unix.
Some implementations may choose to interpret Read as execute permission, but
none is required to do so.
Upspin has no concept of "execute".

Each line of an `Access` file has two colon-separated fields (white space is
ignored across the line).
The first is the name of a right, the second is a space- or comma-separated
non-empty list of users, groups, or wildcards (described in the next section).
The rights are spelled Read, Write, Create, List and Delete, are
case-insensitive, and may be abbreviated to the first character (upper- or
lower-case R,W, C, L, or D).
Also, a set of rights may be comma-separated for grouping.
An example:

```
r: family, bob@example.com
w,c,list: family
```

This example defines that anyone in the family, plus `bob@gmail.com`, has
permission to read items, but only the family is allowed to write items, to
create new items, or to see what items are present.
Because there is no delete right list in this example, no one is allowed to
delete items from this directory, even the owner (except that
`ann@example.com`, the owner of this `Access` file, can as always delete the
`Access` file itself or update it to provide delete access).
These rights override any granted by higher-level `Access` files.
In particular, even though there is no explicit delete right granted here, this
Access file defines that no one has delete rights in this directory, regardless
of what higher-placed `Access` files may say.

## Wildcards

Inside Access and Group files, the wildcard character * (asterisk) means "all
rights".
Thus one can say

```
*: family
```

as a shorthand for

```
read: family
write: family
list: family
create: family
delete: family
```

The user name `all` (case is ignored) means "any authenticated Upspin user".
The asterisked user name *@example.com means any authenticated user whose
account is in the `example.com` domain.

To allow anyone with an Upspin account to read items, this line in the relevant
Access file

```
read: all
```

will serve; to allow anyone to do anything (which is unwise!),

```
*: all
```

The "`all`" wildcard has a couple of restrictions, to make it harder to
introduce it accidentally.
First, it must be the only user mentioned on the line within the Access file.
Also, to make sure that someone placing a group name in an Access file doesn't
unintentionally publish data to the world it is not permitted anywhere in Group
files.

As a side note: a user-name wildcard such as `*@example.com` applied to the
read right can only provide genuine read access if the item being read is not
encrypted, or if every user in the domain has a key wrapped for the item (see
the [Upspin Security document](/doc/security.md)), which is impractical at best.
In future, Upspin may provide a mechanism for some sort of key mechanism that
would allow encrypted files to be accessible by everyone in an organization,
but that has not been done.

## Encoding and access for Access and Group files

`Group` and `Access` files are plain UTF-8-encoded text files and are always
readable by anyone with permission to access them, and by the servers that enforce the
permissions they grant.

Also, if an `Access` or `Group` file in user U's tree mentions a `Group` file
from user V's tree, user V must explicitly grant public read access for the
`Group` file there so that U's tree, which is running as some other,
administrative user, can read V's `Group` file.
As an example, if ann@machine.com has a Group file that names the group
`bob@example.com/Group/public/knittingcircle`, then `bob@example.com` should
add a file `bob@example.com/Group/public/Access` granting public read access to
that directory.
That `Access` file could say just

```
read: all
```

which would declare that the group is publicly known.
In this example we put the `Group` file in a public subdirectory.
That is not required—`public` is not a special name—but is a good convention.

In practice, we expect most groups to be local to the owner's tree, with no
need for explicit access controls except for the occasional public group such
as a social circle.

## Errors and discoverability

One goal of the design of access controls in Upspin is that a user cannot
easily discover valid names in the Upspin name space unless granted permission
to do so.
As a result, under some circumstances operations return "private" errors rather
than "permission denied" errors if the operation fails.

Generally, if an operation fails because the user has no access rights at all
in the corresponding directory, the operation returns an error that means
"information withheld for privacy reasons".
If the user has some access rights but not those required, the operation
returns "permission denied".

There is one special case supporting this model.
For the `Glob` (directory search) operation, if permission is not granted to
see a particular item, rather than return "permission denied" the operation
simply elides the offending item's information from the returned list.

## Links

The presence of links affects the access control mechanisms because the owner
must also grant the right to indirect through the link.

If the evaluation of an Upspin name reaches a link node, the Directory server
holding the link entry returns the `DirEntry` (the data structure in the API
that describes the item stored with a given name) for the link itself, with the
special error code `ErrFollowLink`.
The caller can then take the `Link` field from the returned `DirEntry` and
retry the original operation with that path, again subject to access controls.
(These operations are handled automatically by the Upspin client library.)

To step through a link this way, the user must have some access right for the
link itself.
Any right will do (`List`, `Read`, `Write`, `Create`, or `Delete`).
 The reason that any right is sufficient to grant access is that the caller
might be evaluating the name for any operation, and the access controls for the
link should be consistent.
Also, it simplifies the implementation to allow the fine-grained check to
happen once evaluation reaches the final, non-linked name.

If the caller has no access rights for the link, the error returned is an
"information withheld" error, hiding the existence of the link (and its target)
from the caller.
That is, if the caller has no permission to see the link, the caller cannot
discover that the link exists.

## Snapshots

Snapshots, which are trees that provide a backup mechanism in the reference
implementation of the Directory server, have special access control rules.
The snapshot of the tree for `ann@example.com` has root
`ann+snapshot@example.com.` Typically `ann+snapshot@example.com` will have the
same keys as `ann@example.com` so `ann@example.com` can decrypt items stored in
her snapshot.

Only the owner of a snapshot (In this case, `ann@example.com` or
`ann+snapshot@example.com`) can access the tree or its contents.
Moreover, even the owner has limited rights because the snapshot tree is
read-only: the tree is maintained and updated by the server but cannot be
modified with Upspin calls to the `DirServer`.

For a brief discussion of user names and + suffixes, see the
[Overview document](/doc/overview.md)'s section on users.

## Appendix: Summary of access rules

The details of which rights are checked for which operations are summarized in
this section.

DirServer operations and the rights they check are:

*   Lookup
    *   The caller needs Read access to see full information, including storage
references.
    *   If the caller has some rights but not Read, returned `DirEntry` has
empty `Blocks` and `Packdata` fields, hiding where the data is stored.
    *   If the caller has no rights, an "information withheld" error is
returned.
*   Put (including making directories and links)
    *   If the entry does not exist, the caller needs Create access.
    *   If the entry does exist, the caller needs Write access.
    *   If the entry is for an existing directory, Put always fails.
*   Glob
    *   Caller needs List access for every directory whose entries match a
wildcard in the argument pattern.
        *   For instance, given u@google.com/a/*/b the caller needs List access
for directory 'a' because Glob will search to match the '*'.
    *   Given the list of matching entries, the caller is only permitted to see
those for which the caller has List access.
    *   If the caller does not have Read access for the returned entries, the
corresponding DirEntries have empty Blocks and Packdata fields.
*   Delete
    *   Caller needs Delete access.
*   WhichAccess
    *   Caller needs any right (any of Read, Write, Create, List, Delete) for
the entry.
    *   If the caller does not have Read access for the Access file itself, the
returned `DirEntry` has empty `Blocks` and `Packdata` fields.

As always, if the name steps through a link, the caller must have some access
rights for the link entry itself.

The owner has special rights regarding items in the owner's tree:

*   any file can be read
*   any directory can be listed (its contents can be viewed)
*   any `Access` or `Group` file can be created, read or modified

For snapshots, once the snapshot tree is initialized it behaves as if the tree
has an `Access` file with (for `ann+snapshot@example.com`):

```
ann@example.com, ann+snapshot@example.com: list,read
```

and the owner's special rights for `Access` and `Group` files is rescinded.
