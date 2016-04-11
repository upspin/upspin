package main

import (
	"sync"

	"upspin.googlesource.com/upspin.git/client"
	"upspin.googlesource.com/upspin.git/upspin"
)

type directoryCache struct {
	sync.Mutex
	client  upspin.Client
	entries map[upspin.UserName]upspin.Directory
}

func newDirectoryCache(context *upspin.Context) *directoryCache {
	c := &directoryCache{client: client.New(context), entries: make(map[upspin.UserName]upspin.Directory)}
	return c
}

// remove removes a user from the cache.
func (c *directoryCache) remove(name upspin.UserName) {
	c.Lock()
	delete(c.entries, name)
	c.Unlock()
}

// lookup looks up a user.  Return the directory to use.
func (c *directoryCache) lookup(name upspin.UserName) (upspin.Directory, error) {
	c.Lock()
	dir, ok := c.entries[name]
	c.Unlock()

	if ok {
		return dir, nil
	}

	dir, err := c.client.Directory(upspin.PathName(name))
	if err != nil {
		return nil, err
	}

	c.Lock()
	c.entries[name] = dir
	c.Unlock()
	return dir, nil
}
