/*
Package upspin contains global interface and other definitions for the components of the system.

Upspin provides a global hierarchical name space for accessing
arbitrary user-defined data.  The interfaces for accessing and
protecting data are general: Any service that implements the
interfaces defined here, whether provided by the Upspin project
itself or by an external agent that implements these interfaces as
an alternate access method, contributes to the global name space.

The name space itself is a single rooted tree. The root of the tree
contains a subdirectory for each user, called the user root, that
contains all directories and storage for that user. The user root
is identified by the user's name, which has an email-like format.
Thus the fully qualified name (called a path name) of an item looks
analogous to
	user@name.com/file/name
There is no leading slash in a path name.

There are three fundamental services defined in Upspin.

* The Store is the ultimate location for user-provided data.  Every
piece of user data stored in the system is identified by a Location,
which identifies where it is stored and how to address it, and a
Packing, which identifies how the data is represented, including
details such as encryption and compression.

* The Directory service implements the name space, binding hierarchical,
Unix-like names to Locations of data stored in Store servers.
Directories also attach metadata to file names, which may include
encryption/decryption keys or other access-control information.

* The User service stores, for each registered user, the Location
of the user root and any public encryption keys exported by that
user.

Each of these three services is itself distributed, so for instance
a user root may be hosted on or split across multiple Directory
servers, which coordinate to provide a consistent view of the user's
data.

Finally, the Client interface provides a coherent high-level file-like API that,
internally, calls upon the other services to access and manage the data.
Most applications using Upspin will talk to the Client interface alone.
*/
package upspin
