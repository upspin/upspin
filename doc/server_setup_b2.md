# Backblaze B2-specific server setup instructions

These instructions are part of the instructions for
[Setting up `upspinserver`](/doc/server_setup.md).
Please make sure you have read that document first.

## Build `upspinserver-b2cs` and `upspin-setupstorage-b2cs`

To use the Backblaze B2 Storage, fetch the `b2.upspin.io` repository and use the
`upspinserver-b2cs` and `upspin-setupstorage-b2cs` variants.

Fetch and install the repository and its dependencies:

```
local$ go get b2.upspin.io/cmd/...
```

This will install both the `upspin-setupstorage-b2cs` and `upspinserver-b2cs`
commands.

## Get your B2 credentials

1. Visit your B2 [dashboard](https://secure.backblaze.com/b2_buckets.htm).
2. If necessary, log in with your B2 credentials.
3. Click on *Show Account ID and Application Key*.
4. Copy your user ID and the application key.

Now run the `upspin-setupstorage-b2cs` command and pass the previously copied account ID and the application key as
arguments:

```
local$ upspin setupstorage-b2cs -domain=example.com -account=<account_id> -appkey=<application_key>
```

It should produce an output like this:

```
You should now deploy the upspinserver binary and run 'upspin setupserver'.
```

## Continue

You can now continue with the rest of the setup instructions: [Set up a server and deploy the upspinserver binary](/doc/server_setup.md#deploy).
