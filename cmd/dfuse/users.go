// The upspin fuse interface.
package main

import (
	"errors"
	"sync"
	"time"

	"upspin.googlesource.com/upspin.git/bind"
	"upspin.googlesource.com/upspin.git/upspin"
)

// userEntry represents what is known about a user.
type userEntry struct {
	expires time.Time // when the information expires.
	name    upspin.UserName
	dir     upspin.Directory
}

type userCache struct {
	sync.Mutex
	context *upspin.Context
	entries map[upspin.UserName]*userEntry
}

func newUserCache(context *upspin.Context) *userCache {
	c := &userCache{context: context}
	// Don't perform lookups since you are the authoritative source.
	c.add(context.UserName, context.Directory, time.Now().Add(time.Duration(100000)*time.Hour))
	return c
}

// add a user to the  cache.
func (u *userCache) add(name upspin.UserName, dir upspin.Directory, expires time.Time) *userEntry {
	ue := &userEntry{expires: expires, name: name, dir: dir}
	u.Lock()
	u.entries[name] = ue
	u.Unlock()
	return ue
}

// remove a user from the cache.
func (u *userCache) remove(name upspin.UserName) {
	u.Lock()
	delete(u.entries, name)
	u.Unlock()
}

// lookup a user.  Return the directory to use.
func (u *userCache) lookup(name upspin.UserName) (*userEntry, error) {
	u.Lock()
	ue, ok := u.entries[name]
	u.Unlock()

	// If we have an unexpired binding, use it.
	if ok && !time.Now().After(ue.expires) {
		return ue, nil
	}
	eps, _, err := u.context.User.Lookup(name)
	if err != nil {
		return nil, err
	}

	// Try for a new binding.
	lastErr := errors.New("could not connect to user directory")
	for _, ep := range eps {
		if dir, err := bind.Directory(u.context, ep); err == nil {
			return u.add(name, dir, time.Now().Add(time.Hour)), nil
		} else {
			lastErr = err
		}
	}

	// At this point we failed binding to a directory.  However, if we
	// still have an expired entry, use that instead.
	u.Lock()
	ue, ok = u.entries[name]
	u.Unlock()
	if !ok {
		return nil, lastErr
	}
	return ue, nil
}

func (u *userCache) knownUserDirectories() []*upspin.DirEntry {
	u.Lock()
	defer u.Unlock()
	var all []*upspin.DirEntry
	for user, _ := range u.entries {
		de := &upspin.DirEntry{Name: upspin.PathName(user), Metadata: upspin.Metadata{IsDir: true}}
		all = append(all, de)
	}
	return all
}
