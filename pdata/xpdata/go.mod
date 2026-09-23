module go.opentelemetry.io/collector/pdata/xpdata

go 1.26.0

require (
	github.com/cespare/xxhash/v2 v2.3.0
	github.com/stretchr/testify v1.12.1
	go.opentelemetry.io/collector/client v1.67.0
	go.opentelemetry.io/collector/pdata v1.67.0
	go.opentelemetry.io/collector/pdata/pprofile v0.161.0
	go.opentelemetry.io/collector/pdata/testdata v0.161.0
	go.opentelemetry.io/otel/trace v1.46.0
	go.uber.org/goleak v1.3.0
)

require (
	github.com/HdrHistogram/hdrhistogram-go v1.2.0 // indirect
	github.com/axiomhq/hyperloglog v0.2.6 // indirect
	github.com/dgryski/go-metro v0.0.0-20250106013310-edb8663e5e33 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/google/flatbuffers v25.12.19+incompatible // indirect
	github.com/kamstrup/intmap v0.5.2 // indirect
	github.com/klauspost/compress v1.18.7 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.26 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/collector/consumer/consumererror v0.161.0 // indirect
	go.opentelemetry.io/otel/metric v1.46.0 // indirect
	golang.org/x/exp v0.0.0-20260824195058-e88cd73687aa // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.83.2 // indirect
)

require (
	github.com/apache/arrow-go/v18 v18.6.0
	github.com/fxamacker/cbor/v2 v2.9.3
	github.com/hashicorp/go-version v1.9.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/open-telemetry/otel-arrow/go v0.56.0
	go.opentelemetry.io/collector/featuregate v1.67.0
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	google.golang.org/protobuf v1.36.12
)

replace go.opentelemetry.io/collector/pdata => ..

replace go.opentelemetry.io/collector/pdata/pprofile => ../pprofile

replace go.opentelemetry.io/collector/pdata/testdata => ../testdata

replace go.opentelemetry.io/collector/client => ../../client

replace go.opentelemetry.io/collector/consumer => ../../consumer

replace go.opentelemetry.io/collector/featuregate => ../../featuregate

replace go.opentelemetry.io/collector/internal/testutil => ../../internal/testutil

replace go.opentelemetry.io/collector/consumer/consumererror => ../../consumer/consumererror
