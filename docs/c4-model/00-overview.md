# NetMapper, in plain words

| | |
|---|---|
| Status | Draft for review |
| Date | 2026-08-27 |
| Purpose | The shortest possible statement of what the tool does, and the words it does it with |

## What it does

NetMapper logs into a network, one device at a time, starting from a device you name and following what each one says about its neighbours. It collects a small set of facts from each of them, keeps the raw output exactly as the device printed it, and turns all of that into a graph: which device, which port, connected to which port on which other device.

Three things make it different from a script that does the same thing.

It says how it knows. Every device, every link, every MAC address behind a port carries the observation it came from, when it was collected, and how confident the tool is. A link both endpoints agree on and a link deduced from a forwarding table are not the same claim, and the graph says so.

It remembers. Each run produces a snapshot, snapshots are immutable, and two of them can be compared. That comparison is what tells you a device appeared, a link went away, or a neighbour moved to another port.

It compares with what was declared. Given NetBox, or any other source of intent, it reports what exists and was never documented, what is documented and was never seen, and where the two disagree. The first of those is the shadow IT question.

## What it does not do

No configuration backup, no compliance testing, no metrics or alerting, no configuration changes. It reads, it remembers, and it reports.

## The words

These are used with one meaning each, everywhere.

| Word | Means |
|---|---|
| Perimeter | the address ranges NetMapper is allowed to touch. Without one, nothing starts |
| Seed | the device a crawl starts from |
| Run | one execution of a discovery. It has a start, a duration, and a result |
| Snapshot | everything one Run collected, frozen. It outlives the Run |
| Observation | one statement, from one source, about one device, at one moment. Never modified |
| Entity | a resolved subject: a device, an interface, an endpoint. Computed from observations, never collected directly |
| Edge | a relation between two entities: a cable, an attachment, a routing session. Carries its evidence |
| Fact family | a unit of meaning such as "the LLDP neighbours" or "the MAC table", with a stable schema |
| Recipe | how to obtain one fact family on one platform: a command, a template, an OID |
| Platform pack | a directory of recipes, templates and naming rules for one platform. Data, not code |
| Finding | something worth reporting: a device nobody declared, a template that stopped matching, a merge the tool could not decide |
| Intent | what someone declared should exist. Lives beside the graph and never inside it |
| Job | any execution: a discovery run, a replay of parsers over stored output, an intent import, a maintenance pass |

## The three roles

NetMapper is one program that runs in three roles, deployed as three containers from one image.

The `collector` talks to the network and is the only one holding device credentials. Nobody can reach it.

The `engine` computes: it resolves identities, builds the graph, compares snapshots, reconciles with intent, and schedules work. It never touches a device and never holds a secret.

The `api` faces users and agents. It reads and writes through the database and reaches no device either.

That separation exists so that the part anyone can reach is never the part that can log into every switch in the building.
