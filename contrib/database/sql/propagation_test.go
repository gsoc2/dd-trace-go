// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net/http"
	"regexp"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummyTransport struct{}

func (d *dummyTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		Body: io.NopCloser(bytes.NewBufferString("{}")),
	}, nil
}

type noopLogger struct{}

func (n *noopLogger) Log(_ string) {
	return
}

func TestDBMPropagation(t *testing.T) {
	// Ensure the global service name is set to the previous value after we finish the test, since the
	// tracer.WithService option overrides it.
	prevServiceName := globalconfig.ServiceName()
	defer globalconfig.SetServiceName(prevServiceName)

	testCases := []struct {
		name           string
		tracerOpts     []tracer.StartOption
		registerOpts   []RegisterOption
		opts           []Option
		skipParentSpan bool
		callDB         func(ctx context.Context, db *sql.DB) error
		prepared       []string
		executed       []*regexp.Regexp
	}{
		{
			name:         "prepare-disabled",
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"SELECT 1 from DUAL"},
		},
		{
			name:         "prepare-service",
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"/*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'*/ SELECT 1 from DUAL"},
		},
		{
			name:         "prepare-service-peer-service",
			tracerOpts:   []tracer.StartOption{tracer.WithPeerServiceDefaults(true)},
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeService)},
			opts:         []Option{WithCustomTag(ext.DBName, "peer-service-db")},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"/*dddbs='peer-service-db',dde='test-env',ddps='test-service',ddpv='1.0.0'*/ SELECT 1 from DUAL"},
		},
		{
			name:         "prepare-service-peer-service-defaults-not-enabled",
			tracerOpts:   []tracer.StartOption{tracer.WithPeerServiceDefaults(false)},
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeService)},
			opts:         []Option{WithCustomTag(ext.DBName, "peer-service-db")},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"/*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'*/ SELECT 1 from DUAL"},
		},
		{
			name:         "prepare-full",
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			prepared: []string{"/*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'*/ SELECT 1 from DUAL"},
		},
		{
			name:         "query-disabled",
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name:         "query-service",
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name:         "query-service-peer-service",
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeService)},
			opts:         []Option{WithCustomTag(ext.PeerService, "peer-service-value")},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='peer-service-value',dde='test-env',ddps='test-service',ddpv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name:         "query-full",
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0',traceparent='00-00000000000000000000000000000001-[\\da-f]{16}-01'\\*/ SELECT 1 from DUAL")},
		},
		{
			name:         "exec-disabled",
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("SELECT 1 from DUAL")},
		},
		{
			name:         "exec-service",
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name:           "exec-service-no-parent-span",
			skipParentSpan: true,
			registerOpts:   []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name:         "exec-service-peer-service",
			tracerOpts:   []tracer.StartOption{tracer.WithPeerServiceDefaults(true)},
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeService)},
			opts:         []Option{WithCustomTag(ext.DBName, "peer-service-db")},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='peer-service-db',dde='test-env',ddps='test-service',ddpv='1.0.0'\\*/ SELECT 1 from DUAL")},
		},
		{
			name:         "exec-full",
			registerOpts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			executed: []*regexp.Regexp{regexp.MustCompile("/\\*dddbs='test.db',dde='test-env',ddps='test-service',ddpv='1.0.0',traceparent='00-00000000000000000000000000000001-[\\da-f]{16}-01'\\*/ SELECT 1 from DUAL")},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipParentSpan {
				t.Skip("TODO: known issue, re-enable when it's fixed")
			}
			tracerOpts := append(tc.tracerOpts,
				tracer.WithLogger(&noopLogger{}),
				tracer.WithService("test-service"),
				tracer.WithEnv("test-env"),
				tracer.WithServiceVersion("1.0.0"),
				tracer.WithHTTPClient(&http.Client{Transport: &dummyTransport{}}),
			)
			tracer.Start(tracerOpts...)
			defer tracer.Stop()

			d := &internal.MockDriver{}
			Register("test", d, tc.registerOpts...)
			defer unregister("test")

			db, err := Open("test", "dn", tc.opts...)
			require.NoError(t, err)

			var (
				ctx = context.Background()
				s   tracer.Span
			)
			if !tc.skipParentSpan {
				s, ctx = tracer.StartSpanFromContext(context.Background(), "test.call", tracer.WithSpanID(1))
			}
			err = tc.callDB(ctx, db)
			if s != nil {
				s.Finish()
			}

			require.NoError(t, err)
			require.Len(t, d.Prepared, len(tc.prepared))
			for i, e := range tc.prepared {
				assert.Equal(t, e, d.Prepared[i])
			}

			require.Len(t, d.Executed, len(tc.executed))
			for i, e := range tc.executed {
				assert.Regexp(t, e, d.Executed[i])
				// the injected span ID should not be the parent's span ID
				assert.NotContains(t, d.Executed[i], "traceparent='00-00000000000000000000000000000001-0000000000000001")
			}
		})
	}
}

func TestDBMTraceContextTagging(t *testing.T) {
	testCases := []struct {
		name                    string
		opts                    []RegisterOption
		callDB                  func(ctx context.Context, db *sql.DB) error
		spanType                string
		traceContextInjectedTag bool
	}{
		{
			name: "prepare",
			opts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.PrepareContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypePrepare,
			traceContextInjectedTag: false,
		},
		{
			name: "query-disabled",
			opts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeQuery,
			traceContextInjectedTag: false,
		},
		{
			name: "query-service",
			opts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeQuery,
			traceContextInjectedTag: false,
		},
		{
			name: "query-full",
			opts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.QueryContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeQuery,
			traceContextInjectedTag: true,
		},
		{
			name: "exec-disabled",
			opts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeDisabled)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeExec,
			traceContextInjectedTag: false,
		},
		{
			name: "exec-service",
			opts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeService)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeExec,
			traceContextInjectedTag: false,
		},
		{
			name: "exec-full",
			opts: []RegisterOption{WithDBMPropagation(tracer.DBMPropagationModeFull)},
			callDB: func(ctx context.Context, db *sql.DB) error {
				_, err := db.ExecContext(ctx, "SELECT 1 from DUAL")
				return err
			},
			spanType:                QueryTypeExec,
			traceContextInjectedTag: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tr := mocktracer.Start()
			defer tr.Stop()

			d := &internal.MockDriver{}
			Register("test", d, tc.opts...)
			defer unregister("test")

			db, err := Open("test", "dn")
			require.NoError(t, err)

			s, ctx := tracer.StartSpanFromContext(context.Background(), "test.call", tracer.WithSpanID(1))
			err = tc.callDB(ctx, db)
			s.Finish()

			require.NoError(t, err)
			spans := tr.FinishedSpans()

			sps := spansOfType(spans, tc.spanType)
			for _, s := range sps {
				tags := s.Tags()
				if tc.traceContextInjectedTag {
					assert.Equal(t, true, tags[keyDBMTraceInjected])
				} else {
					_, ok := tags[keyDBMTraceInjected]
					assert.False(t, ok)
				}
			}
		})
	}
}

func spansOfType(spans []mocktracer.Span, spanType string) (filtered []mocktracer.Span) {
	filtered = make([]mocktracer.Span, 0)
	for _, s := range spans {
		if s.Tag("sql.query_type") == spanType {
			filtered = append(filtered, s)
		}
	}
	return filtered
}
