// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Code generated by upspin gendoc. DO NOT EDIT.
// After editing a command's usage, run 'go generate' to update this file.

/*


The upspin command provides utilities for creating and administering
Upspin files, users, and servers. Although Upspin data is often
accessed through the host file system using upspinfs, the upspin
command is necessary for other tasks, such as: changing a user's
keys (upspin user); updating the wrapped keys after access permissions
are changed (upspin share); or seeing all the information about an
Upspin file beyond what is visible through the host file system
(upspin info). It can also be used separately from upspinfs to
create, read, and update files.

Each subcommand has a -help flag that explains it in more detail.
For instance

	upspin user -help

explains the purpose and usage of the user subcommand.

There is a set of global flags such as -config to identify the
configuration file to use (default $HOME/upspin/config) and -log
to set the logging level for debugging. These flags apply across
the subcommands.

Each subcommand has its own set of flags, which if used must appear
after the subcommand name. For example, to run the ls command with
its -l flag and debugging enabled, run

	upspin -log debug ls -l

As a shorthand, a path beginning with a plain @ refers to the current
user's root (ann@example.com), while one starting @+suffix is the
same with the suffix included (ann+suffix@example.com).

For a list of available subcommands and global flags, run

	upspin -help

Usage of upspin:
	upspin [globalflags] <command> [flags] <path>
Upspin commands:
	shell (Interactive mode)
	config
	countersign
	cp
	createsuffixeduser
	deletestorage
	get
	getref
	info
	keygen
	link
	ls
	mkdir
	put
	repack
	rm
	rotate
	setupdomain
	setupserver
	setupstorage
	setupwriters
	share
	signup
	snapshot
	tar
	user
	watch
	whichaccess
Global flags:
  -blocksize size
    	size of blocks when writing large files (default 1048576)
  -cachesize bytes
    	max disk bytes for cache (default 5000000000)
  -config file
    	user's configuration file (default "/home/user/upspin/config")
  -log level
    	level of logging: debug, info, error, disabled (default info)
  -prudent
    	protect against malicious directory server
  -version
    	print build version and exit
  -writethrough
    	make storage cache writethrough


Sub-command audit

Upspin-audit provides subcommands for auditing storage consumption.

The subcommands are:

scan-dir
scan-store
	Scan the directory and store servers, creating a list of blocks
	each uses, and report the total storage held by those blocks.

find-garbage
	Use the results of scan-dir and scan-store operations to create a list
	of blocks that are present in a store server but not referenced
	by the scanned directory servers.

delete-garbage
	Delete the blocks found by find-garbage from the store server.

To delete the garbage references in a given store server:
1. Run scan-store (as the store server user) to generate a list of references
   to blocks in the store server.
2. Run scan-dir for each Upspin tree that stores data in the store server (as
   the Upspin users that own those trees) to generate lists of block
   references mentioned by those trees.
3. Run find-garbage to compile a list of references that are in the scan-store
   output but not in the combined output of the scan-dir runs.
4. Run delete-garbage (as the store server user) to delete the blocks in the
   find-garbage output.

Usage of upspin audit:
	upspin [globalflags] audit <command> [flags] ...
Commands: scan-dir, scan-store, find-garbage, delete-garbage
    	user's configuration file (default "/home/user/upspin/config")



Sub-command config

Usage: upspin config [-out=outputfile]

Config prints to standard output the contents of the current config file.

It works by saving the file at initialization time, so if the actual
file has changed since the command started, it will still show the
configuration being used.

Flags:
  -help
    	print more information about the command
  -out string
    	output file (default standard output)



Sub-command countersign

Usage: upspin countersign

Countersign updates the signatures and encrypted data for all items
owned by the user. It is intended to be run after a user has changed
keys.

See the description for rotate for information about updating keys.

Flags:
  -help
    	print more information about the command



Sub-command cp

Usage: upspin cp [opts] file... file or cp [opts] file... directory

Cp copies files into, out of, and within Upspin. If the final
argument is a directory, the files are placed inside it.  The other
arguments must not be directories unless the -R flag is set.

If the final argument is not a directory, cp requires exactly two
path names and copies the contents of the first to the second.
The -R flag requires that the final argument be a directory.

All file names given to cp must be fully qualified paths,
either locally or within Upspin. For local paths, this means
they must be absolute paths or start with '.', '..',  or '~'.

When copying from one Upspin path to another Upspin path, cp can be
very efficient, copying only the references to the data rather than
the data itself.

Flags:
  -R	recursively copy directories
  -help
    	print more information about the command
  -overwrite
    	overwrite existing files (default true)
  -v	log each file as it is copied



Sub-command createsuffixeduser

Usage: upspin createsuffixeduser <suffixed-user-name>

Createsuffixeduser creates a suffixed user of the current user, adding it
to the keyserver and creating a new config file and keys. It takes one
argument, the full name of the new user. The name of the new config file
will be the same as the current with .<suffix> appended. Default values
for servers and packing will be taken from the current config.

To create the user with suffix +snapshot, run
   upspin snapshot
rather than this command.

Flags:
  -curve name
    	cryptographic curve name: p256, p384, or p521 (default "p256")
  -dir address
    	Directory server address (default "dir.example.com:443")
  -force
    	if suffixed user already exists, overwrite its keys and config file
  -help
    	print more information about the command
  -rotate
    	back up the existing keys and replace them with new ones
  -secrets directory
    	directory to store key pair
  -secretseed string
    	the seed containing a 128 bit secret in proquint format or a file that contains it
  -server address
    	Store and Directory server address (if combined)
  -store address
    	Store server address (default "store.example.com:443")



Sub-command deletestorage

Usage: upspin deletestorage [-path path... | -ref reference...]

Deletestorage deletes blocks from the store. It is given
either a list of path names, in which case it deletes all blocks
referenced by those names, or a list of references, in which case
it deletes the blocks with those references.

WARNING! Deletestorage is dangerous and should not be used unless
the user can guarantee that the blocks that will be deleted are not
referenced by another path name in any other directory tree, including
snapshots.

Exactly one of the -path or -ref flags must be specified.

For -path, only regular items (not links or directories) can be
processed. Each block will be removed from the store on which it
resides, which in exceptional circumstances may be different from
the user's store.

For -ref, the reference must exactly match the reference's full
value, such as is presented by the info command. The reference is
assumed to refer to the store defined in the user's configuration.

Flags:
  -help
    	print more information about the command
  -path
    	delete all blocks referenced by the path names
  -ref
    	delete individual blocks with the specified references



Sub-command get

Usage: upspin get [-out=outputfile] path

Get writes to standard output the contents identified by the Upspin path.

The -glob flag can be set to false to have get skip Glob processing,
treating its argument as literal text even if it contains special
characters. (A leading @ sign is always expanded.)

Flags:
  -glob
    	apply glob processing to the arguments (default true)
  -help
    	print more information about the command
  -out string
    	output file (default standard output)



Sub-command getref

Usage: upspin getref [-store endpoint] [-out=outputfile] ref

Getref writes to standard output the contents identified by the reference from
the specified store endpoint, by default the user's default store server.
It does not resolve redirections.

Flags:
  -help
    	print more information about the command
  -out string
    	output file (default standard output)
  -store string
    	store endpoint (default the user's store)



Sub-command info

Usage: upspin info path...

Info prints to standard output a thorough description of all the
information about named paths, including information provided by
ls but also storage references, sizes, and other metadata.

If the path names an Access or Group file, it is also checked for
validity. If it is a link, the command attempts to access the target
of the link.

Flags:
  -R	recur into subdirectories
  -help
    	print more information about the command



Sub-command keygen

Usage: upspin keygen [-curve=256] [-secretseed=seed] <directory>

Keygen creates a new Upspin key pair and stores the pair in local files
secret.upspinkey and public.upspinkey in the specified directory.
Existing key pairs are appended to secret2.upspinkey.
Keygen does not update the information in the key server;
use the "user -put" command for that.

New users should instead use the "signup" command to create their first key.

See the description for rotate for information about updating keys.

Flags:
  -curve name
    	cryptographic curve name: p256, p384, or p521 (default "p256")
  -help
    	print more information about the command
  -rotate
    	back up the existing keys and replace them with new ones
  -secretseed string
    	the seed containing a 128-bit secret in proquint format or a file that contains it



Sub-command link

Usage: upspin link [-f] original_path link_path

Link creates an Upspin link. The link is created at the second path
argument and points to the first path argument.

Flags:
  -f	force creation of link when original path is inaccessible
  -help
    	print more information about the command



Sub-command ls

Usage: upspin ls [-l] [path...]

Ls lists the names and, if requested, other properties of Upspin
files and directories. If given no path arguments, it lists the
user's root. By default ls does not follow links; use the -L flag
to learn about the targets of links.

Flags:
  -L	follow links
  -R	recur into subdirectories
  -help
    	print more information about the command
  -l	long format



Sub-command mkdir

Usage: upspin mkdir [-p] directory...

Mkdir creates Upspin directories.

The -p flag can be set to have mkdir create any missing parent directories of
each argument.

The -glob flag can be set to false to have mkdir skip Glob processing,
treating its arguments as literal text even if they contain special
characters. (Leading @ signs are always expanded.)

Flags:
  -glob
    	apply glob processing to the arguments (default true)
  -help
    	print more information about the command
  -p	make all parent directories



Sub-command put

Usage: upspin put [-in=inputfile] path

Put writes its input to the store server and installs a directory
entry with the given path name to refer to the data.

The -glob flag can be set to false to have put skip Glob processing,
treating its arguments as literal text even if they contain special
characters. (Leading @ signs are always expanded.)

Flags:
  -glob
    	apply glob processing to the arguments (default true)
  -help
    	print more information about the command
  -in string
    	input file (default standard input)
  -packing string
    	packing to use (default from user's config)



Sub-command repack

Usage: upspin repack [-pack ee] [flags] path...

Repack rewrites the data referred to by each path, storing it again using the
packing specified by its -pack option, ee by default. If the data is already
packed with the specified packing, the data is untouched unless the -f (force)
flag is specified, which can be helpful if the data is to be repacked using a
fresh key.

Repack does not delete the old storage. See the deletestorage command
for more information.

Flags:
  -f	force repack even if the file is already packed as requested
  -help
    	print more information about the command
  -pack string
    	packing to use when rewriting (default "ee")
  -r	recur into subdirectories
  -v	verbose: log progress



Sub-command rm

Usage: upspin rm path...

Rm removes Upspin files and directories from the name space.

The -glob flag can be set to false to have rm skip Glob processing,
treating its arguments as literal text even if they contain special
characters. (Leading @ signs are always expanded.)

Rm does not delete the associated storage, which is rarely necessary
or wise: storage can be shared between items and unused storage is
better recovered by automatic means.

Rm does not delete the targets of links, only the links themselves.

See the deletestorage command for more information about deleting
storage.

Flags:
  -R	recur into subdirectories
  -f	continue if errors occur
  -glob
    	apply glob processing to the arguments (default true)
  -help
    	print more information about the command



Sub-command rotate

Usage: upspin rotate

Rotate pushes an updated key to the key server.

To update an Upspin key, the sequence is:

  upspin keygen -rotate <secrets-dir>   # Create new key.
  upspin countersign                    # Update file signatures to use new key.
  upspin rotate                         # Save new key to key server.
  upspin share -r -fix me@example.com/  # Update keys in file metadata.

Keygen creates a new key and saves the old one. Countersign walks
the file tree and adds signatures with the new key alongside those
for the old. Rotate pushes the new key to the KeyServer. Share walks
the file tree, re-wrapping the encryption keys that were encrypted
with the old key to use the new key.

Some of these steps could be folded together but the full sequence
makes it easier to recover if a step fails.

TODO: Rotate and countersign are terms of art, not clear to users.

Flags:
  -help
    	print more information about the command



Sub-command setupdomain

Usage: upspin setupdomain [-where=$HOME/upspin/deploy] [-cluster] -domain=<name>

Setupdomain is the first step in setting up an upspinserver or Upspin
Kubernetes cluster. If setting up an upspinserver, the next steps are
'setupstorage' (optionally) and 'setupserver'.

It generates keys and config files for Upspin server users, placing them in
$where/$domain (the values of the -where and -domain flags substitute for
$where and $domain respectively) and generates a signature that proves that the
calling Upspin user has control over domain.

If the -cluster flag is specified, keys for upspin-dir@domain and
upspin-store@domain are created instead. This flag should be used when setting
up a domain that will run its directory and store servers separately, requiring
separate users to adminster each one. When -cluster is not specified, keys for
a single user (upspin@domain) are generated.

If any state exists at the given location (-where) then the command aborts.

Flags:
  -cluster
    	generate keys for upspin-dir@domain and upspin-store@domain (default is upspin@domain only)
  -curve name
    	cryptographic curve name: p256, p384, or p521 (default "p256")
  -domain name
    	domain name for this Upspin installation
  -help
    	print more information about the command
  -project project
    	GCP project name
  -put-users
    	put server users to the key server
  -secretseed string
    	the seed containing a 128 bit secret in proquint format or a file that contains it
  -where directory
    	directory to store private configuration files (default "/home/user/upspin/deploy")



Sub-command setupserver

Usage: upspin setupserver -domain=<domain> -host=<host> [-where=$HOME/upspin/deploy] [-writers=user,...]

Setupserver is the final step of setting up an upspinserver.
It assumes that you have run 'setupdomain' and (optionally) 'setupstorage'.

It registers the user created by 'setupdomain' domain with the key server,
copies the configuration files from $where/$domain to the upspinserver and
restarts it, puts the Writers file, and makes the root for the calling user.

The calling user and the server user are included in the Writers file by
default (giving them write access to the store and directory). You may specify
additional writers with the -writers flag. For instance, if you want all users
@example.com to be able to access storage, specify "-writers=*@example.com".

The calling user must be the same one that ran 'upspin setupdomain'.

Flags:
  -domain name
    	domain name for this Upspin installation
  -help
    	print more information about the command
  -host name
    	host name of upspinserver (empty implies the cluster dir.domain and store.domain)
  -where directory
    	directory to store private configuration files (default "/home/user/upspin/deploy")
  -writers users
    	additional users to be given write access to this server



Sub-command setupstorage

Usage: upspin setupstorage -domain=<name> -path=<storage_dir>

Setupstorage is the second step in establishing an upspinserver,
It sets up storage for your Upspin installation.
The first step is 'setupdomain' and the final step is 'setupserver'.

This version of setupstorage configures local disk storage.
Read the documentation at
	https://upspin.io/doc/server_setup.md
for information on configuring upspinserver to use cloud storage services.

Flags:
  -config string
    	do not set; here only for consistency with other upspin commands
  -domain name
    	domain name for this Upspin installation
  -help
    	print more information about the command
  -path directory
    	directory on the server in which to keep Upspin storage (default is $HOME/upspin/server/storage)
  -where directory
    	directory to store private configuration files (default "/home/user/upspin/deploy")



Sub-command setupwriters

Usage: upspin setupwriters [-where=$HOME/upspin/deploy] -domain=<domain> <user names>

Setupwriters creates or updates the Writers file for the given domain.
The file lists the names of users granted access to write to the domain's
store server and to create their own root on the directory server.

A wildcard permits access to all users of a domain ("*@example.com").

The user name of the project's directory server is automatically included in
the list, so the directory server can use the store for its own data storage.

Flags:
  -domain name
    	domain name for this Upspin installation
  -help
    	print more information about the command
  -where directory
    	directory containing private configuration files (default "/home/user/upspin/deploy")



Sub-command share

Usage: upspin share path...

Share reports the user names that have access to each of the argument
paths, and what access rights each has. If the access rights do not
agree with the keys stored in the directory metadata for a path,
that is also reported. Given the -fix flag, share updates the keys
to resolve any such inconsistency. Given both -fix and -force, it
updates the keys regardless. The -d and -r flags apply to directories;
-r states whether the share command should descend into subdirectories.

For the rare case of a world-readable ("read:all") file that is encrypted,
the -unencryptforall flag in combination with -fix will rewrite the file
using the EEIntegrity packing, decrypting it and making its contents
visible to anyone.

The -glob flag can be set to false to have share skip Glob processing,
treating its arguments as literal text even if they contain special
characters. (Leading @ signs are always expanded.)

See the description for rotate for information about updating keys.

Flags:
  -d	do all files in directory; path must be a directory
  -fix
    	repair incorrect share settings
  -force
    	replace wrapped keys regardless of current state
  -glob
    	apply glob processing to the arguments (default true)
  -help
    	print more information about the command
  -q	suppress output. Default is to show state for every file
  -r	recur into subdirectories; path must be a directory. assumes -d
  -unencryptforall
    	for currently encrypted read:all files only, rewrite using EEIntegrity; requires -fix or -force



Sub-command shell

Usage: upspin shell [-v] [-prompt=<prompt_string>]

Shell runs an interactive session for Upspin subcommands.
When running the shell, the leading "upspin" is assumed on each command.

The shell has a simple interface, free of quoting or other features usually
associated with interactive shells. It is intended only for testing and is kept
simple for reasons of comprehensibility, portability, and maintainability.
Those who need quoting or line editing or other such features should use their
regular shell and run upspinfs or invoke the upspin command line-by-line.

The shell does have one convenience feature, though, in the handling of path
names. A path beginning with a plain @ refers to the current user's root
(ann@example.com), while one starting @+suffix is the same with the suffix
included (ann+suffix@example.com). This feature works in all upspin commands
but is particularly handy inside the shell.

Flags:
  -help
    	print more information about the command
  -prompt prompt
    	interactive prompt (default "<username>")
  -v	verbose; print to stderr each command before execution



Sub-command signup

Usage: upspin [-config=<file>] signup -dir=<addr> -store=<addr> [flags] <username>
       upspin [-config=<file>] signup -server=<addr> [flags] <username>

Signup generates an Upspin configuration file and private/public key pair,
stores them locally, and sends a signup request to the public Upspin key server
at key.upspin.io. The server will respond by sending a confirmation email to
the given email address (or "username").

The email address becomes a username after successful signup but is never
again used by Upspin to send or receive email. Therefore the email address
may be disabled once signup is complete if one wishes to have an Upspin
name distinct from one's regular email address. Either way, if the email
address is compromised after Upspin signup, the security of the user's
Upspin data is unaffected.

Signup writes a configuration file to $HOME/upspin/config, holding the
username and the location of the directory and store servers. It writes the
public and private keys to $HOME/.ssh. These locations may be set using the
global -config and signup-specific -where flags.

The -dir and -store flags specify the network addresses of the Store and
Directory servers that the Upspin user will use. The -server flag may be used
to specify a single server that acts as both Store and Directory, in which case
the -dir and -store flags must not be set.

By default, signup creates new keys with the p256 cryptographic curve set.
The -curve and -secretseed flags allow the user to control the curve or to
recreate or reuse prior keys.

The -signuponly flag tells signup to skip the generation of the configuration
file and keys and only send the signup request to the key server.

Flags:
  -curve name
    	cryptographic curve name: p256, p384, or p521 (default "p256")
  -dir address
    	Directory server address
  -force
    	create a new user even if keys and config file exist
  -help
    	print more information about the command
  -key address
    	Key server address (default "key.upspin.io:443")
  -secrets directory
    	directory to store key pair
  -secretseed string
    	the seed containing a 128 bit secret in proquint format or a file that contains it
  -server address
    	Store and Directory server address (if combined)
  -signuponly
    	only send signup request to key server; do not generate config or keys
  -store address
    	Store server address



Sub-command snapshot

Usage: upspin snapshot

Snapshot requests the system to take a snapshot of the user's
directory tree as soon as possible. Snapshots are created only if
the directory server for the user's root supports them.

Flags:
  -help
    	print more information about the command



Sub-command tar

Usage: upspin tar [-extract [-match prefix -replace substitution] ] upspin_directory local_file

Tar archives an Upspin tree into a local tar file, or with the
-extract flag, unpacks a local tar file into an Upspin tree.

When extracting, the -match and -replace flags cause the extracted
file to have any prefix that matches be replaced by substitute text.
Whether or not these flags are used, the destination path must
always be in Upspin.

Flags:
  -extract
    	extract from archive
  -help
    	print more information about the command
  -match prefix
    	extract from the archive only those pathnames that match the prefix
  -replace text
    	replace -match prefix with the replacement text
  -v	verbose output



Sub-command user

Usage: upspin user [username...]
              user -put [-in=inputfile] [-force] [username]

User prints in YAML format the user record stored in the key server
for the specified user, by default the current user.

With the -put flag, user writes or replaces the information stored
for the current user, such as to update keys or server information.
The information is read from standard input or from the file provided
with the -in flag. The input must provide the complete record for
the user, and must be in the same YAML format printed by the command
without the -put flag.

When using -put, the command takes no arguments. The name of the
user whose record is to be updated must be provided in the input
record and must either be the current user or the name of another
user whose domain is administered by the current user.

A handy way to use the command is to edit the config file and run
	upspin user | upspin user -put

To install new users see the signup command.

Flags:
  -force
    	force writing user record even if key is empty
  -help
    	print more information about the command
  -in string
    	input file (default standard input)
  -put
    	write new user record



Sub-command watch

Usage: upspin watch [-sequence=n] path

Watch watches the given Upspin path beginning with the specified
sequence number and prints the events to standard output. A sequence
number of -1, the default, will send the current state of the tree
rooted at the given path.

The -glob flag can be set to false to have watch skip Glob processing,
treating its arguments as literal text even if they contain special
characters. (Leading @ signs are always expanded.)

Flags:
  -glob
    	apply glob processing to the arguments (default true)
  -help
    	print more information about the command
  -sequence sequence
    	sequence number (default -1)



Sub-command whichaccess

Usage: upspin whichaccess path...

Whichaccess reports the Upspin path of the Access file
that controls permissions for each of the argument paths.

The -glob flag can be set to false to have watchaccess skip Glob
processing, treating its arguments as literal text even if they
contain special characters. (Leading @ signs are always expanded.)

Flags:
  -glob
    	apply glob processing to the arguments (default true)
  -help
    	print more information about the command

*/
package main
