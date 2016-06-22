// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"upspin.io/errors"
	"upspin.io/upspin"
)

// TODOS:
// - Ensure we're connected before doing anything.
// - Ping from time-to-time and redial if connection went to hell.
// - Add a test for Mongo, either running against a local DB or by mocking it.
// - (Maybe) Make doc be a more indexable unit so we can search for things other than a pathname.
// - (Maybe) Keep relationship between refs so that a ListDir is faster (likely requires a new interface).

// mongoImpl is the implementation of Storage using a MongoDB backend.
type mongoImpl struct {
	serverAddrs []upspin.NetAddr
	c           *mgo.Collection
	sess        *mgo.Session
	db          *mgo.Database
}

var _ Storage = (*mongoImpl)(nil)

const (
	databaseName   = "upspin"
	collectionName = "Dirs"
)

// NewMongoDB returns a new instance of Storage for talking to a MongoDB backend.
func NewMongoDB(serverAddrs []upspin.NetAddr) Storage {
	return &mongoImpl{
		serverAddrs: serverAddrs,
	}
}

// doc is the internal representation of a MongoDB object.
type doc struct {
	// ID is the MongoDB _id key. It's opaque to us.
	ID bson.ObjectId `bson:"_id,omitempty"`

	// Ref is the ref (or pathname) used by the Storage operations. It is our index key.
	Ref string

	// Contents is the raw contents we're storing. It is opaque to MongoDB and thus not indexable.
	Contents []byte
}

// PutLocalFile implements storage.Storage.
func (m *mongoImpl) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	// TODO: implement it, possibly using GridFS if we want to store large blobs (say, to replace
	// GCS in the Store service too)
	return "", errors.E("MongoDB.PutLocalFile", errors.Str("MongoDB.PutLocalFile not implemented"))
}

// Get implements storage.Storage.
func (m *mongoImpl) Get(ref string) (link string, error error) {
	// TODO: save as above. Not needed for Directory operations.
	return "", errors.E("MongoDB.Get", errors.Str("MongoDB.Get not implemented"))
}

// Download implements storage.Storage.
func (m *mongoImpl) Download(ref string) ([]byte, error) {
	const Download = "MongoDB.Download"
	var queryResults []doc
	err := m.c.Find(bson.M{"ref": ref}).Limit(2).All(&queryResults)
	if err != nil {
		// This error probably doesn't happen with All(), but it does with One().
		// Leave the check here for absolute and future-proof safety.
		if err == mgo.ErrNotFound {
			return nil, errors.E(Download, errors.NotExist, err)
		}
		return nil, errors.E(Download, errors.IO, err)
	}
	// Detect DB consistency error.
	if len(queryResults) > 1 {
		return nil, errors.E(Download, errors.IO, errors.Str("duplication of indexed key"))
	}
	if len(queryResults) == 0 {
		return nil, errors.E(Download, errors.NotExist, errors.Errorf("ref not found %s", ref))
	}
	return queryResults[0].Contents, nil
}

// Put implements storage.Storage.
func (m *mongoImpl) Put(ref string, contents []byte) (refLink string, error error) {
	// Update or insert the reference with the new contents.
	// This could be cheaper if we knew at this point whether the item exists or not.
	// TODO(edpin): consider a new interface for Storage at some point
	_, err := m.c.Upsert(bson.M{"ref": ref}, &doc{
		Ref:      ref,
		Contents: contents,
	})
	if err != nil {
		return "", errors.E("MongoDB.Put", errors.IO, err)
	}
	return "", nil
}

// ListPrefix implements storage.Storage.
func (m *mongoImpl) ListPrefix(prefix string, depth int) ([]string, error) {
	// TODO: inspect the depth and comply with it.
	return m.ListDir(prefix)
}

// ListDir implements storage.Storage.
func (m *mongoImpl) ListDir(dir string) ([]string, error) {
	const ListDir = "MongoDB.ListDir"
	var queryResults []doc
	// Regex to match the dir prefix.
	regex := bson.M{"ref": bson.M{"$regex": bson.RegEx{Pattern: "^" + dir}}}
	// Retrieve just the names, not the contents and add a reasonable limit so we don't run out of memory.
	err := m.c.Find(regex).Select(bson.M{"ref": 1}).Limit(10000).All(&queryResults)
	if err != nil {
		if err == mgo.ErrNotFound {
			return nil, errors.E(ListDir, errors.NotExist, err)
		}
		return nil, errors.E(ListDir, errors.IO, err)
	}
	if len(queryResults) == 0 {
		return nil, errors.E(ListDir, errors.NotExist, errors.Str("No items found"))
	}
	res := make([]string, len(queryResults))
	for i, r := range queryResults {
		res[i] = r.Ref
	}
	return res, nil
}

// Delete implements storage.Storage.
func (m *mongoImpl) Delete(ref string) error {
	const Delete = "MongoDB.Delete"
	err := m.c.Remove(bson.M{"ref": ref})
	if err != nil {
		if err == mgo.ErrNotFound {
			return errors.E(Delete, errors.NotExist, err)
		}
		return errors.E(Delete, errors.IO, err)
	}
	return nil
}

// Connect implements storage.Storage.
func (m *mongoImpl) Connect() error {
	const Connect = "MongoDB.Connect"
	var err error
	m.sess, err = mgo.Dial("localhost") // TODO parse and use serverAddrs.
	if err != nil {
		return errors.E(Connect, errors.IO, err)
	}
	// TODO: If mongod (the server) restarts all of this goes to hell. We need to monitor our
	// connection and re-dial.
	m.sess.SetMode(mgo.Strong, true)
	m.sess.SetSafe(&mgo.Safe{
		// if there are multiple servers, use WMode: "majority" to confirm writes from the majority of servers.
		J: true, // if using a journal, wait for the journal to sync.
	})
	m.db = m.sess.DB(databaseName)
	m.c = m.db.C(collectionName)
	// This blocks until the index is done. But it needs only be computed the first time
	// and the DB is expected to be empty the first time.
	err = m.c.EnsureIndex(mgo.Index{
		Key:    []string{"ref"},
		Unique: true,
		Sparse: true,
		// Optionally set Background: true if necessary. TODO
	})
	if err != nil {
		return errors.E(Connect, errors.IO, err)
	}
	return nil
}

// Disconnect implements storage.Storage.
func (m *mongoImpl) Disconnect() {
	m.sess.Close()
	m.db.Logout()
	m.sess = nil
	m.db = nil
}
