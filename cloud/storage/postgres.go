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
	_, err = db.Exec(
		`CREATE TABLE IF NOT EXISTS directory (
	             id SERIAL PRIMARY KEY,
	             ref varchar(8000) UNIQUE NOT NULL,
	             data text NOT NULL
	         )`)
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
func (p *postgresImpl) Put(ref string, contents []byte) (refLink string, error error) {
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
		return "", errors.E(Put, errors.IO, errors.Str("spurious updates in SQL DB"))
	}
	return "", nil
}

// ListPrefix implements storage.Storage.
func (p *postgresImpl) ListPrefix(prefix string, depth int) ([]string, error) {
	const ListPrefix = "ListPrefix"
	// TODO: check depth and enforce it.
	query := "SELECT ref FROM directory WHERE ref LIKE $1"
	arg := fmt.Sprintf("%s%%", prefix) // a left-prefix-match.
	return p.commonListDir(ListPrefix, query, arg)
}

// commonListDir implements common functionality shared between ListPrefix and ListDir.
func (p *postgresImpl) commonListDir(op string, query string, args ...interface{}) ([]string, error) {
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
	log.Printf("=== ListDir: %v", res)
	return res, nil
}

// ListDir implements storage.Storage.
func (p *postgresImpl) ListDir(dir string) ([]string, error) {
	const ListDir = "Postgres.ListDir"
	notSubDir := fmt.Sprintf("%s[^/]+/", dir)
	query := "SELECT ref FROM directory WHERE ref ~ $1 AND ref !~ $2"
	return p.commonListDir(ListDir, query, dir, notSubDir)
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
