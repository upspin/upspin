package storage

import (
	"database/sql"

	_ "github.com/lib/pq"

	"fmt"
	"upspin.io/errors"
	"upspin.io/log"
)

// postgresImpl is a Storage that connects to an SQL backend and is fine-tuned for Postgres.
type postgresImpl struct {
	db *sql.DB
}

var _ Storage = (*postgresImpl)(nil)

func NewPostgres() (Storage, error) {
	const New = "NewPostgres"
	log.Printf("Connecting and creating table")
	db, err := sql.Open("postgres", "user=postgres host=localhost port=5432 password=postgres sslmode=disable")
	if err != nil {
		return nil, errors.E(New, errors.IO, err)
	}
	// We need a dummy primary key so that we can build an index on ref.
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS directory (id SERIAL PRIMARY KEY, ref varchar(8000) NOT NULL, data text NOT NULL)")
	if err != nil {
		return nil, errors.E(New, errors.IO, err)
	}
	// Build a text index on ref to speed up regex pattern matching queries.
	_, err = db.Exec("CREATE INDEX IF NOT EXISTS directory_ref_index ON directory (ref text_pattern_ops);")
	if err != nil {
		return nil, errors.E(New, errors.IO, err)
	}
	log.Printf("No errors found!")
	return &postgresImpl{
		db: db,
	}, nil
}

// PutLocalFile implements storage.Storage.
func (p *postgresImpl) PutLocalFile(srcLocalFilename string, ref string) (refLink string, error error) {
	return "", errors.E("Postgres.PutLocalFile", errors.Syntax, errors.Str("putlocalfile not implemented for postgres"))
}

// Get implements storage.Storage.
func (p *postgresImpl) Get(ref string) (link string, error error) {
	return "", errors.E("Postgres.Get", errors.Syntax, errors.Str("get not implemented for postgres"))
}

// Download implements storage.Storage.
func (p *postgresImpl) Download(ref string) ([]byte, error) {
	const Download = "Postgres.Download"
	var data string
	// TODO: we need to sanitize ref further to prevent escapes and shenanigans like "'foo'; DROP TABLES *;".
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
func (p *postgresImpl) Put(ref string, contents []byte) (refLink string, error error) {
	const Put = "Postgres.Put"
	// TODO: escape single quotes (') and sanitize to prevent shenanigans like "'foo'; DROP TABLES *;".
	sql := fmt.Sprintf(
		`INSERT INTO directory (ref, data) values ('%s', '%s') ON CONFLICT (ref) DO UPDATE SET data = '%s';`,
		ref, string(contents), string(contents))
	log.Printf("=== going to insert: %s", sql)
	res, err := p.db.Exec(sql)
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
func (p *postgresImpl) ListPrefix(prefix string, depth int) ([]string, error) {
	// TODO: check depth and enforce it.
	return p.ListDir(prefix)
}

// ListDir implements storage.Storage.
func (p *postgresImpl) ListDir(dir string) ([]string, error) {
	const ListDir = "Postgres.ListDir"

	// We need to use fmt here because db.Query fails on parsing "~" with an argument.
	sqlStr := fmt.Sprintf("SELECT ref FROM directory WHERE ref ~ '^%s';", dir)
	rows, err := p.db.Query(sqlStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, errors.E(ListDir, errors.IO, err)
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
		return res, errors.E(ListDir, errors.IO, err)
	}
	return res, nil
}

// Delete implements storage.Storage.
func (p *postgresImpl) Delete(ref string) error {
	// TODO.
	return nil
}

// Connect implements storage.Storage.
func (p *postgresImpl) Connect() error {
	return nil
}

// Disconnect implements storage.Storage.
func (p *postgresImpl) Disconnect() {
}
