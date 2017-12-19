# Documentation

<!--- These tags hold related issue numbers. This page's development
is part of #336. --->

## Introduction

- The [Upspin Overview](/doc/overview.md) document provides a high-level
  introduction to Upspin.
  It is a good place to start to learn about the motivation for the project
  and overall design.
  It also has introductions to many of the other topics explored in more
  detail in the other documents.

- The [FAQ](/doc/faq.md) answers common questions about Upspin.

## User guide

- The [Signing up a new user](/doc/signup.md) document describes the process for
  generating keys and registering a user with the Upspin key server.<!--- #326 #210 --->

- The [Upspin Access Control](/doc/access_control.md) document describes
  Upspin's access control mechanisms. TODO: Break into user-level pieces
  and implementation details; also linked in Architecture below.

- The [Upspin Configuration](/doc/config.md) document describes Upspin's
  configuration file format and settings.

## Tools

- The [`upspin`](https://godoc.org/upspin.io/cmd/upspin) command is a
  command-line tool for creating and administering Upspin files, users,
  and servers.

- The [`upspin-ui`](https://godoc.org/augie.upspin.io/cmd/upspin-ui) tool
  presents a web interface to the Upspin name space, and also provides a
  facility to sign up an Upspin user and deploy an upspinserver to Google Cloud
  Platform.

- The [`cacheserver`](https://godoc.org/upspin.io/cmd/cacheserver)
  is a client-side directory and storage cache for Upspin.

- The [`upspinfs`](https://godoc.org/upspin.io/cmd/upspinfs) command
  is a [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace)
  interface for Upspin.

- The [`upspin-audit`](https://godoc.org/upspin.io/cmd/upspin#hdr-Sub_command_audit) command
  provides subcommands for auditing storage consumption.

## Architecture

- The [Upspin architecture](/doc/arch.md) page has a number of diagrams
  showing, bottom-up, how the pieces all fit together. TODO: add things like keys,
  sharing etc. as diagrams there.<!---  #217 #209 --->

- The [Upspin Access Control](/doc/access_control.md) document describes
  Upspin's access control mechanisms. TODO: Break into user-level pieces
  and implementation details. TODO: Server-level access control: Writers file etc.

- The [Upspin Security](/doc/security.md) document describes Upspin's security
  model.

## System setup and administration

- The [Setting up `upspinserver`](/doc/server_setup.md) document explains how
  to set up your own Upspin installation on a Linux server.<!--- #406 #326 --->

- TODO: Show how to set up with a reverse proxy. <!--- #233 --->

## Programming

- The [`upspin` package](https://godoc.org/upspin.io/upspin) specifies the core
  interfaces that define the Upspin protocol.

- The [`rpc` package](https://godoc.org/upspin.io/rpc) includes a semiformal
  description of the wire protocol used to communicate between clients and
  servers.

- The [`client` package](https://godoc.org/upspin.io/client) provides a
  simple client interface for communicating with Upspin servers.

- TODO: A worked example (implementer's guide).
