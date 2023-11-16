// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpcsec

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/dyngo/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/dyngo/instrumentation/httpsec"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// SetSecurityEventsTags sets the AppSec events span tags.
func SetSecurityEventsTags(span ddtrace.Span, events []any) {
	if err := setSecurityEventsTags(span, events); err != nil {
		log.Error("appsec: unexpected error while creating the appsec events tags: %v", err)
	}
}

func setSecurityEventsTags(span ddtrace.Span, events []any) error {
	if events == nil {
		return nil
	}
	return instrumentation.SetEventSpanTags(span, events)
}

// SetRequestMetadataTags sets the gRPC request metadata span tags.
func SetRequestMetadataTags(span ddtrace.Span, md map[string][]string) {
	for h, v := range httpsec.NormalizeHTTPHeaders(md) {
		span.SetTag("grpc.metadata."+h, v)
	}
}
