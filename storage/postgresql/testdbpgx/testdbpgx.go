// Copyright 2017 Google LLC. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package testdb creates new databases for tests.
package testdbpgx

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/trillian/testonly"
	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/pgxpool" // postgresql driver
	_ "github.com/lib/pq"               // postgres driver
)

const (
	// PostgreSQLURIEnv is the name of the ENV variable checked for the test PostgreSQL
	// instance URI to use. The value must have a trailing slash.
	PostgreSQLURIEnv = "TEST_POSTGRESQL_URI"

	// Note: pgxpool.New requires the URI to end with a slash.
	defaultTestPostgreSQLURI = "root@tcp(127.0.0.1)/"

	// CockroachDBURIEnv is the name of the ENV variable checked for the test CockroachDB
	// instance URI to use. The value must have a trailing slash.
	CockroachDBURIEnv = "TEST_COCKROACHDB_URI"

	defaultTestCockroachDBURI = "postgres://root@localhost:26257/?sslmode=disable"
)

type storageDriverInfo struct {
	sqlDriverName string
	schema        string
	uriFunc       func(paths ...string) string
}

var (
	trillianPostgreSQLSchema = testonly.RelativeToPackage("../postgresql/schema/storage.sql")
	trillianCRDBSchema  = testonly.RelativeToPackage("../crdb/schema/storage.sql")
)

// DriverName is the name of a database driver.
type DriverName string

const (
	// DriverPostgreSQL is the identifier for the PostgreSQL storage driver.
	DriverPostgreSQL DriverName = "postgresql"
	// DriverCockroachDB is the identifier for the CockroachDB storage driver.
	DriverCockroachDB DriverName = "cockroachdb"
)

var driverMapping = map[DriverName]storageDriverInfo{
	DriverPostgreSQL: {
		sqlDriverName: "postgresql",
		schema:        trillianPostgreSQLSchema,
		uriFunc:       postgresqlURI,
	},
	DriverCockroachDB: {
		sqlDriverName: "postgres",
		schema:        trillianCRDBSchema,
		uriFunc:       crdbURI,
	},
}

// postgresqlURI returns the PostgreSQL connection URI to use for tests. It returns the
// value in the ENV variable defined by PostgreSQLURIEnv. If the value is empty,
// returns defaultTestPostgreSQLURI.
//
// We use an ENV variable, rather than a flag, for flexibility. Only a subset
// of the tests in this repo require a database and import this package. With a
// flag, it would be necessary to distinguish "go test" invocations that need a
// database, and those that don't. ENV allows to "blanket apply" this setting.
func postgresqlURI(dbRef ...string) string {
	var stringurl string
	if e := os.Getenv(PostgreSQLURIEnv); len(e) > 0 {
		stringurl = e
	} else {
		stringurl = defaultTestPostgreSQLURI
	}

	for _, ref := range dbRef {
		separator := "/"
		if strings.HasSuffix(stringurl, "/") {
			separator = ""
		}
		stringurl = strings.Join([]string{stringurl, ref}, separator)
	}

	return stringurl
}

// crdbURI returns the CockroachDB connection URI to use for tests. It returns the
// value in the ENV variable defined by CockroachDBURIEnv. If the value is empty,
// returns defaultTestCockroachDBURI.
func crdbURI(dbRef ...string) string {
	var uri *url.URL
	if e := os.Getenv(CockroachDBURIEnv); len(e) > 0 {
		uri = getURL(e)
	} else {
		uri = getURL(defaultTestCockroachDBURI)
	}

	return addPathToURI(uri, dbRef...)
}

func addPathToURI(uri *url.URL, paths ...string) string {
	if len(paths) > 0 {
		for _, ref := range paths {
			currentPaths := uri.Path
			// If the path is the root path, we don't want to append a slash.
			if currentPaths == "/" {
				currentPaths = ""
			}
			uri.Path = strings.Join([]string{currentPaths, ref}, "/")
		}
	}
	return uri.String()
}

func getURL(unparsedurl string) *url.URL {
	//nolint:errcheck // We're not expecting an error here.
	u, _ := url.Parse(unparsedurl)
	return u
}

// PostgreSQLAvailable indicates whether the configured PostgreSQL database is available.
func PostgreSQLAvailable() bool {
	return dbAvailable(DriverPostgreSQL)
}

// CockroachDBAvailable indicates whether the configured CockroachDB database is available.
func CockroachDBAvailable() bool {
	return dbAvailable(DriverCockroachDB)
}

func dbAvailable(driver DriverName) bool {
	driverName := driverMapping[driver].sqlDriverName
	uri := driverMapping[driver].uriFunc()
	db, err := pgxpool.New(context.TODO(), uri)
	if err != nil {
		log.Printf("pgxpool.New(): %v", err)
		return false
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("db.Close(): %v", err)
		}
	}()
	if err := db.Ping(context.TODO()); err != nil {
		log.Printf("db.Ping(): %v", err)
		return false
	}
	return true
}

// SetFDLimit sets the soft limit on the maximum number of open file descriptors.
// See http://man7.org/linux/man-pages/man2/setrlimit.2.html
func SetFDLimit(uLimit uint64) error {
	var rLimit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &rLimit); err != nil {
		return err
	}
	if uLimit > rLimit.Max {
		return fmt.Errorf("Could not set FD limit to %v. Must be less than the hard limit %v", uLimit, rLimit.Max)
	}
	rLimit.Cur = uLimit
	return unix.Setrlimit(unix.RLIMIT_NOFILE, &rLimit)
}

// newEmptyDB creates a new, empty database.
// It returns the database handle and a clean-up function, or an error.
// The returned clean-up function should be called once the caller is finished
// using the DB, the caller should not continue to use the returned DB after
// calling this function as it may, for example, delete the underlying
// instance.
func newEmptyDB(ctx context.Context, driver DriverName) (*pgxpool.Pool, func(context.Context), error) {
	if err := SetFDLimit(2048); err != nil {
		return nil, nil, err
	}

	inf, gotinf := driverMapping[driver]
	if !gotinf {
		return nil, nil, fmt.Errorf("unknown driver %q", driver)
	}

	db, err := pgxpool.New(ctx, inf.uriFunc())
	if err != nil {
		return nil, nil, err
	}

	// Create a randomly-named database and then connect using the new name.
	name := fmt.Sprintf("trl_%v", time.Now().UnixNano())

	stmt := fmt.Sprintf("CREATE DATABASE %v", name)
	if _, err := db.Exec(ctx, stmt); err != nil {
		return nil, nil, fmt.Errorf("error running statement %q: %v", stmt, err)
	}

	if err := db.Close(); err != nil {
		return nil, nil, fmt.Errorf("failed to close DB: %v", err)
	}
	uri := inf.uriFunc(name)
	db, err = pgxpool.New(ctx, uri)
	if err != nil {
		return nil, nil, err
	}

	done := func(ctx context.Context) {
		defer func() {
			if err := db.Close(); err != nil {
				klog.Errorf("db.Close(): %v", err)
			}
		}()
		if _, err := db.Exec(ctx, fmt.Sprintf("DROP DATABASE %v", name)); err != nil {
			klog.Warningf("Failed to drop test database %q: %v", name, err)
		}
	}

	return db, done, db.Ping(ctx)
}

// NewTrillianDB creates an empty database with the Trillian schema. The database name is randomly
// generated.
// NewTrillianDB is equivalent to Default().NewTrillianDB(ctx).
func NewTrillianDB(ctx context.Context, driver DriverName) (*pgxpool.Pool, func(context.Context), error) {
	db, done, err := newEmptyDB(ctx, driver)
	if err != nil {
		return nil, nil, err
	}

	schema := driverMapping[driver].schema

	sqlBytes, err := os.ReadFile(schema)
	if err != nil {
		return nil, nil, err
	}

	for _, stmt := range strings.Split(sanitize(string(sqlBytes)), ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(ctx, stmt); err != nil {
			return nil, nil, fmt.Errorf("error running statement %q: %v", stmt, err)
		}
	}
	return db, done, nil
}

func sanitize(script string) string {
	buf := &bytes.Buffer{}
	for _, line := range strings.Split(string(script), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || strings.Index(line, "--") == 0 {
			continue // skip empty lines and comments
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.String()
}

// SkipIfNoPostgreSQL is a test helper that skips tests that require a local PostgreSQL.
func SkipIfNoPostgreSQL(t *testing.T) {
	t.Helper()
	if !PostgreSQLAvailable() {
		t.Skip("Skipping test as PostgreSQL not available")
	}
	t.Logf("Test PostgreSQL available at %q", postgresqlURI())
}

// SkipIfNoCockroachDB is a test helper that skips tests that require a local CockroachDB.
func SkipIfNoCockroachDB(t *testing.T) {
	t.Helper()
	if !CockroachDBAvailable() {
		t.Skip("Skipping test as CockroachDB not available")
	}
	t.Logf("Test CockroachDB available at %q", crdbURI())
}
