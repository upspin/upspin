# Upspin architecture

Each user of the system is represented by an Upspin user name, which looks like
an email address; a public/private key pair; and the network address of a
directory server:

<img src="/images/arch/user.png" width="157" alt="User diagram"/>

That directory server holds a hierarchical tree of names pointing to the user's
data, which is held in a store server, possibly encrypted.
Each item in the tree is represented by a directory entry containing a list of
references that point to data in the store server:

<img src="/images/arch/dirstore.png" width="674" alt="Directory and Store server diagram"/>

All the users are connected through a central key server at `key.upspin.io`,
which holds the public key and directory server address for each user.

<img src="/images/arch/key.png" width="527" alt="Key server diagram"/>

This is how the pieces fit together:

<img src="/images/arch/overall.png" width="503" alt="Overall system diagram"/>

From top to bottom, these represent:

- Shared directory and store servers used by multiple users.
- A single-user system with a combined directory and store server.
- A camera served by a special-purpose combined directory and store server.

To illustrate the relationship between these components, here is the sequence
of requests a client exchanges with the servers to read the file
`augie@upspin.io/Images/Augie/large.jpg`:

<img src="/images/arch/readfile.png" width="629" alt="Reading a file diagram"/>

1. The client asks the key server for the record describing the owner of the
   file, which is the user name at the beginning of the file name (`augie@upspin.io`).
   The key server's response contains the name of the directory server holding
   that user's tree (`dir.upspin.io`) and Augie's public key.
2. The client asks the directory server for the directory entry describing the
   file. The response contains a list of block references, which include the
   name of the store server (`store.upspin.io`).
3. The client can then ask the store server for each of the blocks, pipelining
   the requests for efficiency.
4. The client decrypts the blocks (using Augie's public key) and concatenates
   them to assemble the file.
