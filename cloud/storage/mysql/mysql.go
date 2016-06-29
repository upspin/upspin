// Copyright 2016 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mysql implements a storage.Storage using mySQL as the backend.
package mysql

import (
	"database/sql"

	// Required when importing this package.
	_ "github.com/go-sql-driver/mysql"

	"upspin.io/cloud/storage"
	"upspin.io/errors"
	"upspin.io/log"
)

// mysql is a Storage that connects to a mySQL backend.
type mysql struct {
	db *sql.DB
}

var _ storage.Storage = (*mysql)(nil)

// PutLocalFile implements storage.Storage.
func (p *mysql) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	// TODO: implement. Only relevant if we want to store blobs though.
	return "", errors.E("SQL.PutLocalFile", errors.Syntax, errors.Str("putlocalfile not implemented for SQL"))
}

// Get implements storage.Storage.
func (p *mysql) Get(ref string) (link string, error error) {
	// TODO: implement. Only relevant if we want to store blobs though.
	return "", errors.E("SQL.Get", errors.Syntax, errors.Str("get not implemented for SQL"))
}

// Download implements storage.Storage.
func (p *mysql) Download(ref string) ([]byte, error) {
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
func (p *mysql) Put(ref string, contents []byte) (refLink string, error error) {
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
func (p *mysql) ListPrefix(prefix string, depth int) ([]string, error) {
	const ListPrefix = "SQL.ListPrefix"
	query := "SELECT ref FROM directory WHERE ref LIKE ?"
	arg := prefix + "%" // a left-prefix-match.
	// TODO: check depth and enforce it.
	return p.commonListDir(ListPrefix, query, arg)
}

// commonListDir implements common functionality shared between ListPrefix and ListDir.
func (p *mysql) commonListDir(op string, query string, args ...interface{}) ([]string, error) {
	rows, err := p.db.Query(query, args...)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, errors.E(op, errors.IO, err)
	}
	defer rows.Close()
	var res []string
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
func (p *mysql) ListDir(dir string) ([]string, error) {
	const ListDir = "SQL.ListDir"
	topDir := dir + "%"
	notSubDir := dir + "[^/]+/%"
	// Usage of LIKE and NOT SIMILAR is necessary here to trigger the use of the index.
	// Using posix-regex (i.e. using the operator "~") does not trigger the index.
	query := "SELECT ref FROM directory WHERE ref LIKE ? AND ref NOT LIKE ?"
	return p.commonListDir(ListDir, query, topDir, notSubDir)
}

// Delete implements storage.Storage.
func (p *mysql) Delete(ref string) error {
	const Delete = "SQL.Delete"
	_, err := p.db.Exec("DELETE FROM directory WHERE ref = ?", ref)
	if err != nil {
		return errors.E(Delete, errors.IO, err)
	}
	return nil
}

// Dial implements storage.Storage.
func (p *mysql) Dial(opts *storage.Opts) error {
	const Dial = "SQL.Dial"
	optStr := buildOptStr(opts)
	log.Printf("Connecting and creating table with options [%s]", optStr)
	db, err := sql.Open("mysql", optStr)
	if err != nil {
		return errors.E(Dial, errors.IO, err)
	}
	// The ref is *not* unique because text can't be unique unless it's a varchar and then
	// we're limited to 255 chars. We enforce uniqueness at higher layers already so this is not
	// a problem.
	_, err = db.Exec(
		`CREATE TABLE IF NOT EXISTS directory (
	             id SERIAL PRIMARY KEY,
	             ref varchar(255) unique NOT NULL,
	             data text NOT NULL
	         )`)
	if err != nil {
		return errors.E(Dial, errors.IO, err)
	}
	// Build a text index on ref to speed up regex pattern matching queries, if one does not exist yet.
	var res int
	err = db.QueryRow(
		`SELECT COUNT(1) FROM INFORMATION_SCHEMA.STATISTICS
		 WHERE table_schema=DATABASE() AND
		       table_name='directory' AND
		       index_name='directory_ref_index'`).Scan(&res)
	if err != nil {
		return errors.E(Dial, errors.IO, err)
	}
	if res == 0 {
		// Index is not there. Create it now.
		// Note that mySQL will limit the indexing key to 767 characters even if we ask for more, but some
		// mySQL versions fail if we ask for more.
		_, err = db.Exec("CREATE INDEX directory_ref_index ON directory (ref);")
		if err != nil {
			return errors.E(Dial, errors.IO, err)
		}
	}

	// We're ready to have fun with the db.
	p.db = db

	return nil
}

func buildOptStr(opts *storage.Opts) string {
	if v, ok := opts.Opts["dsn"]; ok {
		return v
	}
	return ""
}

// Close implements storage.Storage.
func (p *mysql) Close() {
	p.db.Close()
	p.db = nil
}

func init() {
	err := storage.Register("mysql", &mysql{})
	if err != nil {
		log.Fatal(err)
	}
}
