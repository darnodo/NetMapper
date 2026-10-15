package collector_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darnodo/NetMapper/internal/pack"
	. "github.com/darnodo/NetMapper/internal/testutil"
	"github.com/darnodo/NetMapper/internal/transport/fake"
)

// churnRecipe is a bgp_peers recipe for a copy of fakeos whose family step must only add to the
// rows of the peer step, as the Arista summaries do (issue #44).
const churnRecipe = `family: bgp_peers
implementations:
  - id: bgp-cli
    versions: '>=1.0'
    transport: ssh
    steps:
      - command: display peers
        template: display_peers.textfsm
        map: { vrf: VRF, peer_address: PEER, peer_as: AS, state: STATE, local_as: '=65000', router_id: '=9.9.9.9', type: '=ebgp' }
      - command: display family ipv4
        template: display_family.textfsm
        join_on: [vrf, peer_address]
        unmatched: report
        map: { vrf: VRF, peer_address: PEER, configured_address_families: '=ipv4 unicast' }
    merge_on: [vrf, peer_address]
    covers:
      configured_address_families: [ipv4 unicast]
    values:
      state: { Idle: idle, '*': other }
    defaults: { shutdown: 'no', dynamic: 'no' }
`

// Issue #44: a peer the family step lists and the peer step does not (it connected between the two
// commands) gets no row, and a collection_disagreement finding names the device and the peer and
// cites the observation, which is still collected with the other peer.
func TestCollectionDisagreement(t *testing.T) {
	env(t)
	dir := filepath.Join(t.TempDir(), "fakeos")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(Root(), "internal", "pack", "testdata", "fakeos"))); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "pack.yaml"))
	os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(strings.Replace(string(b), "version: 1\n", "version: 1\npack_schema: 2\n", 1)), 0o644)
	os.WriteFile(filepath.Join(dir, "recipes", "bgp_peers.yaml"), []byte(churnRecipe), 0o644)
	reg, err := pack.Load(filepath.Join(Root(), "packs", "_base"), dir)
	if err != nil {
		t.Fatal(err)
	}

	sw := FakeOS("sw1", "S001")
	sw.CLI["display peers"] = "vrf default\npeer 10.1.1.1 as 65001 state Idle\n"
	sw.CLI["display family ipv4"] = "vrf default\nmember 10.1.1.1\nmember 10.1.1.2\n"
	l := NewLab(t, &fake.Network{Devices: map[netip.Addr]*fake.Device{Addr("10.0.0.1"): sw}})
	l.Registry = reg
	l.Crawl(Doc)

	if got := l.Strings(`SELECT status || ' ' || jsonb_array_length(parsed)::text FROM observation WHERE fact_family = 'bgp_peers'`); len(got) != 1 || got[0] != "collected 1" {
		t.Errorf("observation %v, want collected with 1 row", got)
	}
	f := l.Strings(`SELECT f.domain || ' ' || f.category || ' ' || f.severity || ' ' || f.subject_ref || ' ' ||
		(f.detail ->> 'family') || ' ' || (f.detail ->> 'command') || ' | ' || (f.detail ->> 'row') || ' | ' || o.fact_family
		FROM finding f JOIN finding_evidence e ON e.finding_id = f.id
		JOIN observation o ON o.snapshot_id = e.snapshot_id AND o.id = e.observation_id`)
	want := "data_quality collection_disagreement warning target:10.0.0.1 bgp_peers display family ipv4 | vrf=default, peer_address=10.1.1.2 | bgp_peers"
	if len(f) != 1 || f[0] != want {
		t.Errorf("findings %v\nwant [%s]", f, want)
	}
}
