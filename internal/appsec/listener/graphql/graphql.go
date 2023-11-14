// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	"sync"
	"time"

	waf "github.com/DataDog/go-libddwaf/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/graphqlsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/limiter"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/util"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
)

// GraphQL rule addresses currently supported by the WAF
const (
	graphQLServerAllResolversAddr = "graphql.server.all_resolvers"
	graphQLServerResolverAddr     = "graphql.server.resolver"
)

// List of GraphQL rule addresses currently supported by the WAF
var supportedpAddresses = map[string]struct{}{
	graphQLServerAllResolversAddr: {},
	graphQLServerResolverAddr:     {},
}

func SupportsAddress(addr string) bool {
	_, ok := supportedpAddresses[addr]
	return ok
}

// NewWAFEventListener returns the WAF event listener to register in order
// to enable it.
func NewWAFEventListener(handle *waf.Handle, _ sharedsec.Actions, addresses map[string]struct{}, timeout time.Duration, limiter limiter.Limiter) dyngo.EventListener {
	return graphqlsec.OnQueryStart(func(query *graphqlsec.Query, args graphqlsec.QueryArguments) {
		wafCtx := waf.NewContext(handle)
		if wafCtx == nil {
			return
		}

		var (
			allResolvers   map[string][]map[string]any
			allResolversMu sync.Mutex
		)

		query.On(graphqlsec.OnFieldStart(func(field *graphqlsec.Field, args graphqlsec.FieldArguments) {
			if _, found := addresses[graphQLServerResolverAddr]; found {
				wafResult := util.RunWAF(
					wafCtx,
					waf.RunAddressData{
						Ephemeral: map[string]any{
							graphQLServerResolverAddr: map[string]any{args.FieldName: args.Arguments},
						},
					},
					timeout,
				)
				util.AddSecurityEvents(field, limiter, wafResult.Events)
			}

			if args.FieldName != "" {
				// Register in all resolvers
				allResolversMu.Lock()
				defer allResolversMu.Unlock()
				if allResolvers == nil {
					allResolvers = make(map[string][]map[string]any)
				}
				allResolvers[args.FieldName] = append(allResolvers[args.FieldName], args.Arguments)
			}

			// field.On(graphqlsec.OnFieldFinish(func(field *graphqlsec.Field, res graphqlsec.FieldResult) {}))
		}))

		query.On(graphqlsec.OnQueryFinish(func(query *graphqlsec.Query, res graphqlsec.QueryResult) {
			defer wafCtx.Close()

			if _, found := addresses[graphQLServerAllResolversAddr]; found && len(allResolvers) > 0 {
				// TODO: this is currently happening AFTER the resolvers have all run, which is... too late to block side-effects.
				wafResult := util.RunWAF(wafCtx, waf.RunAddressData{Persistent: map[string]any{graphQLServerAllResolversAddr: allResolvers}}, timeout)
				util.AddSecurityEvents(query, limiter, wafResult.Events)
			}

			wafDiags := handle.Diagnostics()
			overall, internal := wafCtx.TotalRuntime()
			nbTimeouts := wafCtx.TotalTimeouts()
			util.AddWAFMonitoringTags(query, wafDiags.Version, overall, internal, nbTimeouts)

			util.AddRulesMonitoringTags(query, &wafDiags)
			query.AddTag(ext.ManualKeep, samplernames.AppSec)
		}))
	})
}
