// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package postgres implements a storage backend for interfacing with a Postgres database.
package postgres

import (
	"bytes"
	"database/sql"
	"fmt"

	// Required when importing this package.
	_ "github.com/lib/pq"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/log"
)

// postgres is a Storage that connects to a Postgres backend.
// It likely won't work with other SQL databases because of a few
// Postgres-ism such as the text index and how "upsert" is handled.
type postgres struct {
	db *sql.DB
}

var _ storage.Storage = (*postgres)(nil)

// PutLocalFile implements storage.Storage.
func (p *postgres) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	// TODO: implement. Only relevant if we want to store blobs though.
	return "", errors.E("Postgres.PutLocalFile", errors.Syntax, errors.Str("putlocalfile not implemented for postgres"))
}

// Get implements storage.Storage.
func (p *postgres) Get(ref string) (link string, error error) {
	// TODO: implement. Only relevant if we want to store blobs though.
	return "", errors.E("Postgres.Get", errors.Syntax, errors.Str("get not implemented for postgres"))
}

// Download implements storage.Storage.
func (p *postgres) Download(ref string) ([]byte, error) {
	const Download = "Postgres.Download"
	var data string
	// QueryRow with $1 parameters ensures we don't have SQL escape problems.
	err := p.db.QueryRow("SELECT data FROM directory WHERE ref = $1;", ref).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, errors.E(Download, errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(Download, errors.IO, err)
	}
	return []byte(data), nil
}

// Put implements storage.Storage.
func (p *postgres) Put(ref string, contents []byte) (refLink string, error error) {
	const Put = "Postgres.Put"
	res, err := p.db.Exec(
		`INSERT INTO directory (ref, data) values ($1, $2) ON CONFLICT (ref) DO UPDATE SET data = $2;`,
		ref, string(contents))
	if err != nil {
		return "", errors.E(Put, errors.IO, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		// No information. Assume success.
		return "", nil
	}
	if n != 1 {
		// Something went wrong.
		return "", errors.E(Put, errors.IO, errors.Errorf("spurious updates in SQL DB, expected 1, got %d", n))
	}
	return "", nil
}

// ListPrefix implements storage.Storage.
func (p *postgres) ListPrefix(prefix string, depth int) ([]string, error) {
	const ListPrefix = "Postgres.ListPrefix"
	query := "SELECT ref FROM directory WHERE ref LIKE $1"
	arg := prefix + "%" // a left-prefix-match.
	// TODO: check depth and enforce it.
	return p.commonListDir(ListPrefix, query, arg)
}

// commonListDir implements common functionality shared between ListPrefix and ListDir.
func (p *postgres) commonListDir(op string, query string, args ...interface{}) ([]string, error) {
	rows, err := p.db.Query(query, args...)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	defer rows.Close()
	var res []string // We don't know the size ahead of time without doing a SELECT COUNT.
	var firstErr error
	saveErr := func(err error) {
		if firstErr != nil {
			firstErr = err
		}
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			saveErr(err)
			continue
		}
		res = append(res, name)
	}
	if err := rows.Err(); err != nil {
		saveErr(err)
	}
	if firstErr != nil {
		return res, errors.E(op, errors.IO, err)
	}
	return res, nil
}

// ListDir implements storage.Storage.
func (p *postgres) ListDir(dir string) ([]string, error) {
	const ListDir = "Postgres.ListDir"
	topDir := dir + "%"
	notSubDir := dir + "[^/]+/%"
	// Usage of LIKE and NOT SIMILAR is necessary here to trigger the use of the index.
	// Using posix-regex (i.e. using the operator "~") does not trigger the index.
	query := "SELECT ref FROM directory WHERE ref LIKE $1 AND ref NOT SIMILAR TO $2"
	return p.commonListDir(ListDir, query, topDir, notSubDir)
}

// Delete implements storage.Storage.
func (p *postgres) Delete(ref string) error {
	const Delete = "Postgres.Delete"
	_, err := p.db.Exec("DELETE FROM directory WHERE ref = $1", ref)
	if err != nil {
		return errors.E(Delete, errors.IO, err)
	}
	return nil
}

// Dial implements storage.Storage.
func (p *postgres) Dial(opts *storage.Opts) error {
	const Dial = "Postgres.Dial"
	optStr := buildOptStr(opts)
	log.Printf("Connecting and creating table with options [%s]", optStr)
	db, err := sql.Open("postgres", optStr)
	if err != nil {
		return errors.E(Dial, errors.IO, err)
	}
	// We need a dummy primary key so that we can build an index on ref.
	_, err = db.Exec(
		`CREATE TABLE IF NOT EXISTS directory (
	             id SERIAL PRIMARY KEY,
	             ref varchar(8000) UNIQUE NOT NULL,
	             data text NOT NULL
	         )`)
	if err != nil {
		return errors.E(Dial, errors.IO, err)
	}
	// Build a text index on ref to speed up regex pattern matching queries.
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS directory_ref_index ON directory (ref text_pattern_ops);")
	if err != nil {
		return errors.E(Dial, errors.IO, err)
	}

	// Set the db and go!
	p.db = db

	return nil
}

func buildOptStr(opts *storage.Opts) string {
	var b bytes.Buffer
	first := true
	for k, v := range opts.Opts {
		if !first {
			fmt.Fprintf(&b, " %s=%s", k, v)
		} else {
			fmt.Fprintf(&b, "%s=%s", k, v)
			first = false
		}
	}
	return b.String()
}

// Close implements storage.Storage.
func (p *postgres) Close() {
	p.db.Close()
	p.db = nil
}

func init() {
	err := storage.Register("Postgres", &postgres{})
	if err != nil {
		log.Fatal(err)
	}
}
