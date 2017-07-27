# Frequently Asked Questions

### Introduction

In the examples that follow, commands to be typed to the shell begin with a dollar sign "`$`".
Lines that do not begin with "`$`" show command output.

The text uses `you@example.com` as a stand-in for your user name.
If you run one of these commands, substitute your own name for that one.

The examples are Unix-oriented but it should be easy to adapt them to a Windows environment.

## The project

### What is Upspin?

Upspin attempts to provide a secure platform for naming, storing, and sharing
information.

It is not a storage system first, it is a naming system.
It provides protocols for secure storage and sharing that can be
used by people, programs, providers, and products to build a single coherent
way to talk about and manage data.

### What are the goals of the project?

The cloud-oriented information world brought much to celebrate,
but it is not always a seamless environment.
It's too often necessary to download a file only to upload it again to
another service, or to make an item publicly accessible when the
goal is just to make it accessible to one person.

Upspin addresses some of this clumsiness by providing a single,
consistent experience that, if the project succeeds, will allow
people to share data just by naming it, not by copying it around;
to share it securely;
to know who has access;
and to unify the way information is accessed.

### Why create something new rather than work on an existing system?

There are many systems out there that can provide some of what
Upspin offers, but there is none that addresses its goals directly.

The combination of:

- a single global name space;
- a single global user space;
- a focus on naming rather than storage;
- federated protocols;
- secure end-to-end encryption;
- secure, easily audited sharing;
- open source definition and implementations;

and other properties make Upspin unique.

Because of the dependence on simple federated protocols rather than a
central service provider, it should be possible to unify other
information service providers by connecting them through
Upspin.

We prefer to think of Upspin as a complement to existing services
rather than as a competitor to them.

### Why use a central key server?

The global name space requires that there be only one meaning
for any user name or path name.
This approach, at least conceptually, requires a central key server.
With multiple key servers, names could be ambiguous.

We are aware of the problems of a single point of failure that a
central key server represents, but feel that with suitable
engineering the risk can be mitigated.

The key server is a very simple service, and it could
easily be replaced with another implementation.
We hope one day to replace the existing server with
one built on a public service such as Google's
[Key Transparency](https://github.com/google/keytransparency)
project.

## Users and keys

### Why use email addresses to identify users?

Email addresses are easily verified, externally managed names for people.
They therefore provide a simple foundation upon which Upspin can build.

It's important to realize that the name is only used as an actual email
address during account creation.
Once the user is registered, the user name looks like an email address,
of course, but internally it is just a string.
After signup it is never used by Upspin to send or receive mail.

Those worried about exposing a private email address
can create a temporary one, register with it, and delete the email
account afterwards.
It would still serve as an Upspin user name.
As a corollary, if the email account is hacked, lost, or transferred,
that would have no effect on the security of the user's data in Upspin.

The decision to use email addresses as user names would in principle be
easy to change.
It is just a name, after all.
Upspin could replace the naming mechanism and one day may do so,
although in practice we find email addresses work well.

### How do keys work?

Every user has two keys.
A public one is stored in the key server at `key.upspin.io` and is visible to anyone.
A secret key is coupled to the public key but is stored only on the local machine.
It should never be shared with anyone or published.
It should also be kept safely; if you lose it, you lose all access to Upspin, including
the right to use your register user name with the system.

Locally, these keys are stored, by default, in the directory

```
$HOME/.ssh/you@example.com
```

in files named `public.upspinkey` and `secret.upspinkey`.

See the document "not yet written" for a discussion of how keys provide
security and safe sharing.

### I lost my private key. How do I recover it?

When you signed up, you were encouraged to write down a "secret seed",
a string of letters that can be used to recreate your keys.
It looks something like this:

```
zapal-zuhiv-visop-gagil.dadij-lnjul-takiv-fomin
```

Use that secret to recreate your public and private keys:

```
$ upspin keygen -secretseed zapal-zuhiv-visop-gagil.dadij-lnjul-takiv-fomin $HOME/.ssh/you@example.com
```

This will write the private and public keys to the named directory.
If you have a `secrets` field in your `config` that names a different
directory, use that directory instead as the argument.

You can test that this has succeeded by running,

```
$ upspin user
```

This command will show you your configuration and also compare it with
the record stored in the public key server at `key.upspin.io`.
If there is any discrepancy, it will let you know.

### How do I recreate my keys on another machine?

Use the `upspin` `keygen` command, as described in the previous answer.

### I lost my secret seed. How do I recover it?

It is stored in your private key file, so if you still have that you can just
look inside (the format is just one long line):

```
$ cat $HOME/.ssh/you@example.com/secret.upspinkey
25500039906333220349315666410231979076544554594099570935052437564736835632126 # javam-lumat-ganiv-jizan.vodos-huhuz-jogud-darin
$
```

If you have neither your secret seed nor your private key saved, you will be unable to access Upspin.
There is no account recovery mechanism.

### I lost my keys. How do I recover my account?

If you have your secret seed, see the previous sections.

Otherwise, you cannot recover your keys or your Upspin account.

Without your private key or secret seed to regenerate it, there is no mechanism to restore a lost account.
Upspin attempts to be very secure, and it is our experience that account recovery mechanisms,
no matter how carefully designed, are easily subverted.
We believe the system is more secure by not providing one.

**Do not lose your secret seed!**


## Programming

### What does '"remote" not registered mean'?

If you write your own Upspin program and on startup it fails with an error like,

```
bind.KeyServer: service with transport "remote" not registered
```

it means your program is missing the code that connects it to the
`KeyServer` implementation specified in the Upspin `config` file.
The layer that makes this connection is called a _transport_, and
`remote` is the transport that connects to a network service.

The simplest fix is to add the import declaration,

```
import _ "upspin.io/key/transports"
```

to the Go source file that defines the `main` function.
This import collects all the transport implementations in the core Upspin
libraries and adds them to your program.

The underscore in the import declaration is necessary.
It tells the Go compiler that the import statement is being done for
side effects only, in this case the linking into the binary of the
full suite of transports.

