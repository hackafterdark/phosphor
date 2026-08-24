module github.com/hackafterdark/phosphor

go 1.26.6

require (
	charm.land/bubbles/v2 v2.2.0
	charm.land/bubbletea/v2 v2.0.9
	charm.land/catwalk v0.52.2
	charm.land/fang/v2 v2.0.1
	charm.land/fantasy v0.41.2
	charm.land/glamour/v2 v2.0.1
	charm.land/lipgloss/v2 v2.0.6
	charm.land/log/v2 v2.0.0
	charm.land/x/vcr v0.1.1
	github.com/Arize-ai/openinference/go/openinference-semantic-conventions v0.1.3
	github.com/JohannesKaufmann/html-to-markdown v1.6.0
	github.com/Microsoft/go-winio v0.6.2
	github.com/NimbleMarkets/ntcharts v0.5.1
	github.com/PuerkitoBio/goquery v1.12.0
	github.com/alecthomas/chroma/v2 v2.27.0
	github.com/aymanbagabas/go-udiff v0.4.1
	github.com/bmatcuk/doublestar/v4 v4.10.0
	github.com/charlievieth/fastwalk v1.0.14
	github.com/charmbracelet/colorprofile v0.4.3
	github.com/charmbracelet/lipgloss v1.1.0
	github.com/charmbracelet/ultraviolet v0.0.0-20260811164956-006e29f97886
	github.com/charmbracelet/x/ansi v0.11.8
	github.com/charmbracelet/x/editor v0.2.0
	github.com/charmbracelet/x/etag v0.2.0
	github.com/charmbracelet/x/exp/charmtone v0.0.0-20260713092006-0d683c34c74b
	github.com/charmbracelet/x/exp/golden v0.0.0-20250806222409-83e3a29d542f
	github.com/charmbracelet/x/exp/ordered v0.1.0
	github.com/charmbracelet/x/exp/slice v0.0.0-20260713092006-0d683c34c74b
	github.com/charmbracelet/x/exp/strings v0.1.0
	github.com/charmbracelet/x/powernap v0.1.6
	github.com/charmbracelet/x/term v0.2.2
	github.com/clipperhouse/displaywidth v0.11.0
	github.com/clipperhouse/uax29/v2 v2.7.0
	github.com/disintegration/imaging v1.6.2
	github.com/dustin/go-humanize v1.0.1
	github.com/fsnotify/fsnotify v1.10.1
	github.com/gen2brain/beeep v0.11.2
	github.com/go-git/go-git/v5 v5.19.2
	github.com/google/uuid v1.6.0
	github.com/invopop/jsonschema v0.14.0
	github.com/itchyny/gojq v0.12.19
	github.com/joho/godotenv v1.5.1
	github.com/jordanella/go-ansi-paintbrush v0.0.0-20240728195301-b7ad996ecf3d
	github.com/labstack/echo/v5 v5.3.1
	github.com/lucasb-eyer/go-colorful v1.4.1
	github.com/mattn/go-isatty v0.0.24
	github.com/mbndr/figlet4go v0.0.0-20190224160619-d6cef5b186ea
	github.com/modelcontextprotocol/go-sdk v1.7.0
	github.com/ncruces/go-sqlite3 v0.35.3
	github.com/nxadm/tail v1.4.11
	github.com/openai/openai-go/v3 v3.50.0
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c
	github.com/pressly/goose/v3 v3.27.3
	github.com/rivo/uniseg v0.4.7
	github.com/robfig/cron/v3 v3.0.1
	github.com/sahilm/fuzzy v0.1.3
	github.com/sourcegraph/jsonrpc2 v0.2.2
	github.com/spf13/cobra v1.10.2
	github.com/stretchr/testify v1.12.0
	github.com/swaggo/http-swagger/v2 v2.0.2
	github.com/swaggo/swag v1.16.6
	github.com/tidwall/gjson v1.19.0
	github.com/tidwall/sjson v1.2.5
	github.com/tree-sitter/go-tree-sitter v0.25.0
	github.com/zeebo/xxh3 v1.1.0
	go.opentelemetry.io/otel v1.45.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.45.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.45.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.45.0
	go.opentelemetry.io/otel/metric v1.45.0
	go.opentelemetry.io/otel/sdk v1.45.0
	go.opentelemetry.io/otel/trace v1.45.0
	go.uber.org/goleak v1.3.0
	golang.design/x/clipboard v0.9.0
	golang.org/x/exp v0.0.0-20260718201538-764159d718ef
	golang.org/x/net v0.58.0
	golang.org/x/sync v0.22.0
	golang.org/x/sys v0.47.0
	golang.org/x/text v0.41.0
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.57.0
	mvdan.cc/sh/moreinterp v0.0.0-20251208190329-32c5db00fe6b
	mvdan.cc/sh/v3 v3.13.1
)

require (
	github.com/anthropics/anthropic-sdk-go v1.63.1 // indirect
	github.com/go-openapi/swag/pools v0.28.0 // indirect
	github.com/hhrutter/tiff v1.0.6 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
)

require (
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/auth v0.23.1 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	git.sr.ht/~jackmordaunt/go-toast v1.1.2 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.22.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.12.0 // indirect
	github.com/KyleBanks/depth v1.2.1 // indirect
	github.com/andybalholm/cascadia v1.3.4 // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/attilabuti/striprtf v1.0.0
	github.com/aws/aws-sdk-go-v2 v1.43.5 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.17 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.32.36 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.35 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.36 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.36 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.36 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.37 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.16 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.36 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.5.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.33.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.38.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.45.5 // indirect
	github.com/aws/smithy-go v1.27.7 // indirect
	github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.2.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charmbracelet/bubbles v1.0.0 // indirect
	github.com/charmbracelet/bubbletea v1.3.10 // indirect
	github.com/charmbracelet/x/cellbuf v0.0.15 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/coregx/gxpdf v0.9.4
	github.com/cyphar/filepath-securejoin v0.7.0 // indirect
	github.com/dlclark/regexp2/v2 v2.5.1 // indirect
	github.com/ebitengine/purego v0.10.2 // indirect
	github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect
	github.com/esiqveland/notify v0.14.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.9.0 // indirect
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68 // indirect
	github.com/go-logfmt/logfmt v0.6.1 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v1.0.0 // indirect
	github.com/go-openapi/jsonreference v1.0.0 // indirect
	github.com/go-openapi/spec v0.22.9 // indirect
	github.com/go-openapi/swag/conv v0.28.0 // indirect
	github.com/go-openapi/swag/jsonutils v0.28.0 // indirect
	github.com/go-openapi/swag/loading v0.28.0 // indirect
	github.com/go-openapi/swag/stringutils v0.28.0 // indirect
	github.com/go-openapi/swag/typeutils v0.28.0 // indirect
	github.com/go-openapi/swag/yamlutils v0.28.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.21 // indirect
	github.com/googleapis/gax-go/v2 v2.23.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/itchyny/timefmt-go v0.1.8 // indirect
	github.com/jackmordaunt/icns/v3 v3.0.1 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/kaptinlin/jsonpointer v0.4.28 // indirect
	github.com/kaptinlin/jsonschema v0.9.8 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/klauspost/pgzip v1.2.6 // indirect
	github.com/lrstanley/bubblezone v1.0.0 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.27 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/mango v0.2.0 // indirect
	github.com/muesli/mango-cobra v1.3.0 // indirect
	github.com/muesli/mango-pflag v0.2.0 // indirect
	github.com/muesli/roff v0.1.0 // indirect
	github.com/muesli/termenv v0.16.0 // indirect
	github.com/ncruces/go-sqlite3-wasm/v3 v3.2.35304 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/pdfcpu/pdfcpu v0.15.0
	github.com/pierrec/lz4/v4 v4.1.27 // indirect
	github.com/pjbgf/sha1cd v0.6.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/richardlehane/mscfb v1.0.7 // indirect
	github.com/richardlehane/msoleps v1.0.6 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/sergeymakinen/go-bmp v1.0.0 // indirect
	github.com/sergeymakinen/go-ico v1.0.0 // indirect
	github.com/sethvargo/go-retry v0.4.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/swaggo/files/v2 v2.0.2 // indirect
	github.com/tadvi/systray v0.0.0-20190226123456-11a2b8fa57af // indirect
	github.com/tidwall/match v1.2.0 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tiendc/go-deepcopy v1.7.2 // indirect
	github.com/tree-sitter/tree-sitter-c v0.24.2 // indirect
	github.com/tree-sitter/tree-sitter-go v0.25.0 // indirect
	github.com/tree-sitter/tree-sitter-javascript v0.25.0 // indirect
	github.com/tree-sitter/tree-sitter-php v0.24.2 // indirect
	github.com/tree-sitter/tree-sitter-python v0.25.0 // indirect
	github.com/tree-sitter/tree-sitter-rust v0.24.2 // indirect
	github.com/u-root/u-root v0.16.0 // indirect
	github.com/u-root/uio v0.0.0-20240224005618-d2acac8f3701 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/xuri/efp v0.0.1 // indirect
	github.com/xuri/excelize/v2 v2.11.0
	github.com/xuri/nfp v0.0.2-0.20250530014748-2ddeb826f9a9 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	github.com/yuin/goldmark v1.8.4 // indirect
	github.com/yuin/goldmark-emoji v1.0.6 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.70.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.70.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.6 // indirect
	golang.design/x/x11 v0.2.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/exp/shiny v0.0.0-20260709172345-9ea1abe57597 // indirect
	golang.org/x/image v0.45.0 // indirect
	golang.org/x/mobile v0.0.0-20260709172247-6129f5bee9d5 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	google.golang.org/api v0.293.0 // indirect
	google.golang.org/genai v1.68.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260810153831-ec0a7760b754 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260810153831-ec0a7760b754 // indirect
	google.golang.org/grpc v1.83.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gopkg.in/dnaeon/go-vcr.v4 v4.0.6-0.20251110073552-01de4eb40290 // indirect
	gopkg.in/tomb.v1 v1.0.0-20141024135613-dd632973f1e7 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
