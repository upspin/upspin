// Package usercache pushes a user cache in front of context.User.
package usercache

import (
	"time"

	"upspin.googlesource.com/upspin.git/cache"
	"upspin.googlesource.com/upspin.git/upspin"
)

type entry struct {
	expires time.Time // when the information expires.
	eps     []upspin.Endpoint
	pub     []upspin.PublicKey
}

type userCache struct {
	uncached upspin.User
	entries  *cache.LRU
	duration time.Duration
}

// Push a cache onto the User service.  After this all User service requests will
// be filtered through the cache.
//
// TODO(p): Push is not concurrency safe since context is assumed to be immutable
// everywhere else.  Not sure this needs to be fixed but should at least be noted.
func Push(context *upspin.Context) {
	c := &userCache{
		uncached: context.User,
		entries:  cache.NewLRU(256),
		duration: time.Minute * 15,
	}
	context.User = c
}

// Lookup implements upspin.User.Lookup.
func (c *userCache) Lookup(name upspin.UserName) ([]upspin.Endpoint, []upspin.PublicKey, error) {
	v, ok := c.entries.Get(name)

	// If we have an unexpired binding, use it.
	if ok && !time.Now().After(v.(*entry).expires) {
		e := v.(*entry)
		return e.eps, e.pub, nil
	}

	// Not found, look it up.
	eps, pub, err := c.uncached.Lookup(name)
	if err != nil {
		return nil, nil, err
	}
	e := &entry{
		expires: time.Now().Add(c.duration),
		eps:     eps,
		pub:     pub,
	}
	c.entries.Add(name, e)
	return eps, pub, nil
}

// Dial implements upspin.User.Dial.
func (c *userCache) Dial(context *upspin.Context, e upspin.Endpoint) (interface{}, error) {
	return c, nil
}

// ServerUserName implements upspin.User.ServerUserName.
func (c *userCache) ServerUserName() string {
	return c.uncached.ServerUserName()
}

// SetDuration sets the duration until entries expire.  Primarily
// intended for testing.
func (c *userCache) SetDuration(d time.Duration) {
	c.duration = d
}
