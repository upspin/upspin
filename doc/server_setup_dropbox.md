# Dropbox-specific server setup instructions

These instructions are part of the instructions for
[Setting up `upspinserver`](/doc/server_setup.md).
Please make sure you have read that document first.

## Build `upspinserver-dropbox` and `upspin-setupstorage-dropbox`

To use Dropbox Storage fetch the `dropbox.upspin.io` repository and use the
`upspinserver-dropbox` and `upspin-setupstorage-dropbox` variants.

Fetch the repository and its dependencies:

```
local$ go get -d dropbox.upspin.io/cmd/...
```

Install the `upspin-setupstorage-dropbox` command:

```
local$ go install dropbox.upspin.io/cmd/upspin-setupstorage-dropbox
```

Build the `upspinserver-dropbox` binary:

```
local$ GOOS=linux GOARCH=amd64 go build dropbox.upspin.io/cmd/upspinserver-dropbox
```

## Get an Dropbox authorization code

1. Visit the following [site](https://www.dropbox.com/oauth2/authorize?client_id=wt1281n3q768jj3&response_type=code).
2. If necessary, log in with your Dropbox credentials and click on "Allow".
   <img src="/images/dropbox/allow.png" alt="Allow Upspin storage server to access Dropbox"/>
3. Copy the displayed authorization code.
   <img src="/images/dropbox/code.png" alt="Dropbox API code"/>

Now run the `upspin-setupstorage-dropbox` command and pass the previously copied code as
argument:

```
local$ upspin setupstorage-dropbox -domain=example.com <code>
```

It should produce an output like this:

```
You should now deploy the upspinserver binary and run 'upspin setupserver'.
```


## Notes

All Upspin data will be stored under the app folder `upspin` in the user Dropbox (`/App/upspin`).
Users should [disable syncing](https://www.dropbox.com/lp/pro/pro_onboarding_selective_sync) for
this folder on their Dropbox clients.

## Continue

You can now continue following the instructions in
[Setting up `upspinserver`](/doc/server_setup.md).
