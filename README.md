# archastro-go

Go SDK for the ArchAstro Platform API — generated from the canonical
OpenAPI spec by [`@archastro/sdk-generator`](https://github.com/ArchAstro/archastro-openapi),
with a hand-maintained runtime. The Go sibling of
[`archastro-js`](https://github.com/ArchAstro/archastro-js),
[`archastro-python`](https://github.com/ArchAstro/archastro-python), and
[`archastro-swift`](https://github.com/ArchAstro/archastro-swift).

Requires Go 1.23+. The only dependency is
[`gorilla/websocket`](https://github.com/gorilla/websocket), used by the
Phoenix channel client.

## Installation

```bash
go get github.com/ArchAstro/archastro-go
```

```go
import "github.com/ArchAstro/archastro-go/platform"
```

## Usage

```go
ctx := context.Background()

// Server-side (secret key)
client := platform.NewClient(
    platform.WithDefaultHeaders(map[string]string{"x-archastro-api-key": "sk_…"}),
)
// …or the generated factory for the same thing
client = platform.NewClientWithSecretKey("sk_…")

// App-side (publishable key + login). Wires automatic 401 token refresh.
client, err := platform.NewClientWithCredentials(ctx, "pk_…", "dev@example.com", "…")

// Resources — client.V1.Agents… or the default-version accessors
agents, err := client.Agents().List(ctx, platform.AgentListParams{
    Limit: platform.Ptr(20),
})
agent, err := client.Agents().Create(ctx, platform.AgentCreateInput{Name: "support-bot"})
```

Every method takes a `context.Context` first. Optional fields and optional
query parameters are pointers; `platform.Ptr` is the shorthand for filling
them from a literal.

### Streaming (SSE)

```go
stream, err := client.Ai().Chat.Completions.Stream(ctx, input)
if err != nil {
    return err
}
defer stream.Close()

for stream.Next() {
    event := stream.Event()
    fmt.Println(event.Event, event.Data)
}
if err := stream.Err(); err != nil {
    return err
}
```

A non-2xx response is returned by `Stream` itself, before any event is
yielded.

### Realtime channels (Phoenix)

```go
socket, err := client.OpenSocket(ctx)
if err != nil {
    return err
}
defer socket.Close()

chat, err := platform.JoinAPIChatChannelTeamThread(
    ctx, socket, teamID, threadID, nil, nil, nil, nil,
)
if err != nil {
    return err
}

unsubscribe := chat.OnMessageAdded(func(payload platform.APIChatMessageAddedPayload) {
    fmt.Println(payload)
})
defer unsubscribe()

reply, err := chat.APIChatPostMessage(ctx, platform.APIChatAPIChatPostMessageInput{
    Content: "hello",
})
```

Go has no static methods, so each channel's topic builder and join
constructor are package-level functions named after the channel type
(`APIChatChannelTopicTeamThread`, `JoinAPIChatChannelTeamThread`).

### Errors

Non-2xx responses surface as `*platform.APIError`; failed channel
operations as `*platform.ChannelError`. Both match with `errors.As`:

```go
var apiErr *platform.APIError
if errors.As(err, &apiErr) && apiErr.Status == 404 {
    // …
}
```

`Client.Close` disconnects every socket the client opened.

## Project structure

Go compiles one package per directory, so the whole SDK lives flat in
`platform/` alongside the runtime. Generated files carry a `Content hash`
header and a role prefix in the filename.

- `platform/types_*.go`, `platform/v1*.go`, `platform/channels_*.go`,
  `platform/client.go`, `platform/auth.go` — **generated, do not edit.**
- `platform/json.go`, `time.go`, `errors.go`, `options.go`, `http.go`,
  `sse.go`, `phx_socket.go`, `phx_channel.go` — hand-maintained runtime:
  `JSONValue`, the lenient `Time`, `HTTPClient` (auth headers, one-shot 401
  refresh, `APIError`, SSE), and the Phoenix channel client.
- `platform/*_test.go` — hand-written runtime unit tests.
- `contracttests/` — **generated** contract tests plus the hand-maintained
  support files (`support_test.go`, `harness_test.go`) that boot Prism and
  the channel-harness service.

## Regenerating the SDK

```bash
npm ci                      # generator + Prism + channel-harness
./scripts/regenerate_sdk.sh                 # spec from GitHub main
./scripts/regenerate_sdk.sh --local ../archastro-openapi   # local checkout
```

Config lives in `scripts/sdk-generator-config.json`. Env knobs:
`ARCHASTRO_OPENAPI_REF`, `ARCHASTRO_SDK_GENERATOR_BIN`.

The emitter writes structurally correct Go, not column-aligned Go, so the
script runs `gofmt -w` over the output. CI gates on `gofmt -l` being empty.

## Testing

```bash
go test ./platform/                               # runtime unit tests
go test ./...                                     # + REST contract tests (Prism)
ARCHASTRO_RUN_CHANNEL_CONTRACT_TESTS=1 go test ./...   # + channel/stream tests (harness)
```

Contract tests spawn Prism (`node_modules/.bin/prism`) against
`specs/platform-openapi.json` and — when the opt-in env var is set — the
`@archastro/channel-harness` service, exactly like the TypeScript, Python,
and Swift SDK suites. Overrides: `PRISM_PORT`, `PRISM_BIN`,
`OPENAPI_SPEC_PATH`, `ARCHASTRO_HARNESS_BIN`.

## Releasing

Go modules resolve versions from semver git tags — cut a release with:

```bash
git tag v0.1.0 && git push origin v0.1.0
```

The release workflow validates the tagged commit and publishes a GitHub
Release; `proxy.golang.org` picks the tag up on first fetch.
