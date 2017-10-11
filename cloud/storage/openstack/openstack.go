package openstack

import (
	"bytes"
	"net/http"

	"github.com/rackspace/gophercloud"
	"github.com/rackspace/gophercloud/openstack"
	"github.com/rackspace/gophercloud/openstack/objectstorage/v1/containers"
	"github.com/rackspace/gophercloud/openstack/objectstorage/v1/objects"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/upspin"
)

const storageName = "Openstack"

// Openstack specific option names. Credentials are expected to be stored in
// environment variables. See
// https://github.com/rackspace/gophercloud#credentials
const (
	openstackRegion    = "openstackRegion"
	openstackContainer = "openstackContainer"
)

var (
	requiredOpts = []string{openstackRegion, openstackContainer}
)

const (
	// See https://docs.openstack.org/swift/latest/overview_acl.html
	containerReadHeader = "X-Container-Read"
	containerPublicACL  = ".r:*"
)

type openstackStorage struct {
	client    *gophercloud.ServiceClient
	container string
}

func New(opts *storage.Opts) (storage.Storage, error) {
	const op = "cloud/storage/openstack.New"

	for _, opt := range requiredOpts {
		if _, ok := opts.Opts[opt]; !ok {
			return nil, errors.E(op, errors.Invalid, errors.Errorf(
				"%q option is required", opt))
		}
	}

	authOpts, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		return nil, errors.E(op, errors.Invalid, errors.Errorf(
			"Auth options not found in env: %s", err))
	}

	provider, err := openstack.AuthenticatedClient(authOpts)
	if err != nil {
		return nil, errors.E(op, errors.Permission, errors.Errorf(
			"Could not authenticate: %s", err))
	}

	client, err := openstack.NewObjectStorageV1(provider, gophercloud.EndpointOpts{
		Region: opts.Opts[openstackRegion],
	})
	if err != nil {
		// The error kind is "Invalid" because AFAICS this can only
		// happen for unknown region
		return nil, errors.E(op, errors.Invalid, errors.Errorf(
			"Could not create object storage client: %s", err))
	}

	return &openstackStorage{
		client:    client,
		container: opts.Opts[openstackContainer],
	}, nil
}

func init() {
	err := storage.Register(storageName, New)
	if err != nil {
		// If more modules are registering under the same storage name,
		// an application should not start.
		panic(err)
	}
}

var _ storage.Storage = (*openstackStorage)(nil)

// LinkBase will return the URL if the container has read access for everybody
// and an unsupported error in case it does not. Still, it might return an
// error because it can't get the necessary metadata.
func (s *openstackStorage) LinkBase() (string, error) {
	const op = "cloud/storage/openstack.LinkBase"

	r := containers.Get(s.client, s.container)
	if r.Err != nil {
		return "", errors.E(op, errors.IO, errors.Errorf(
			"Unable to get container %q: %s", s.container, r.Err))
	}
	h, err := r.ExtractHeader()
	if err != nil {
		return "", errors.E(op, errors.IO, errors.Errorf(
			"Unable to extract header: %s", err))
	}
	if acl := h.Get(containerReadHeader); acl == containerPublicACL {
		return s.client.ServiceURL(s.container) + "/", nil
	}
	return "", upspin.ErrNotSupported
}

func (s *openstackStorage) Download(ref string) ([]byte, error) {
	const op = "cloud/storage/openstack.Download"

	contents, err := objects.Download(s.client, s.container, ref, nil).ExtractContent()
	if err != nil {
		if unexpected, ok := err.(*gophercloud.UnexpectedResponseCodeError); ok {
			if unexpected.Actual == http.StatusNotFound {
				return nil, errors.E(op, errors.NotExist, err)
			}
		}
		return nil, errors.E(op, errors.IO, errors.Errorf(
			"Unable to download ref %q from container %q: %s", ref, s.container, err))
	}
	return contents, nil
}

func (s *openstackStorage) Put(ref string, contents []byte) error {
	const op = "cloud/storage/openstack.Put"

	err := objects.Create(s.client, s.container, ref, bytes.NewReader(contents), nil).Err
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf(
			"Unable to upload ref %q to container %q: %s", ref, s.container, err))
	}
	return nil
}

func (s *openstackStorage) Delete(ref string) error {
	const op = "cloud/storage/openstack.Delete"

	err := objects.Delete(s.client, s.container, ref, nil).Err
	if err != nil {
		return errors.E(op, errors.IO, errors.Errorf(
			"Unable to delete ref %q from container %q: %s", ref, s.container, err))
	}
	return nil
}
