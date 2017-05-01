# Upspin server implementers guide

This document describes how to implement an Upspin Store and Directory server
in Go. It explains the core concepts and interfaces, the available support
packages, and deployment concerns.

Throughout the document we use the demoserver as an example.
You can install it with `go get`:

```
$ go get upspin.io/exp/cmd/demoserver
```

Most of the types discussed in this document are declared in package `upspin`.
You should consult
[the thorough documentation](https://godoc.org/upspin.io/upspin/)
when implementing Upspin servers.

## Config

## Endpoints

An [Endpoint](https://godoc.org/upspin.io/upspin/#Endpoint)
identifies a means for connecting to a Service.

```
type Endpoint struct {
	Transport Transport
	NetAddr   NetAddr
}
```

The Transport field specifies how to connect to the service,
which in almost all cases is over the network (upspin.Remote).
The NetAddr field sepecifies the network address (such as "localhost:9999").

Each Service must have an Endpoint.
Typically the NetAddr component of a Service's Endpoint is provided as the
command-line argument -addr, available to your program as flags.NetAddr.

```
addr := upspin.NetAddr(flags.NetAddr)
ep := upspin.Endpoint{
	Transport: upspin.Remote,
	NetAddr:   addr,
}
```

## Services

The three core Upspin services (KeyServer, StoreServer, and DirServer) each
implement the [Service](https://godoc.org/upspin.io/upspin/#Service) interface.

```
type Service interface {
	Endpoint() Endpoint
	Ping() bool
	Close()
}
```

The implementation of these methods is trivial.

## The Dialer interface

The core Upspin services also implement the
[Dialer](https://godoc.org/upspin.io/upspin/#Dialer) interface.

```
type Dialer interface {
	Dial(Config, Endpoint) (Service, error)
}
```

The Dial method returns an instance of the Service for the given Config's User.

If your service doesn't perform any kind of User-based access controls
then its implementation could be as simple as this:

```
func (s *service) Dial(upspin.Config, upspin.Endpoint) (upspin.Service, error) {
	return s, nil
}
```

More often, a Dial method will return a copy of the Service that serves
the given user:

```
func (s *service) Dial(cfg upspin.Config, _ upspin.Endpoint) (upspin.Service, error) {
	ss := *s
	s.user = cfg.UserName()
	return &ss, nil
}
```

Other methods of the Service may then use its `user` field for access control
(authenticating and/or validating reads, writes, etc).

## The StoreServer interface

An Upspin [StoreServer](https://godoc.org/upspin.io/upspin/#StoreServer)
stores data keyed by [References](https://godoc.org/upspin.io/upspin/#Reference).

```
type StoreServer interface {
	Dialer
	Service
	Get(ref Reference) ([]byte, *Refdata, []Location, error)
	Put(data []byte) (*Refdata, error)
	Delete(ref Reference) error
}
```

References are server-defined: when a user Puts an object, the server computes
a Reference for it and returns it in its reply.
Typically References are a hash of the data,
but they may be any valid Unicode string.

### The StoreServer.Put method

The Put method stores the given data and returns a Refdata containing a
Reference with which the data can be retrieved.
(TODO: figure out why we return Refdata here, issue #376.)

```
type Refdata struct {
	Reference Reference
	Volatile  bool
	Duration  time.Duration
}
```

The Reference is typically a SHA256 hash of the data, but it may be any valid
UTF-8 string.

### The StoreServer.Get method

```
	Get(ref Reference) ([]byte, *Refdata, []Location, error)
```

The Get method returns the data for a given Reference.
If the server does not have any data for that Reference, it should
return an error with kind errors.NotExist.
It also returns a Refdata:

```
type Refdata struct {
	Reference Reference
	Volatile  bool
	Duration  time.Duration
}
```

Alternatively, the store may redirect the caller to another store
by returning nil []byte, nil Refdata, and non-nil []Location.
However, this feature is seldom used.

### The StoreServer.Delete method


## The DirServer interface

An Upspin [DirServer](https://godoc.org/upspin.io/upspin/#StoreServer)
stores structured data about user trees, including file names,
References, 

```
type DirServer interface {
	Dialer
	Service
	Lookup(name PathName) (*DirEntry, error)
	Put(entry *DirEntry) (*DirEntry, error)
	Glob(pattern string) ([]*DirEntry, error)
	Delete(name PathName) (*DirEntry, error)
	WhichAccess(name PathName) (*DirEntry, error)
	Watch(name PathName, order int64, done <-chan struct{}) (<-chan Event, error)
}
```


## Serving RPC methods

Upspin uses a simple RPC implementation that sends [Protocol Buffers](TODO) over HTTP.
The `upspin.io/rpc` package provides both the server and client implementations.

Once you have implemented an StoreServer or DirServer, you may use the wrappers
The `upspin.io/rpc/storeserver` and `upspin.io/rpc/dirserver` provide functions
that take a `StoreServer` or `DirServer` (repsectively) and return it as an
`http.Handler`, which may then be served by the `net/http` package's HTTP
server.

```
var (
	myStore upspin.StoreServer
	myDir   upspin.DirServer
	cfg     upspin.Config
)
http.Handle("/api/Store/", storeserver.New(cfg, myStore, addr))
http.Handle("/api/Dir/", dirserver.New(cfg, myDir, addr))
```

## Testing with `upbox`

