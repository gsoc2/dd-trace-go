// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package telemetry

import "net/http"

// Request captures all necessary information for a telemetry event submission
// so we do not need to read directly from our telemetry client when submitting
// asynchronously
type Request struct {
	Body       *Body
	Header     *http.Header
	HTTPClient *http.Client
	URL        string
}

// Body is the common high-level structure encapsulating a telemetry request body
type Body struct {
	APIVersion  string      `json:"api_version"`
	RequestType RequestType `json:"request_type"`
	TracerTime  int64       `json:"tracer_time"`
	RuntimeID   string      `json:"runtime_id"`
	SeqID       int64       `json:"seq_id"`
	Debug       bool        `json:"debug"`
	Payload     interface{} `json:"payload"`
	Application Application `json:"application"`
	Host        Host        `json:"host"`
}

// RequestType determines how the Payload of a request should be handled
type RequestType string

const (
	// RequestTypeAppStarted is the first message sent by the telemetry
	// client, containing the configuration, and integrations and
	// dependencies loaded at startup
	RequestTypeAppStarted RequestType = "app-started"
	// RequestTypeAppHeartbeat is sent periodically by the client to indicate
	// that the app is still running
	RequestTypeAppHeartbeat RequestType = "app-heartbeat"
	// RequestTypeGenerateMetrics contains all metrics accumulated by the
	// client, and is sent periodically along with the heartbeat
	RequestTypeGenerateMetrics RequestType = "generate-metrics"
	// RequestTypeAppClosing is sent when the telemetry client is stopped
	RequestTypeAppClosing RequestType = "app-closing"
	// RequestTypeDependenciesLoaded is sent if DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED
	// is enabled. Sent when Start is called for the telemetry client.
	RequestTypeDependenciesLoaded RequestType = "app-dependencies-loaded"
	// RequestTypeAppClientConfigurationChange is sent if there are changes
	// to the client library configuration
	RequestTypeAppClientConfigurationChange RequestType = "app-client-configuration-change"
	// RequestTypeAppProductChange is sent when products are enabled/disabled
	RequestTypeAppProductChange RequestType = "app-product-change"
)

// Namespace describes an APM product to distinguish telemetry coming from
// different products used by the same application
type Namespace string

const (
	// NamespaceTracers is for distributed tracing
	NamespaceTracers Namespace = "tracers"
	// NamespaceProfilers is for continuous profiling
	NamespaceProfilers Namespace = "profilers"
	// NamespaceASM is for application security monitoring
	NamespaceASM Namespace = "appsec" // This was defined before the appsec -> ASM change
)

// Application is identifying information about the app itself
type Application struct {
	ServiceName     string `json:"service_name"`
	Env             string `json:"env"`
	ServiceVersion  string `json:"service_version"`
	TracerVersion   string `json:"tracer_version"`
	LanguageName    string `json:"language_name"`
	LanguageVersion string `json:"language_version"`
	RuntimeName     string `json:"runtime_name"`
	RuntimeVersion  string `json:"runtime_version"`
	RuntimePatches  string `json:"runtime_patches,omitempty"`
}

// Host is identifying information about the host on which the app
// is running
type Host struct {
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	OSVersion string `json:"os_version,omitempty"`
	// TODO: Do we care about the kernel stuff? internal/osinfo gets most of
	// this information in OSName/OSVersion
	Architecture  string `json:"architecture"`
	KernelName    string `json:"kernel_name"`
	KernelRelease string `json:"kernel_release"`
	KernelVersion string `json:"kernel_version"`
}

// AppStarted corresponds to the "app-started" request type
type AppStarted struct {
	Configuration     []Configuration     `json:"configuration,omitempty"`
	Products          Products            `json:"products,omitempty"`
	AdditionalPayload []AdditionalPayload `json:"additional_payload,omitempty"`
	Error             Error               `json:"error,omitempty"`
}

// ConfigurationChange corresponds to the `AppClientConfigurationChange` event
// that contains information about configuration changes since the app-started event
type ConfigurationChange struct {
	Configuration []Configuration `json:"conf_key_values"`
	RemoteConfig  RemoteConfig    `json:"remote_config"`
}

// Configuration is a library-specific configuration value
// that should be initialized through StringConfig, IntConfig, FloatConfig, or BoolConfig
type Configuration struct {
	Name  string      `json:"name"`
	Value interface{} `json:"value"`
	// origin is the source of the config. It is one of {env_var, code, dd_config, remote_config}
	Origin      string `json:"origin"`
	Error       Error  `json:"error"`
	IsOverriden bool   `json:"is_overridden"`
}

// TODO: be able to pass in origin, error, isOverriden info to config
// constructors

// StringConfig returns a Configuration struct with a string value
func StringConfig(key string, val string) Configuration {
	return Configuration{Name: key, Value: val}
}

// IntConfig returns a Configuration struct with a int value
func IntConfig(key string, val int) Configuration {
	return Configuration{Name: key, Value: val}
}

// FloatConfig returns a Configuration struct with a float value
func FloatConfig(key string, val float64) Configuration {
	return Configuration{Name: key, Value: val}
}

// BoolConfig returns a Configuration struct with a bool value
func BoolConfig(key string, val bool) Configuration {
	return Configuration{Name: key, Value: val}
}

// Products specifies information about available products.
type Products struct {
	AppSec   ProductDetails `json:"appsec,omitempty"`
	Profiler ProductDetails `json:"profiler,omitempty"`
}

// ProductDetails specifies details about a product.
type ProductDetails struct {
	Enabled bool   `json:"enabled"`
	Version string `json:"version,omitempty"`
	Error   Error  `json:"error,omitempty"`
}

// Dependencies stores a list of dependencies
type Dependencies struct {
	Dependencies []Dependency `json:"dependencies"`
}

// Dependency is a Go module on which the application depends. This information
// can be accesed at run-time through the runtime/debug.ReadBuildInfo API.
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// RemoteConfig contains information about remote-config
type RemoteConfig struct {
	UserEnabled     string `json:"user_enabled"`     // whether the library has made a request to fetch remote-config
	ConfigsRecieved bool   `json:"configs_received"` // whether the library recieves a valid config response
	Error           Error  `json:"error"`
}

// Error stores error information about various tracer events
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// AdditionalPayload can be used to add extra information to the app-started
// event
type AdditionalPayload struct {
	Name  string      `json:"name"`
	Value interface{} `json:"value"`
}

// Metrics corresponds to the "generate-metrics" request type
type Metrics struct {
	Namespace Namespace `json:"namespace"`
	Series    []Series  `json:"series"`
}

// Series is a sequence of observations for a single named metric
type Series struct {
	Metric string       `json:"metric"`
	Points [][2]float64 `json:"points"`
	Type   string       `json:"type"`
	Tags   []string     `json:"tags"`
	// Common distinguishes metrics which are cross-language vs.
	// language-specific.
	//
	// NOTE: If this field isn't present in the request, the API assumes
	// assumed the metric is common. So we can't "omitempty" even though the
	// field is technically optional.
	Common bool `json:"common"`
}
