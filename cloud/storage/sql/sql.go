// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sql

import (
	"database/sql"

	// Required when importing this package.
	_ "github.com/go-sql-driver/mysql"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/log"
)

// sql is a Storage that connects to a SQL backend.
type cloudSQL struct {
	db *sql.DB
}

var _ storage.Storage = (*cloudSQL)(nil)

// PutLocalFile implements storage.Storage.
func (p *cloudSQL) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	// TODO: implement. Only relevant if we want to store blobs though.
	return "", errors.E("SQL.PutLocalFile", errors.Syntax, errors.Str("putlocalfile not implemented for SQL"))
}

// Get implements storage.Storage.
func (p *cloudSQL) Get(ref string) (link string, error error) {
	// TODO: implement. Only relevant if we want to store blobs though.
	return "", errors.E("SQL.Get", errors.Syntax, errors.Str("get not implemented for SQL"))
}

// Download implements storage.Storage.
func (p *cloudSQL) Download(ref string) ([]byte, error) {
	const Download = "SQL.Download"
	var data string
	// QueryRow with $1 parameters ensures we don't have SQL escape problems.
	err := p.db.QueryRow("SELECT data FROM directory WHERE ref = ?", ref).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, errors.E(Download, errors.NotExist, err)
	}
	if err != nil {
		return nil, errors.E(Download, errors.IO, err)
	}
	return []byte(data), nil
}

// Put implements storage.Storage.
func (p *cloudSQL) Put(ref string, contents []byte) (refLink string, error error) {
	const Put = "SQL.Put"
	res, err := p.db.Exec(
		`INSERT INTO directory (ref, data) VALUES (?, ?) ON DUPLICATE KEY UPDATE data = ?`,
		ref, string(contents), string(contents))
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
		return "", errors.E(Put, errors.IO, errors.Str("spurious updates in SQL DB"))
	}
	return "", nil
}

// ListPrefix implements storage.Storage.
func (p *cloudSQL) ListPrefix(prefix string, depth int) ([]string, error) {
	const ListPrefix = "SQL.ListPrefix"
	query := "SELECT ref FROM directory WHERE ref LIKE ?"
	arg := prefix + "%" // a left-prefix-match.
	// TODO: check depth and enforce it.
	return p.commonListDir(ListPrefix, query, arg)
}

// commonListDir implements common functionality shared between ListPrefix and ListDir.
func (p *cloudSQL) commonListDir(op string, query string, args ...interface{}) ([]string, error) {
	rows, err := p.db.Query(query, args...)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	defer rows.Close()
	res := make([]string, 0, 16) // We don't know the size ahead of time without doing a SELECT COUNT.
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
func (p *cloudSQL) ListDir(dir string) ([]string, error) {
	const ListDir = "SQL.ListDir"
	topDir := dir + "%"
	notSubDir := dir + "[^/]+/%"
	// Usage of LIKE and NOT SIMILAR is necessary here to trigger the use of the index.
	// Using posix-regex (i.e. using the operator "~") does not trigger the index.
	query := "SELECT ref FROM directory WHERE ref LIKE ? AND ref NOT SIMILAR TO ?"
	return p.commonListDir(ListDir, query, topDir, notSubDir)
}

// Delete implements storage.Storage.
func (p *cloudSQL) Delete(ref string) error {
	const Delete = "SQL.Delete"
	_, err := p.db.Exec("DELETE FROM directory WHERE ref = ?", ref)
	if err != nil {
		return errors.E(Delete, errors.IO, err)
	}
	return nil
}

// Dial implements storage.Storage.
func (p *cloudSQL) Dial(opts *storage.Opts) error {
	const Dial = "SQL.Dial"
	optStr := buildOptStr(opts)
	log.Printf("Connecting and creating table with options [%s]", optStr)
	db, err := sql.Open("mysql", optStr)
	if err != nil {
		return errors.E(Dial, errors.IO, err)
	}
	// We need a dummy primary key so that we can build an index on ref.
	_, err = db.Exec(
		`CREATE TABLE IF NOT EXISTS directory (
	             id SERIAL PRIMARY KEY,
	             ref varchar(255) UNIQUE NOT NULL,
	             data text NOT NULL
	         )`)
	if err != nil {
		return errors.E(Dial, errors.IO, err)
	}
	// Build a text index on ref to speed up regex pattern matching queries.
	// TODO: this should only be run once.
	/*
		_, err = db.Exec("CREATE INDEX directory_ref_index ON directory (ref);")
		if err != nil {
			return errors.E(Dial, errors.IO, err)
		}
	*/

	// We're ready to have fun with the db.
	p.db = db

	return nil
}

func buildOptStr(opts *storage.Opts) string {
	if v, ok := opts.Opts["dns"]; ok {
		return v
	}
	return ""
}

// Close implements storage.Storage.
func (p *cloudSQL) Close() {
	p.db.Close()
	p.db = nil
}

func init() {
	err := storage.Register("SQL", &cloudSQL{})
	if err != nil {
		log.Fatal(err)
	}
}
