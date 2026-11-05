# Dispatch Kernel — Interview Study Guide

இந்த guide, இந்த repository-யில் உள்ள dispatch project-ஐ புரிந்து கொண்டு,
interview-ல் architecture, implementation, trade-offs, மற்றும் limitations
பற்றி தெளிவாக விளக்குவதற்காக எழுதப்பட்டது. இது project-ஐ மட்டும் முழுமையாக
விளக்குகிறது; முழு Go/GIS interview syllabus அல்ல.

## 1. 60-second project introduction

> “I built a modular Go dispatch service. It stores candidate locations in a
> persistent bbolt database and maintains an H3 spatial index for nearby
> candidate search. The dispatch engine applies optional matching policies,
> requests a batched route matrix from Valhalla, scores reachable candidates,
> and returns them in deterministic rank order. The same engine is available
> through REST and gRPC. I added unit tests, benchmarks, pprof support, and an
> opt-in test that launches 100,000 simultaneous in-process dispatches. That
> stress test uses a fake router; it does not prove 100,000 network requests or
> sub-millisecond end-to-end latency.”

### தமிழ் விளக்கம்

Candidate-களின் location-ஐ bbolt-ல் persistent-ஆக வைக்கிறோம்; H3 index மூலம்
pickup-க்கு அருகிலுள்ளவர்களைத் தேடுகிறோம். Optional matcher-கள் filter
செய்த பிறகு, Valhalla-விடம் ஒரே matrix request-ல் பல route-களைப் பெற்று,
reachable candidate-களை scorer மூலம் rank செய்து திருப்புகிறோம். REST மற்றும்
gRPC இரண்டும் ஒரே kernel-ஐப் பயன்படுத்துகின்றன. 100k test என்பது உள்ளக Go
dispatch calls-க்கானது; உண்மையான network/Valhalla 100k load அல்லது sub-ms
latency என்று அர்த்தமில்லை.

## 2. System architecture

```text
REST :8080 ─────┐
                ├──> kernel.Engine
gRPC :9090 ─────┘        │
                         ├── Matcher(s): optional geofence/policy filters
                         ├── CandidateSource
                         │     └── BoltIndex
                         │          ├── bbolt: durable candidate records
                         │          └── H3Index: in-memory cell-to-candidate index
                         ├── Router
                         │     └── Valhalla /sources_to_targets matrix
                         └── Scorer: duration or fare
```

- **Transport layer:** HTTP JSON handler and gRPC server translate external
  requests into kernel types. They do not implement matching/routing policy.
- **Kernel:** coordinates candidate search, matchers, routing, scoring, and
  ranking through Go interfaces.
- **Spatial layer:** maps coordinates to H3 cells, searches a configured
  cell-radius neighborhood, and returns the nearest limited candidates.
- **Storage layer:** bbolt stores candidates durably. The H3 index is rebuilt
  from that database at startup.
- **Routing adapter:** batches destination coordinates into a Valhalla matrix
  API call and translates results into kernel routes.
- **Policy layer:** optional polygon geofence and fare scorer are examples of
  replaceable business logic.

The kernel interfaces are in [`internal/kernel/types.go`](internal/kernel/types.go);
the main orchestration is [`internal/kernel/engine.go`](internal/kernel/engine.go).

## 3. Dispatch request lifecycle

When a client calls `Dispatch`:

1. REST/gRPC decodes the request and applies the default candidate limit
   (100); the maximum is 500.
2. The kernel validates request ID, coordinates, and limit.
3. `CandidateSource.Search` asks the H3 index for candidates in the pickup
   cell's configured `H3_RADIUS_CELLS` neighborhood.
4. The index computes haversine distance and keeps only the nearest `limit`
   candidates, using a max-heap. It returns them nearest-first.
5. Optional `Matcher` implementations reject candidates. The geofence example
   requires both pickup and candidate point to lie inside its polygon.
6. The kernel sends the remaining locations to `Router.Routes` as one batch.
7. Valhalla returns a source-to-target matrix. Null/unavailable cells are
   represented as unreachable routes.
8. The kernel skips unreachable candidates, validates route metrics, asks the
   scorer for a cost, and sorts by ascending cost. Equal costs are ordered by
   candidate ID for deterministic output.
9. The transport maps the response to JSON or protobuf.

### Important distinction

H3 candidate search is an approximate geographic prefilter, not road routing.
`H3_RADIUS_CELLS` is a number of grid steps, **not meters**. Valhalla then
snaps locations to roads and calculates road-network travel costs. If Valhalla
has no suitable nearby road edge, a route can fail even if H3 found the
candidate.

## 4. Go concepts used in this project

### Interfaces and dependency injection

```go
type CandidateSource interface {
    Search(ctx context.Context, around Location, limit int) ([]Candidate, error)
}
```

`Engine` depends on behavior (`CandidateSource`, `Router`, `Scorer`, `Matcher`),
not concrete packages. Tests inject fake implementations; production startup
injects bbolt, Valhalla, and selected policy objects. This makes the kernel
testable and allows implementations to change without rewriting orchestration.

### Pointer declaration from the project

```go
var index *spatial.H3Index // declares a nil pointer; no index is created yet
index, err := spatial.NewH3Index(9, 2) // constructs and returns an index
```

`*T` is a pointer to a `T`. A zero-valued pointer is `nil`, so it must not be
dereferenced until assigned. `NewH3Index` returns `(*H3Index, error)` because
invalid configuration can fail.

### Context and cancellation

`context.Context` flows from HTTP/gRPC request to candidate search, matching,
routing, and scoring. When a client cancels or times out, implementations can
stop unnecessary work. The Valhalla adapter uses
`http.NewRequestWithContext`.

### Errors

The kernel wraps lower-level errors with operation context (`search
candidates`, `route candidates`, `score candidate`). Request validation errors
are distinguished using sentinel errors and `errors.Is`. Transports map invalid
input to client errors and unexpected dependency failures to server errors;
internal causes are logged rather than returned as a success-shaped response.

### Concurrency and synchronization

- `H3Index` uses `sync.RWMutex`: parallel searches can hold read locks;
  upsert/delete require exclusive access.
- `BoltIndex` serializes mutations and coordinates database/index consistency
  with a mutex. Search holds a read lock while querying the in-memory H3 index.
- `http.Server` and gRPC serve concurrent requests using Go's network runtime.
- The stress test starts one goroutine per request and releases them together.

This is **not lock-free**. A mutex is a clear, correct baseline. Whether
sharding, immutable snapshots, or a more specialized structure helps must be
measured under representative read/write ratios.

### `sync.Pool` and lock-free structures

This project does **not** use `sync.Pool` or lock-free data structures. Do not
claim otherwise in an interview. A pool is useful only when profiling shows
short-lived reusable allocations are a bottleneck and pooled values can be
safely reset. Pool contents may be discarded by the runtime; it is not durable
storage or a cache with retention guarantees.

## 5. Spatial search details

`H3Index` maintains:

- `cells`: H3 cell → candidate ID → candidate.
- `byID`: candidate ID → its current H3 cell, for efficient update/delete.

Upserting a moved candidate removes its old cell entry and adds the new one.
Search:

1. Converts pickup latitude/longitude into an H3 cell at configured resolution.
2. Uses `GridDisk(radius)` to enumerate nearby cells.
3. Computes great-circle distance using the haversine formula.
4. Maintains a max-heap of at most `limit` nearest candidates.
5. Sorts the retained candidates nearest-first, with ID tie-breaking.

The heap avoids sorting every result when a cell neighborhood has many
candidates. If `M` candidates are in the searched cells and the response limit
is `K`, selection is approximately `O(M log K)` plus `O(K log K)` final sort,
instead of sorting all `M` candidates. H3 neighborhood generation and
trigonometric distance calculations also have cost.

H3 resolution controls cell size: higher resolutions make smaller cells.
The best resolution/radius depends on geography, density, and desired search
area, and should be validated against real coordinates. H3 candidate search
does not guarantee a road-network nearest candidate or exact meter-radius
geofence.

## 6. Routing with Valhalla

The adapter posts to:

```text
POST {VALHALLA_URL}/sources_to_targets
```

It sends one source (pickup), many targets (candidate locations), and a
costing model (`auto` by default). Valhalla matrix distance is in kilometers;
the adapter converts it to meters. Travel time is represented in seconds.
Unreachable/null cells are skipped by the kernel.

The HTTP client has a timeout; request context supports cancellation; response
body size is bounded. Non-2xx status codes and malformed response shapes
become errors.

Valhalla is a separate Docker service, not part of the Go binary. It needs
OpenStreetMap data and routing tiles. `/status` only indicates that the server
responds; `/locate` should also return road edges for the test coordinate
before routing can work.

## 7. Persistence with bbolt

bbolt is an embedded, disk-backed key/value database. The default database is
`candidates.db`; the bucket key is candidate ID and the value is JSON-encoded
candidate data.

- Startup opens the database and rebuilds the in-memory H3 index from stored
  records.
- Upsert validates and encodes a candidate, commits it to bbolt, then updates
  the H3 index.
- Delete removes the persisted record and its H3 entry.
- Close happens during graceful shutdown.

This is appropriate for a single local service instance. bbolt is a
single-writer embedded database; it is not a distributed database or shared
multi-instance candidate store. Multi-instance deployment needs a deliberate
storage/index consistency design. Back up `candidates.db`.

## 8. Matching, geofencing, and scoring

### Matcher

```go
type Matcher interface {
    Match(ctx context.Context, request DispatchRequest, candidate Candidate) (bool, error)
}
```

Matchers run before Valhalla to avoid routing candidates that are already
ineligible. Multiple matchers compose as logical AND: the first rejection
skips the candidate.

### Polygon geofence

`policy.Geofence` uses a ray-casting point-in-polygon algorithm in latitude /
longitude coordinates and treats a point on the edge as inside. The sample
requires both pickup and candidate inside. This is a simple planar method for
local regions; production GIS should explicitly handle antimeridian crossing,
large polygons, polygon holes, coordinate reference systems, and boundary
accuracy as needed.

### Scorer

`Scorer.Cost` returns a comparable numeric cost; lower is ranked first.

- `DurationScorer`: route duration in seconds.
- `FareScorer`: `base + service fee + (distance km × rate/km) + (duration min × rate/min)`.

The fare implementation demonstrates a policy hook, not a production pricing
model. Actual commercial pricing, currency, rounding, taxes, surge, driver
payout, and fairness constraints need product requirements.

## 9. REST and gRPC

Both interfaces use the same kernel and bbolt-backed candidate store.

### REST — port 8080

```bash
# Health
curl http://localhost:8080/healthz

# Register candidate
curl -i -X PUT http://localhost:8080/v1/candidates/driver-1 \
  -H "Content-Type: application/json" \
  -d '{"location":{"latitude":37.775,"longitude":-122.418}}'

# Dispatch
curl -X POST http://localhost:8080/v1/dispatch \
  -H "Content-Type: application/json" \
  -d '{"id":"request-1","pickup":{"latitude":37.775,"longitude":-122.418},"max_candidates":100}'

# Delete candidate
curl -i -X DELETE http://localhost:8080/v1/candidates/driver-1
```

The REST decoder rejects unknown fields, rejects trailing JSON values, and
limits request bodies to 1 MiB. Candidate upsert/delete return `204` on
success. REST errors use HTTP status codes and a JSON `error` field.

### gRPC — loopback port 9090

The source schema is
[`api/proto/dispatch/v1/dispatch.proto`](api/proto/dispatch/v1/dispatch.proto).
Buf generates Go bindings in `gen/dispatch/v1`. RPCs:

- `UpsertCandidate`
- `DeleteCandidate`
- `Dispatch`

`grpcurl` quick test:

```bash
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
export PATH="$(cygpath -u "$(go env GOPATH)")/bin:$PATH"

grpcurl -plaintext -import-path api/proto \
  -proto dispatch/v1/dispatch.proto \
  -d '{"candidate":{"id":"grpc-driver","location":{"latitude":37.775,"longitude":-122.418}}}' \
  127.0.0.1:9090 dispatch.v1.DispatchService/UpsertCandidate

grpcurl -plaintext -import-path api/proto \
  -proto dispatch/v1/dispatch.proto \
  -d '{"id":"grpc-request-1","pickup":{"latitude":37.775,"longitude":-122.418},"maxCandidates":100}' \
  127.0.0.1:9090 dispatch.v1.DispatchService/Dispatch
```

An empty `{}` is the expected response for the empty upsert response message.
`grpcurl` uses protobuf JSON names (`maxCandidates`). It normally omits
zero-valued scalar fields from displayed output.

The default gRPC listener is plaintext and bound to `127.0.0.1`, not a
production-secure public endpoint. Add TLS, authentication, authorization,
and network controls before exposing it.

## 10. Run locally (Git Bash on Windows)

Start Docker Desktop first. Start Valhalla in one terminal (only needed once
for an existing container):

```bash
docker start valhalla
curl -s http://localhost:8002/status
```

Start the Go application in another terminal:

```bash
export VALHALLA_URL="http://localhost:8002"
go run ./cmd/dispatchd
```

The process serves REST on `:8080`, gRPC on `127.0.0.1:9090`, and persists
candidates in `candidates.db`. Candidate records persist after an app restart.
Stop Valhalla with `docker stop valhalla`; its map is in the Docker volume
`valhalla-data`.

Optional configuration:

```bash
export SCORING_MODE=fare
export FARE_BASE=2
export FARE_SERVICE_FEE=1
export FARE_PER_KILOMETER=1.5
export FARE_PER_MINUTE=0.6
export GEOFENCE_POLYGON='[{"latitude":37.70,"longitude":-122.52},{"latitude":37.70,"longitude":-122.35},{"latitude":37.83,"longitude":-122.35},{"latitude":37.83,"longitude":-122.52}]'
export PPROF_ADDR=127.0.0.1:6060
```

Set `PPROF_ADDR` only to a loopback address. Example profile commands:

```bash
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
go tool pprof http://localhost:6060/debug/pprof/heap
```

## 11. Tests, benchmarks, and what they prove

Run correctness checks:

```bash
go test ./...
go vet ./...
```

Run benchmarks:

```bash
go test ./internal/kernel ./internal/spatial -run '^$' \
  -bench 'BenchmarkDispatchPipeline|BenchmarkH3IndexSearchParallel' \
  -benchmem -cpu=1,2,4,8
```

Run the opt-in concurrency stress test:

```bash
DISPATCH_STRESS_CONCURRENCY=100000 go test ./internal/kernel \
  -run '^TestDispatchStress$' -count=1 -timeout=3m
```

Observed on an AMD Ryzen 5 7535HS / Windows / Go 1.26:

- In-process stress test: 100,000 simultaneous dispatch goroutines completed
  without failures (about 1.06 seconds in the most recent run).
- Synthetic dispatch benchmark: about 221 µs/op at `-cpu=1`, 46 µs/op at
  `-cpu=8` in the recorded run.
- Parallel H3 lookup benchmark: about 228 µs/op at `-cpu=1`, 34 µs/op at
  `-cpu=8` in the recorded run.

These are machine- and run-dependent microbenchmarks. The benchmark router is
fake and in-process. They do **not** include real Valhalla, network, HTTP/gRPC
transport, durable writes per request, production workload, or sustained
throughput. Thus they do not prove the job description's 100k production
capacity or sub-millisecond end-to-end target. The race detector also requires
cgo and a C compiler on this Windows setup.

## 12. Interview questions and suggested answers

### “Walk me through a dispatch request.”

Validate input → H3 candidate search → apply matchers → batch candidate
locations to Valhalla → remove unreachable routes → calculate score → sort by
cost (then ID) → serialize response.

### “Why H3?”

H3 converts coordinates into hierarchical hexagonal cells. It gives a
convenient spatial prefilter for local candidate lookup and neighborhood
expansion. It is not a routing graph; Valhalla handles road-network distance
and travel time.

### “Why batch Valhalla calls?”

One matrix request avoids one HTTP round trip per candidate and asks Valhalla
to calculate many source-to-target costs together. This reduces application
round trips, but matrix size and Valhalla capacity still need limits and load
testing.

### “How does the engine stay modular?”

It depends on small Go interfaces for candidate source, router, matcher, and
scorer. Tests inject fakes; adapters/policies can be swapped without embedding
business decisions in HTTP handlers.

### “How does it handle concurrency?”

Go serves requests concurrently. H3 uses an RWMutex for concurrent reads and
exclusive writes; bbolt mutations are serialized. We measured a 100k-goroutine
in-process test, but have not proven equivalent network throughput. The
current index is not lock-free.

### “What exactly does the performance test prove?”

Only the measured benchmark workload on the stated machine. The synthetic
router has no network or routing cost. The 100k stress test proves those
in-process calls completed without errors in one run; it is not a sustainable
RPS result or production SLA.

### “What would you do before production?”

Run representative end-to-end load tests against Valhalla; measure latency
percentiles, throughput, errors, CPU, allocations, heap, GC, and lock
contention with pprof; define overload/backpressure and timeouts; secure REST
and gRPC; add metrics/tracing; validate persistence/recovery; and make
multi-instance storage/index consistency explicit.

### “Why is the gRPC server plaintext?”

It is a local development default bound to loopback. Before remote access,
configure TLS and authentication/authorization. A loopback listener is not a
substitute for production security.

### “Does the current code assign a driver?”

No. It returns ranked reachable candidates. An assignment/acceptance workflow,
transactional reservation, retries, idempotency, and race-safe ownership are
separate product features.

### “What is not implemented?”

Production security/auth, real business pricing rules, durable multi-node
storage, distributed coordination, real traffic load tests, proven
sub-millisecond end-to-end latency, and production observability.

## 13. Honest interview framing

Describe this as a **working modular prototype / starter kernel** with a
measured in-process performance harness. Distinguish:

- feature works vs. production-ready;
- concurrent goroutines vs. concurrent network requests;
- H3 lookup benchmark vs. complete dispatch latency;
- fake-router benchmark vs. real Valhalla performance;
- one stress-test pass vs. guaranteed throughput/SLA.

Interviewers value precise measurement and clear trade-offs more than an
unsupported claim that the system already meets every target.
