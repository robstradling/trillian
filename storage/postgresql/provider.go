// Copyright 2018 Google LLC. All Rights Reserved.
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

package postgresql

import (
	"database/sql"
	"flag"
	"sync"

	"github.com/google/trillian/monitoring"
	"github.com/google/trillian/storage"
	"k8s.io/klog/v2"

	// Load PostgreSQL driver
	_ "github.com/go-sql-driver/postgresql"
)

var (
	postgreSQLURI = flag.String("postgresql_uri", "test:zaphod@tcp(127.0.0.1:3306)/test", "Connection URI for PostgreSQL database")
	maxConns = flag.Int("postgresql_max_conns", 0, "Maximum connections to the database")
	maxIdle  = flag.Int("postgresql_max_idle_conns", -1, "Maximum idle database connections in the connection pool")

	postgresqlMu              sync.Mutex
	postgresqlErr             error
	postgresqlDB              *sql.DB
	postgresqlStorageInstance *postgresqlProvider
)

// GetDatabase returns an instance of PostgreSQL database, or creates one.
//
// TODO(pavelkalinnikov): Make the dependency of PostgreSQL quota provider from
// PostgreSQL storage provider explicit.
func GetDatabase() (*sql.DB, error) {
	postgresqlMu.Lock()
	defer postgresqlMu.Unlock()
	return getPostgreSQLDatabaseLocked()
}

func init() {
	if err := storage.RegisterProvider("postgresql", newPostgreSQLStorageProvider); err != nil {
		klog.Fatalf("Failed to register storage provider postgresql: %v", err)
	}
}

type postgresqlProvider struct {
	db *sql.DB
	mf monitoring.MetricFactory
}

func newPostgreSQLStorageProvider(mf monitoring.MetricFactory) (storage.Provider, error) {
	postgresqlMu.Lock()
	defer postgresqlMu.Unlock()
	if postgresqlStorageInstance == nil {
		db, err := getPostgreSQLDatabaseLocked()
		if err != nil {
			return nil, err
		}
		postgresqlStorageInstance = &postgresqlProvider{
			db: db,
			mf: mf,
		}
	}
	return postgresqlStorageInstance, nil
}

// getPostgreSQLDatabaseLocked returns an instance of PostgreSQL database, or creates
// one. Requires postgresqlMu to be locked.
func getPostgreSQLDatabaseLocked() (*sql.DB, error) {
	if postgresqlDB != nil || postgresqlErr != nil {
		return postgresqlDB, postgresqlErr
	}
	db, err := OpenDB(*postgreSQLURI)
	if err != nil {
		postgresqlErr = err
		return nil, err
	}
	if *maxConns > 0 {
		db.SetMaxOpenConns(*maxConns)
	}
	if *maxIdle >= 0 {
		db.SetMaxIdleConns(*maxIdle)
	}
	postgresqlDB, postgresqlErr = db, nil
	return db, nil
}

func (s *postgresqlProvider) LogStorage() storage.LogStorage {
	return NewLogStorage(s.db, s.mf)
}

func (s *postgresqlProvider) AdminStorage() storage.AdminStorage {
	return NewAdminStorage(s.db)
}

func (s *postgresqlProvider) Close() error {
	return s.db.Close()
}
