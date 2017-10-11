// Command upspinserver-openstack is a combined DirServer and
// StoreServer for use on stand-alone machines. It provides the
// production implementations of the dir and store servers
// (dir/server and store/server) with support for storage in
// Openstack e.g. OVH Object Storage or Rackspace Cloud Files.
package main // import "upspin.io/cmd/upspinserver-openstack"

import (
	"upspin.io/cloud/https"
	"upspin.io/serverutil/upspinserver"

	_ "upspin.io/cloud/storage/openstack"
)

func main() {
	ready := upspinserver.Main()
	https.ListenAndServe(ready, https.OptionsFromFlags())
}
