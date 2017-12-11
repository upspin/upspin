# Google Drive-specific server setup instructions

These instructions are part of the instructions for
[Setting up `upspinserver`](/doc/server_setup.md).
Please make sure you have read that document first.

## Build `upspinserver-drive` and `upspin-setupstorage-drive`

To use the Google Drive Storage, fetch the `drive.upspin.io` repository and use the
`upspinserver-drive` and `upspin-setupstorage-drive` variants.

Fetch and install the repository and its dependencies:

```
local$ go get drive.upspin.io/cmd/...
```

This will install both the `upspin-setupstorage-drive` and `upspinserver-drive`
commands.

## Link your Google Drive account

To allow the Upspin server to store data in your Google Drive space, you need to authorize
it by providing it with an OAuth2 authorization code. To do so, run the following command,
replacing `example.com` with your domain name:

```
local$ upspin setupstorage-drive -domain=example.com
```

The command should output a URL for you to visit. Open this URL in the browser, authorize Upspin
and copy the displayed authorization code. Paste this code in the terminal window where you have
run the command and press Enter. You should see:

```
You should now deploy the upspinserver binary and run 'upspin setupserver'.
```

## Notes

All Upspin data is stored under the [Application Data](https://developers.google.com/drive/v3/web/appdata) folder,
separate from your regular storage. You can manage the Upspin Google Drive storage in your account by navigating
to the [Manage Apps](https://developers.google.com/drive/v3/web/appdata) page of Google Drive.

## Continue

You can now continue with the rest of the setup instructions: [Set up a server and deploy the upspinserver binary](/doc/server_setup.md#deploy).
