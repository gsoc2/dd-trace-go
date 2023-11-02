// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package sql provides functions to trace the database/sql package (https://golang.org/pkg/database/sql).
// It will automatically augment operations such as connections, statements and transactions with tracing.
//
// We start by telling the package which driver we will be using. For example, if we are using "github.com/lib/pq",
// we would do as follows:
//
//	sqltrace.Register("pq", &pq.Driver{})
//	db, err := sqltrace.Open("pq", "postgres://pqgotest:password@localhost...")
//
// The rest of our application would continue as usual, but with tracing enabled.
package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"reflect"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "database/sql"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported(componentName)
}

// registeredDrivers holds a registry of all drivers registered via the sqltrace package.
var registeredDrivers = &driverRegistry{
	keys:    make(map[reflect.Type]string),
	drivers: make(map[string]driver.Driver),
	configs: make(map[string]*config),
}

type driverRegistry struct {
	// keys maps driver types to their registered names.
	keys map[reflect.Type]string
	// drivers maps keys to their registered driver.
	drivers map[string]driver.Driver
	// configs maps keys to their registered configuration.
	configs map[string]*config
	// mu protects the above maps.
	mu sync.RWMutex
}

// isRegistered reports whether the name matches an existing entry
// in the driver registry.
func (d *driverRegistry) isRegistered(name string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.configs[name]
	return ok
}

// add adds the driver with the given name and config to the registry.
func (d *driverRegistry) add(name string, driver driver.Driver, cfg *config) {
	if d.isRegistered(name) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.keys[reflect.TypeOf(driver)] = name
	d.drivers[name] = driver
	d.configs[name] = cfg
}

// name returns the name of the driver stored in the registry.
func (d *driverRegistry) name(driver driver.Driver) (string, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	name, ok := d.keys[reflect.TypeOf(driver)]
	return name, ok
}

// driver returns the driver stored in the registry with the provided name.
func (d *driverRegistry) driver(name string) (driver.Driver, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	driver, ok := d.drivers[name]
	return driver, ok
}

// config returns the config stored in the registry with the provided name.
func (d *driverRegistry) config(name string) (*config, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	config, ok := d.configs[name]
	return config, ok
}

// unregister is used to make tests idempotent.
func (d *driverRegistry) unregister(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	driver := d.drivers[name]
	delete(d.keys, reflect.TypeOf(driver))
	delete(d.configs, name)
	delete(d.drivers, name)
}

// Register tells the sql integration package about the driver that we will be tracing. If used, it
// must be called before Open. It uses the driverName suffixed with ".db" as the default service
// name.
func Register(driverName string, driver driver.Driver, opts ...RegisterOption) {
	if driver == nil {
		panic("sqltrace: Register driver is nil")
	}
	if registeredDrivers.isRegistered(driverName) {
		// already registered, don't change things
		return
	}

	cfg := new(config)
	defaults(cfg, driverName, nil)
	processOptions(cfg, driverName, opts...)
	log.Debug("contrib/database/sql: Registering driver: %s %#v", driverName, cfg)
	registeredDrivers.add(driverName, driver, cfg)
}

// unregister is used to make tests idempotent.
func unregister(name string) {
	if registeredDrivers.isRegistered(name) {
		registeredDrivers.unregister(name)
	}
}

type tracedConnector struct {
	connector  driver.Connector
	driverName string
	cfg        *config
}

func (t *tracedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	tp := &traceParams{
		driverName: t.driverName,
		cfg:        t.cfg,
	}
	if dc, ok := t.connector.(*dsnConnector); ok {
		tp.meta, _ = internal.ParseDSN(t.driverName, dc.dsn)
	} else if t.cfg.dsn != "" {
		tp.meta, _ = internal.ParseDSN(t.driverName, t.cfg.dsn)
	}
	start := time.Now()
	ctx, end := startTraceTask(ctx, string(QueryTypeConnect))
	defer end()
	conn, err := t.connector.Connect(ctx)
	tp.tryTrace(ctx, QueryTypeConnect, "", start, err)
	if err != nil {
		return nil, err
	}
	return &TracedConn{conn, tp}, err
}

func (t *tracedConnector) Driver() driver.Driver {
	return t.connector.Driver()
}

// from Go stdlib implementation of sql.Open
type dsnConnector struct {
	dsn    string
	driver driver.Driver
}

func (t dsnConnector) Connect(_ context.Context) (driver.Conn, error) {
	return t.driver.Open(t.dsn)
}

func (t dsnConnector) Driver() driver.Driver {
	return t.driver
}

// OpenDB returns connection to a DB using the traced version of the given driver. The driver may
// first be registered using Register. If this did not occur, OpenDB will determine the driver name
// based on its type.
func OpenDB(c driver.Connector, opts ...Option) *sql.DB {
	cfg := new(config)
	var driverName string
	if name, ok := registeredDrivers.name(c.Driver()); ok {
		driverName = name
		rc, _ := registeredDrivers.config(driverName)
		defaults(cfg, driverName, rc)
	} else {
		driverName = reflect.TypeOf(c.Driver()).String()
		defaults(cfg, driverName, nil)
	}
	processOptions(cfg, driverName, opts...)
	tc := &tracedConnector{
		connector:  c,
		driverName: driverName,
		cfg:        cfg,
	}
	return sql.OpenDB(tc)
}

// Open returns connection to a DB using the traced version of the given driver. The driver may
// first be registered using Register. If this did not occur, Open will determine the driver by
// opening a DB connection and retrieving the driver using (*sql.DB).Driver, before closing it and
// opening a new, traced connection.
func Open(driverName, dataSourceName string, opts ...Option) (*sql.DB, error) {
	var d driver.Driver
	if registeredDrivers.isRegistered(driverName) {
		d, _ = registeredDrivers.driver(driverName)
	} else {
		db, err := sql.Open(driverName, dataSourceName)
		if err != nil {
			return nil, err
		}
		defer db.Close()
		d = db.Driver()
		Register(driverName, d)
	}

	if driverCtx, ok := d.(driver.DriverContext); ok {
		connector, err := driverCtx.OpenConnector(dataSourceName)
		if err != nil {
			return nil, err
		}
		// since we're not using the dsnConnector, we need to register the dsn manually in the config
		opts = append(opts, WithDSN(dataSourceName))
		return OpenDB(connector, opts...), nil
	}
	return OpenDB(&dsnConnector{dsn: dataSourceName, driver: d}, opts...), nil
}

func processOptions(cfg *config, driverName string, opts ...Option) {
	for _, fn := range opts {
		fn(cfg)
	}
	checkDBMPropagation(cfg, driverName)
}

func checkDBMPropagation(cfg *config, driverName string) {
	fullModeSupported := func() bool {
		unsupportedDrivers := []string{"sqlserver", "oracle"}
		for _, dr := range unsupportedDrivers {
			if dr == driverName {
				return false
			}
		}
		return true
	}
	if cfg.dbmPropagationMode == tracer.DBMPropagationModeFull && !fullModeSupported() {
		log.Warn("Using DBM_PROPAGATION_MODE in 'full' mode is not supported for %s. See "+
			"https://docs.datadoghq.com/database_monitoring/connect_dbm_and_apm/ for more info.",
			driverName,
		)
		cfg.dbmPropagationMode = tracer.DBMPropagationModeService
	}
}
