module github.com/grafana/mcp-grafana/v2

go 1.26.5

require (
	connectrpc.com/connect v1.21.0
	github.com/PaesslerAG/gval v1.2.4
	github.com/PaesslerAG/jsonpath v0.1.1
	github.com/go-openapi/runtime v0.33.2
	github.com/go-openapi/strfmt v0.27.2
	github.com/google/uuid v1.6.0
	github.com/grafana/amixr-api-go-client v0.0.30
	github.com/grafana/grafana-openapi-client-go v0.0.0-20260828183528-15d99ca95e47
	github.com/grafana/grafana-plugin-sdk-go v0.296.5
	github.com/grafana/incident-go v0.0.0-20260727130053-c21f107e4c42
	github.com/grafana/pyroscope/api v1.6.0
	github.com/invopop/jsonschema v0.14.0
	github.com/itchyny/gojq v0.12.19
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/prometheus/alertmanager v0.34.1
	github.com/prometheus/client_golang v1.24.1
	github.com/prometheus/common v0.71.0
	github.com/prometheus/prometheus v0.314.0
	github.com/stretchr/testify v1.12.1
	go.opentelemetry.io/contrib/bridges/otelslog v0.20.1
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.71.0
	go.opentelemetry.io/contrib/propagators/autoprop v0.71.0
	go.opentelemetry.io/otel v1.46.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.21.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.46.0
	go.opentelemetry.io/otel/exporters/prometheus v0.68.0
	go.opentelemetry.io/otel/metric v1.46.0
	go.opentelemetry.io/otel/sdk v1.46.0
	go.opentelemetry.io/otel/sdk/log v0.22.0
	go.opentelemetry.io/otel/sdk/metric v1.46.0
	go.opentelemetry.io/otel/trace v1.46.0
	go.opentelemetry.io/proto/otlp v1.11.0
	golang.org/x/sync v0.23.0
	golang.org/x/tools v0.50.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	charm.land/bubbles/v2 v2.2.1 // indirect
	charm.land/bubbletea/v2 v2.0.9 // indirect
	charm.land/glamour/v2 v2.0.1 // indirect
	charm.land/huh/v2 v2.0.3 // indirect
	charm.land/lipgloss/v2 v2.0.6 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/Azure/go-ansiterm v0.0.0-20250102033503-faa5f7b0171c // indirect
	github.com/Masterminds/semver/v3 v3.5.0 // indirect
	github.com/NimbleMarkets/ntcharts/v2 v2.2.0 // indirect
	github.com/agnivade/levenshtein v1.2.1 // indirect
	github.com/alecthomas/chroma/v2 v2.27.0 // indirect
	github.com/alecthomas/units v0.0.0-20240927000941-0f3dac36c52b // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.3 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/catppuccin/go v0.3.0 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260910203606-6c9e17dc7a16 // indirect
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/exp/slice v0.0.0-20260803091719-3755ebad01b1 // indirect
	github.com/charmbracelet/x/exp/strings v0.1.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/coreos/go-systemd/v22 v22.7.0 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.1 // indirect
	github.com/dlclark/regexp2/v2 v2.8.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/fxamacker/cbor/v2 v2.9.3 // indirect
	github.com/go-chi/chi/v5 v5.3.2 // indirect
	github.com/go-logfmt/logfmt v0.6.1 // indirect
	github.com/go-openapi/runtime/server-middleware v0.33.2 // indirect
	github.com/go-openapi/swag/pools v0.29.2 // indirect
	github.com/gobwas/glob v0.2.3 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/gofrs/flock v0.13.1 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/gorilla/websocket v1.5.4-0.20250319132907-e064f32e3674 // indirect
	github.com/grafana/authlib/types v0.0.0-20260427145542-9c256e2dbcb2 // indirect
	github.com/grafana/grafana-app-sdk/logging v0.59.0 // indirect
	github.com/grafana/grafana-foundation-sdk/go v0.0.20 // indirect
	github.com/grafana/grafana/pkg/apimachinery v0.0.0-20250903133002-4e28cba1c53a // indirect
	github.com/grafana/otel-checker v0.3.3 // indirect
	github.com/grafana/promql-builder/go v0.0.0-20250916111012-8fa9625b89a3 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-metrics v0.7.0 // indirect
	github.com/hashicorp/go-msgpack/v2 v2.1.5 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-sockaddr v1.0.7 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/hashicorp/memberlist v0.7.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lestrrat-go/blackmagic v1.0.4 // indirect
	github.com/lestrrat-go/dsig v1.4.0 // indirect
	github.com/lestrrat-go/dsig-secp256k1 v1.0.0 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/httprc/v3 v3.0.6 // indirect
	github.com/lestrrat-go/jwx/v3 v3.3.0 // indirect
	github.com/lestrrat-go/option/v2 v2.0.0 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/lrstanley/bubblezone/v2 v2.0.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mdlayher/socket v0.7.0 // indirect
	github.com/mdlayher/vsock v1.3.0 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/miekg/dns v1.1.73 // indirect
	github.com/mitchellh/hashstructure/v2 v2.0.2 // indirect
	github.com/moby/term v0.5.2 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/open-policy-agent/opa v1.20.2 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/exporter-toolkit v0.19.0 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20250401214520-65e299d6c5c9 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/sean-/seed v0.0.0-20170313163322-e2103e2c3529 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/stretchr/objx v0.5.3 // indirect
	github.com/tchap/go-patricia/v2 v2.3.3 // indirect
	github.com/twmb/franz-go v1.22.0 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.14.0 // indirect
	github.com/twmb/franz-go/plugin/kslog v1.0.0 // indirect
	github.com/valyala/fastjson v1.6.10 // indirect
	github.com/vektah/gqlparser/v2 v2.5.37 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xeipuuv/gojsonpointer v0.0.0-20190905194746-02993c407bfb // indirect
	github.com/xeipuuv/gojsonreference v0.0.0-20180127040603-bd5ef7bd5415 // indirect
	github.com/xo/terminfo v1.0.0 // indirect
	github.com/yashtewari/glob-intersection v0.2.0 // indirect
	github.com/yuin/goldmark v1.8.6 // indirect
	github.com/yuin/goldmark-emoji v1.0.6 // indirect
	github.com/zalando/go-keyring v0.2.8 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.46.0 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.6 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/term v0.46.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/telebot.v3 v3.3.8 // indirect
	k8s.io/api v0.37.0 // indirect
	k8s.io/apimachinery v0.37.0 // indirect
	k8s.io/cli-runtime v0.37.0 // indirect
	k8s.io/client-go v0.37.0 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
	k8s.io/kube-openapi v0.0.0-20260821135717-be32def86098 // indirect
	k8s.io/utils v0.0.0-20260707023825-cf1189d6abe3 // indirect
	sigs.k8s.io/json v0.0.0-20260909141634-11ed52e25bc5 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.4.2 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)

require (
	github.com/apache/arrow-go/v18 v18.8.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.47.0 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.33.5 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.20.5 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.20.0 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.10.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.38.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.43.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.51.0 // indirect
	github.com/aws/smithy-go v1.28.2 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/buger/jsonparser v1.6.1 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cheekybits/genny v1.0.0 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/dennwc/varint v1.0.0 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/analysis v1.0.0 // indirect
	github.com/go-openapi/errors v0.22.8 // indirect
	github.com/go-openapi/jsonpointer v1.0.1 // indirect
	github.com/go-openapi/jsonreference v1.0.2 // indirect
	github.com/go-openapi/loads v0.25.3 // indirect
	github.com/go-openapi/spec v1.0.1 // indirect
	github.com/go-openapi/swag v0.29.2 // indirect
	github.com/go-openapi/swag/cmdutils v0.29.2 // indirect
	github.com/go-openapi/swag/conv v0.29.2 // indirect
	github.com/go-openapi/swag/fileutils v0.29.2 // indirect
	github.com/go-openapi/swag/jsonutils v0.29.2 // indirect
	github.com/go-openapi/swag/loading v0.29.2 // indirect
	github.com/go-openapi/swag/mangling v0.29.2 // indirect
	github.com/go-openapi/swag/netutils v0.29.2 // indirect
	github.com/go-openapi/swag/stringutils v0.29.2 // indirect
	github.com/go-openapi/swag/typeutils v0.29.2 // indirect
	github.com/go-openapi/swag/yamlutils v0.29.2 // indirect
	github.com/go-openapi/validate v1.0.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/gogo/googleapis v1.4.1 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/flatbuffers v25.12.19+incompatible // indirect
	github.com/google/gnostic v0.7.1 // indirect
	github.com/google/gnostic-models v0.7.1 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/grafana/gcx v0.0.0
	github.com/grafana/mcp-doc-server v1.0.0
	github.com/grafana/otel-profiling-go v0.6.0 // indirect
	github.com/grafana/pyroscope-go/godeltaprof v0.1.12 // indirect
	github.com/grafana/regexp v0.0.0-20250905093917-f7b3be9d1853 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus v1.1.0 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware/v2 v2.3.4 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-plugin v1.8.0 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/itchyny/timefmt-go v0.1.8 // indirect
	github.com/jaegertracing/jaeger-idl v0.12.0 // indirect
	github.com/jpillora/backoff v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/jszwedko/go-datemath v0.1.1-0.20260113213115-7f666eef0523 // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/mattetti/filebuffer v1.0.1 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mattn/go-runewidth v0.0.30 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/mwitkow/go-conntrack v0.0.0-20190716064945-2f068394615f // indirect
	github.com/oklog/run v1.2.0 // indirect
	github.com/oklog/ulid/v2 v2.1.2 // indirect
	github.com/olekukonko/cat v0.0.0-20250911104152-50322a0618f6 // indirect
	github.com/olekukonko/errors v1.3.0 // indirect
	github.com/olekukonko/ll v0.1.8 // indirect
	github.com/olekukonko/tablewriter v1.1.5 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20250313105119-ba97887b0a25 // indirect
	github.com/prometheus/client_model v0.6.3 // indirect
	github.com/prometheus/otlptranslator v1.0.0 // indirect
	github.com/prometheus/procfs v0.22.0 // indirect
	github.com/prometheus/sigv4 v0.5.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.71.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace v0.71.0 // indirect
	go.opentelemetry.io/contrib/propagators/aws v1.46.0 // indirect
	go.opentelemetry.io/contrib/propagators/b3 v1.46.0 // indirect
	go.opentelemetry.io/contrib/propagators/jaeger v1.46.0 // indirect
	go.opentelemetry.io/contrib/propagators/ot v1.46.0 // indirect
	go.opentelemetry.io/contrib/samplers/jaegerremote v0.37.3 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.46.0 // indirect
	go.opentelemetry.io/otel/log v0.22.0 // indirect
	go.uber.org/atomic v1.12.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20260908205506-85c1c2202aba // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/oauth2 v0.37.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260911204522-f61a6ca850bd // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260911204522-f61a6ca850bd // indirect
	google.golang.org/grpc v1.83.2 // indirect
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v2 v2.4.0 // indirect
)

replace github.com/grafana/gcx => /home/ben/repos/grafana/gcx-embed
