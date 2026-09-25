# C4 level 3: components

| | |
|---|---|
| Status | Draft for review |
| Date | 2026-08-31 |
| Scope | The inside of `collector`, `engine` and `api` |

## The collector

![Collector components](diagrams/components-collector.svg)

A worker pulls a task from the frontier and takes one of two paths.

A `find` task goes through the fingerprinter: SNMP first for `sysObjectID` and `sysDescr`, one SSH command when SNMP stays silent. It comes back with a platform and with the strong identifiers the device gave up, which are written straight away. That write is what stops the crawl from visiting the same device twice through two management addresses, and it is also what lets the neighbours be queued immediately. A `scrape` task for the same device is queued at the same moment, and runs on its own.

A `scrape` task goes through the recipe engine, which asks the pack registry what to run on this platform and this OS version, runs the steps in one session, and merges them. The output goes to the parser, which applies the template, the post-processing, the mapping to the schema and the interface naming rules. The writer stores the raw output under its hash and the rows as observations.

| Component | Responsibility |
|---|---|
| Worker pool | claims `find` and `scrape` tasks, holds and renews the lease, gives up after too many reclaims |
| Boundary check | refuses a target outside the perimeter before any packet leaves |
| Fingerprinter | identifies the platform, extracts the strong identifiers, records an unidentified platform as a state |
| Recipe engine | selects the implementation by platform, version and transport preference, runs the steps, applies the merge |
| Parser and mapper | template, post-processing, mapping to the fact family schema, interface name canonicalisation |
| Transports | SSH sessions with their driver, SNMP client. One session per device per task |
| Pack registry | the loaded packs: fingerprint rules, recipes, templates, naming rules |
| Secret resolver | turns a reference into a value for the time it is needed, in memory only |
| Probe | unauthenticated qualification of an endpoint found in a MAC or ARP table |
| Writer | raw output to the object store by hash, observations and identifier claims to PostgreSQL |

Two of these carry the extensibility. The pack registry is the only component that knows anything vendor-specific, and the parser is the only one that touches a template. Everything else works the same on every platform, which is what makes a new vendor a directory of data rather than a patch.

## The engine

![Engine components](diagrams/components-engine.svg)

Nothing here touches a device. The engine reads what the collector wrote, computes, and writes back. It loads platform packs for one thing only, the interface naming rules the graph projector needs for the far end of a cable.

The scheduler fires and the job runner enqueues a run. When a run's queue empties, the same runner closes the snapshot and the chain of computation follows: the identity resolver turns claims into entities, the graph projector builds vertices and edges with their evidence, the gate measures coverage and decides how the snapshot is published, the diff engine compares it to the previous one, and the reconciler compares it to intent.

| Component | Responsibility |
|---|---|
| Scheduler | cron definitions, one active run per perimeter, missed occurrences skipped |
| Job runner | job states, progress counters, the final retry pass before a snapshot closes |
| Identity resolver | groups identifier claims, merges on strong ones, quarantines conflicts, applies splits |
| Graph projector | interfaces with every spelling ever seen, and typed edges, each with its evidence and how well it is known. Builds `l1_link` and `has_address` today; `attached` and `protocol_adjacency` wait on fact families nothing collects yet |
| Snapshot gate | coverage metrics, then published, degraded or quarantined |
| Diff engine | two snapshots compared, with the rule that only a successful collection allows a disappearance |
| Reconciler | matches the observed graph against an intent version, produces the gap findings |
| Replay | a new parse generation over stored raw output, then a recomputation |
| Maintenance | eviction, orphan sweep on the object store, partition drops and vacuum |
| Findings | every component above raises into the same place, with a domain and a severity |

## The api

Thin by design: it validates, it reads, it enqueues. It computes nothing.

| Component | Responsibility |
|---|---|
| Auth | bearer tokens with one scope, `read`, which covers every endpoint. The set grows when there is something to mutate, and audit of mutating calls arrives with the first one (005) |
| Config service | validates the YAML document, stores it as objects, versions it. Not built: configuration is posted from the operator CLI (005 serves reads only) |
| Job endpoints | triggers a run, a replay or an import, returns progress. Not built: jobs are triggered from the operator CLI |
| Query layer | graph, snapshots, diffs and findings, with the evidence attached to every answer |
| MCP server | the same queries, in the shape an agent consumes. Not built yet |

The query layer is where the promise of the whole project is either kept or lost. An answer that arrives without its evidence and its age is the same answer any other tool would give.
